// Package alert turns model.Event into channel-specific notifications and
// delivers them without ever slowing down the check pipeline: Publish is
// non-blocking, delivery happens on background workers, and storms of
// simultaneous failures are grouped into one message per chat channel
// (§8 of the design spec).
package alert

import (
	"fmt"
	"strconv"
	"strings"
	"time"

	"opskest/internal/model"
)

// Info is what the alert package needs to know about a monitor in order
// to build a Message: its display name, group, target and runbook, and
// which channels should hear about it. The engine owns the real monitor
// data; this is the small adapter shape it hands over through Lookup, so
// this package never imports the engine (which would create an import
// cycle: engine -> alert -> engine).
type Info struct {
	Name, Group, Target, Runbook string
	ChannelIDs                   []int64
}

// Lookup resolves a monitor id to its alerting Info. false means the
// monitor is unknown (e.g. deleted between the event firing and the
// dispatcher processing it), and the event is dropped.
type Lookup interface {
	AlertInfo(id int64) (Info, bool)
}

// Item is one monitor's worth of alert content: either the only entry in
// a standalone Message, or one line of a grouped storm Message.
type Item struct {
	MonitorID                                    int64
	Name, Group, Target, Runbook, Status, Reason string
	Since                                        time.Time
	Duration                                     time.Duration
	LatencyMs, Days                              int
	URL                                          string
	// Code and Response come from the check that triggered the alert: the
	// HTTP status and a short snippet of the response body.
	Code     int
	Response string
}

// Message is what a Sender renders into its channel's wire format. Items
// has one entry unless Grouped is true, in which case it holds every
// monitor folded into this storm message.
type Message struct {
	Kind    model.EventKind
	Title   string
	Items   []Item
	At      time.Time
	AppURL  string
	Grouped bool
	// Loc is the display time zone for "Time:" lines (nil = UTC).
	Loc *time.Location
	// Custom, when set, is the channel's rendered template: it replaces the
	// body of a single-monitor message (the headline stays).
	Custom string
}

// Title renders the headline for m, e.g. "🔴 DOWN: API Gateway",
// "🟢 RECOVERED: API Gateway (down 12m)", "🔴 7 monitors down", or
// "⚠️ Certificate expires in 6 days: API".
func Title(m Message) string {
	n := len(m.Items)
	var it Item
	if n > 0 {
		it = m.Items[0]
	}

	switch m.Kind {
	case model.EvDown:
		if m.Grouped {
			return fmt.Sprintf("🔴 %d monitors down", n)
		}
		return fmt.Sprintf("🔴 Down — %s", it.Name)

	case model.EvRecovered:
		if m.Grouped {
			return fmt.Sprintf("🟢 %d monitors recovered", n)
		}
		return fmt.Sprintf("🟢 Up — %s (was down %s)", it.Name, formatDuration(it.Duration))

	case model.EvDegraded:
		if m.Grouped {
			return fmt.Sprintf("🟡 %d monitors degraded", n)
		}
		return fmt.Sprintf("🟡 Degraded — %s", it.Name)

	case model.EvDegradedCleared:
		if m.Grouped {
			return fmt.Sprintf("🟢 %d monitors back to normal", n)
		}
		return fmt.Sprintf("🟢 Back to normal — %s", it.Name)

	case model.EvReminder:
		if m.Grouped {
			return fmt.Sprintf("🔴 %d monitors still down", n)
		}
		return fmt.Sprintf("🔴 Still down — %s (for %s)", it.Name, formatDuration(it.Duration))

	case model.EvCertExpiring:
		if m.Grouped {
			return fmt.Sprintf("⚠️ %d certificates expiring soon", n)
		}
		return fmt.Sprintf("⚠️ Certificate expires in %d day%s: %s", it.Days, plural(it.Days), it.Name)

	case model.EvDomainExpiring:
		if m.Grouped {
			return fmt.Sprintf("⚠️ %d domains expiring soon", n)
		}
		return fmt.Sprintf("⚠️ Domain expires in %d day%s: %s", it.Days, plural(it.Days), it.Name)

	case model.EvTest:
		return fmt.Sprintf("🔔 Test alert: %s", it.Name)

	case model.EvRegional:
		return fmt.Sprintf("🟠 Regional issue — %s", it.Name)

	case model.EvRegionalCleared:
		return fmt.Sprintf("🟢 Regional issue cleared — %s", it.Name)

	case model.EvLatencyHigh:
		return fmt.Sprintf("🐢 Slow — %s", it.Name)

	case model.EvLatencyCleared:
		return fmt.Sprintf("🟢 Response time back to normal — %s", it.Name)

	default:
		return string(m.Kind)
	}
}

