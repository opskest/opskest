package alert

import (
	"context"
	"fmt"
	"strings"

	"opskest/internal/model"
	"opskest/internal/store"
)

// Slack attachment colors (§8, "Formats"): red for an active problem,
// green for a recovery, amber for an expiry warning.
const (
	slackColorDown = "#dc2626"
	slackColorUp   = "#059669"
	slackColorWarn = "#b45309"
)

type slackPayload struct {
	Text        string            `json:"text"`
	Attachments []slackAttachment `json:"attachments"`
}

type slackAttachment struct {
	Color  string `json:"color"`
	Blocks []any  `json:"blocks"`
}

type slackSender struct{}

func (slackSender) Send(ctx context.Context, cfg store.ChannelConfig, m Message) error {
	return postJSON(ctx, cfg.URL, nil, buildSlackPayload(m))
}

// slackEscape escapes the three characters Slack mrkdwn treats as markup
// (so an HTML error page shows as text instead of breaking the message).
func slackEscape(s string) string {
	return strings.NewReplacer("&", "&amp;", "<", "&lt;", ">", "&gt;").Replace(s)
}

// slackDetails renders a single-monitor alert: bold labels,
// code for the URL and ID, a code block for the response snippet.
func slackDetails(m Message, it Item) string {
	var b strings.Builder
	for _, f := range DetailFields(m, it) {
		v := slackEscape(f.Value)
		switch {
		case f.Block:
			fmt.Fprintf(&b, "*%s:*\n```%s```\n", f.Label, strings.ReplaceAll(v, "```", "'''"))
		case f.Code:
			fmt.Fprintf(&b, "*%s:* `%s`\n", f.Label, strings.ReplaceAll(v, "`", "'"))
		case f.Label == "Runbook":
			fmt.Fprintf(&b, "*Runbook:* %s\n", strings.TrimPrefix(slackRunbookLine(it), "Runbook: "))
		default:
			fmt.Fprintf(&b, "*%s:* %s\n", f.Label, v)
		}
	}
	return strings.TrimRight(b.String(), "\n")
}

// buildSlackPayload builds Block Kit blocks: a section with the details
// (or, for a storm, the grouped listing), then the "Open in opskest"
// button. Blocks are plain maps because Slack's block elements are
// shaped differently, which doesn't fit one struct cleanly.
func buildSlackPayload(m Message) slackPayload {
	var text string
	if m.Custom != "" && !m.Grouped {
		text = slackEscape(m.Custom)
	} else if !m.Grouped && len(m.Items) == 1 {
		text = slackDetails(m, m.Items[0])
	} else {
		text = bodyLinesWith(m, slackRunbookLine)
	}
	if len(text) > 2900 { // Slack caps a section's text at 3000 characters
		text = text[:2900] + "…"
	}
	section := map[string]any{
		"type": "section",
		"text": map[string]any{"type": "mrkdwn", "text": text},
	}
	actions := map[string]any{
		"type": "actions",
		"elements": []any{
			map[string]any{
				"type": "button",
				"text": map[string]any{"type": "plain_text", "text": "Open in opskest"},
				"url":  linkFor(m),
			},
		},
	}
	return slackPayload{
		// The title is the message text (bold); the attachment holds only the
		// details, so Slack never shows the title twice.
		Text: "*" + slackEscape(Title(m)) + "*",
		Attachments: []slackAttachment{{
			Color:  slackColorFor(m.Kind),
			Blocks: []any{section, actions},
		}},
	}
}

// slackRunbookLine renders it's runbook the way Slack mrkdwn wants it: a
// clickable link (§8.1: "Blocks: ... runbook") when the value looks like a
// URL, plain text otherwise.
func slackRunbookLine(it Item) string {
	if it.Runbook == "" {
		return ""
	}
	if isURL(it.Runbook) {
		return fmt.Sprintf("Runbook: <%s|link>", it.Runbook)
	}
	return "Runbook: " + it.Runbook
}

func slackColorFor(kind model.EventKind) string {
	switch kind {
	case model.EvDown, model.EvReminder:
		return slackColorDown
	case model.EvRecovered, model.EvDegradedCleared, model.EvRegionalCleared, model.EvLatencyCleared:
		return slackColorUp
	default:
		return slackColorWarn
	}
}
