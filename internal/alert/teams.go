package alert

import (
	"context"

	"opskest/internal/store"
)

type teamsPayload struct {
	Type        string            `json:"type"`
	Attachments []teamsAttachment `json:"attachments"`
}

type teamsAttachment struct {
	ContentType string    `json:"contentType"`
	Content     teamsCard `json:"content"`
}

type teamsCard struct {
	Schema  string        `json:"$schema"`
	Type    string        `json:"type"`
	Version string        `json:"version"`
	Body    []any         `json:"body"`
	Actions []teamsAction `json:"actions"`
}

type teamsAction struct {
	Type  string `json:"type"`
	Title string `json:"title"`
	URL   string `json:"url"`
}

// teamsSender posts to a Teams Workflows webhook (not the old, retired
// Connectors webhook): the payload is a plain "message" envelope carrying
// an Adaptive Card 1.4 attachment.
type teamsSender struct{}

func (teamsSender) Send(ctx context.Context, cfg store.ChannelConfig, m Message) error {
	return postJSON(ctx, cfg.URL, nil, buildTeamsPayload(m))
}

func buildTeamsPayload(m Message) teamsPayload {
	body := []any{
		map[string]any{
			"type":   "TextBlock",
			"size":   "Medium",
			"weight": "Bolder",
			"wrap":   true,
			"text":   Title(m),
		},
		map[string]any{
			"type": "TextBlock",
			"wrap": true,
			"text": bodyLines(m),
		},
	}

	return teamsPayload{
		Type: "message",
		Attachments: []teamsAttachment{{
			ContentType: "application/vnd.microsoft.card.adaptive",
			Content: teamsCard{
				Schema:  "http://adaptivecards.io/schemas/adaptive-card.json",
				Type:    "AdaptiveCard",
				Version: "1.4",
				Body:    body,
				Actions: []teamsAction{{
					Type:  "Action.OpenUrl",
					Title: "Open in opskest",
					URL:   linkFor(m),
				}},
			},
		}},
	}
}