// ReasonLine renders it's failure detail as one human-readable line, e.g.
// "HTTP 503 · down 12m · 840 ms". Empty parts are omitted; an item with
// nothing to say (e.g. a bare test item) returns "".
func ReasonLine(it Item) string {
	var parts []string
	if it.Reason != "" {
		parts = append(parts, it.Reason)
	}
	if it.Duration > 0 {
		parts = append(parts, "down "+formatDuration(it.Duration))
	}
	if it.LatencyMs > 0 {
		parts = append(parts, fmt.Sprintf("%d ms", it.LatencyMs))
	}
	if it.Days > 0 {
		parts = append(parts, fmt.Sprintf("expires in %d day%s", it.Days, plural(it.Days)))
	}
	return strings.Join(parts, " · ")
}

// isURL reports whether s looks like an absolute http(s) URL. It gates
// whether a rendered runbook value gets turned into a clickable link.
func isURL(s string) bool {
	return strings.HasPrefix(s, "http://") || strings.HasPrefix(s, "https://")
}

// defaultRunbookLine renders it's runbook as a plain "Runbook: <value>"
// display line, or "" when there's none. Every channel that doesn't need
// its own link markup (Teams, Google Chat, email, and anywhere else that
// calls bodyLines) uses this (§8.1/§8.6: every message carries the
// runbook).
func defaultRunbookLine(it Item) string {
	if it.Runbook == "" {
		return ""
	}
	return "Runbook: " + it.Runbook
}

// itemLine renders one line for it inside a message body: an optional
// "Group › Name" prefix, the reason line, its target, and a Runbook line
// when set. showGroup is true only for a standalone message's single
// item — a grouped listing already labels each block with its group
// header, so repeating it on every line would be noise (finding #2:
// "Group missing from single (non-grouped) messages"). renderRunbook lets
// a channel customize how the runbook value is displayed; nil falls back
// to defaultRunbookLine.
func itemLine(it Item, showGroup bool, renderRunbook func(Item) string) string {
	name := it.Name
	if showGroup && it.Group != "" {
		name = it.Group + " › " + it.Name
	}
	line := name
	if r := ReasonLine(it); r != "" {
		line += " — " + r
	}
	if it.Target != "" {
		line += " (" + it.Target + ")"
	}
	if renderRunbook == nil {
		renderRunbook = defaultRunbookLine
	}
	if rb := renderRunbook(it); rb != "" {
		line += "\n" + rb
	}
	return line
}

// bodyLines renders the descriptive body of m using the default
// (plain-text) runbook rendering. See bodyLinesWith for channels that
// need their own runbook markup (e.g. Slack's mrkdwn link).
func bodyLines(m Message) string {
	return bodyLinesWith(m, nil)
}

// bodyLinesWith renders the descriptive body of m: a single item line
// when standalone, or every item grouped by Group (§8.3: "listing the
// monitors, grouped by group") when m.Grouped. renderRunbook is passed
// through to itemLine for every item.
func bodyLinesWith(m Message, renderRunbook func(Item) string) string {
	if len(m.Items) == 0 {
		return ""
	}
	if m.Custom != "" && !m.Grouped {
		return m.Custom
	}
	if !m.Grouped {
		if renderRunbook == nil {
			return plainDetails(m, m.Items[0])
		}
		return itemLine(m.Items[0], true, renderRunbook)
	}

	order, groups := groupItems(m.Items)
	var b strings.Builder
	for i, g := range order {
		items := groups[g]
		if i > 0 {
			b.WriteString("\n")
		}
		label := g
		if label == "" {
			label = "Ungrouped"
		}
		fmt.Fprintf(&b, "%s (%d):\n", label, len(items))
		for _, it := range items {
			b.WriteString("  ")
			b.WriteString(itemLine(it, false, renderRunbook))
			b.WriteString("\n")
		}
	}
	return strings.TrimRight(b.String(), "\n")
}

// groupItems buckets items by Group, preserving first-seen order so
// bodyLines renders groups in a stable sequence.
func groupItems(items []Item) ([]string, map[string][]Item) {
	groups := map[string][]Item{}
	var order []string
	for _, it := range items {
		g := it.Group
		if _, ok := groups[g]; !ok {
			order = append(order, g)
		}
		groups[g] = append(groups[g], it)
	}
	return order, groups
}

// linkFor returns the single monitor's link for a standalone message, or
// the app's root URL for a grouped one (a storm message covers many
// monitors, so no single deep link applies).
func linkFor(m Message) string {
	if !m.Grouped && len(m.Items) == 1 {
		return m.Items[0].URL
	}
	return m.AppURL
}

// formatDuration renders d the way alert copy wants it: "45s", "12m", "1h5m"
// or "2d3h". Under a minute it shows whole seconds, never "0m".
func formatDuration(d time.Duration) string {
	if d < 0 {
		d = 0
	}
	if d < time.Minute {
		return fmt.Sprintf("%ds", int(d.Round(time.Second)/time.Second))
	}
	d = d.Round(time.Minute)

	days := int(d.Hours()) / 24
	hours := int(d.Hours()) % 24
	mins := int(d.Minutes()) % 60

	switch {
	case days > 0:
		return fmt.Sprintf("%dd%dh", days, hours)
	case hours > 0:
		return fmt.Sprintf("%dh%dm", hours, mins)
	default:
		return fmt.Sprintf("%dm", mins)
	}
}

func plural(n int) string {
	if n == 1 {
		return ""
	}
	return "s"
}

// Field is one labelled line of a detailed single-monitor alert.
// Code renders the value as inline code; Block as a preformatted block.
type Field struct {
	Label, Value string
	Code, Block  bool
}

// DetailFields lists what a single-monitor alert shows, in order:
// URL / Host, Group, Response (snippet), Details (reason, code, how long,
// latency), Time, Monitor ID, Runbook. Empty values are omitted.
func DetailFields(m Message, it Item) []Field {
	var f []Field
	if it.Target != "" {
		f = append(f, Field{Label: "URL / Host", Value: it.Target, Code: true})
	}
	if it.Group != "" {
		f = append(f, Field{Label: "Group", Value: it.Group})
	}
	if it.Response != "" {
		f = append(f, Field{Label: "Response", Value: it.Response, Block: true})
	}
	var details []string
	if it.Reason != "" {
		details = append(details, it.Reason)
	}
	if it.Code > 0 && !strings.Contains(it.Reason, strconv.Itoa(it.Code)) {
		details = append(details, "HTTP "+strconv.Itoa(it.Code))
	}
	switch {
	case m.Kind == model.EvRecovered && it.Duration > 0:
		details = append(details, "was down "+formatDuration(it.Duration))
	case it.Duration > 0:
		details = append(details, "down for "+formatDuration(it.Duration))
	}
	if it.LatencyMs > 0 {
		details = append(details, fmt.Sprintf("%d ms", it.LatencyMs))
	}
	if it.Days > 0 {
		details = append(details, fmt.Sprintf("expires in %d day%s", it.Days, plural(it.Days)))
	}
	if len(details) > 0 {
		f = append(f, Field{Label: "Details", Value: strings.Join(details, " · ")})
	}
	at := m.At
	if m.Loc != nil {
		at = at.In(m.Loc)
	}
	f = append(f, Field{Label: "Time", Value: at.Format("2006-01-02 15:04:05 MST")})
	if it.MonitorID > 0 {
		f = append(f, Field{Label: "Monitor ID", Value: strconv.FormatInt(it.MonitorID, 10), Code: true})
	}
	if it.Runbook != "" {
		f = append(f, Field{Label: "Runbook", Value: it.Runbook})
	}
	return f
}

// plainDetails renders DetailFields as plain "Label: value" lines, with a
// response block indented, for channels without rich text (Teams, Google
// Chat, email).
func plainDetails(m Message, it Item) string {
	var b strings.Builder
	for _, f := range DetailFields(m, it) {
		if f.Block {
			b.WriteString(f.Label + ":\n")
			for _, l := range strings.Split(f.Value, "\n") {
				b.WriteString("    " + l + "\n")
			}
			continue
		}
		b.WriteString(f.Label + ": " + f.Value + "\n")
	}
	return strings.TrimRight(b.String(), "\n")
}
