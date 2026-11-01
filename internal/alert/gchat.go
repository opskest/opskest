package alert

import (
	"context"

	"opskest/internal/store"
)

type gchatPayload struct {
	Text    string        `json:"text"`
	CardsV2 []gchatCardV2 `json:"cardsV2"`
}

type gchatCardV2 struct {
	CardID string    `json:"cardId"`
	Card   gchatCard `json:"card"`
}

type gchatCard struct {
	Header   gchatHeader    `json:"header"`
	Sections []gchatSection `json:"sections"`
}

type gchatHeader struct {
	Title string `json:"title"`
}

type gchatSection struct {
	Widgets []any `json:"widgets"`
}

type gchatSender struct{}

func (gchatSender) Send(ctx context.Context, cfg store.ChannelConfig, m Message) error {
	return postJSON(ctx, cfg.URL, nil, buildGChatPayload(m))
}

func buildGChatPayload(m Message) gchatPayload {
	widgets := []any{
		map[string]any{
			"textParagraph": map[string]any{"text": bodyLines(m)},
		},
		map[string]any{
			"buttonList": map[string]any{
				"buttons": []any{
					map[string]any{
						"text": "Open in opskest",
						"onClick": map[string]any{
							"openLink": map[string]any{"url": linkFor(m)},
						},
					},
				},
			},
		},
	}

	return gchatPayload{
		Text: Title(m),
		CardsV2: []gchatCardV2{{
			CardID: "opskest",
			Card: gchatCard{
				Header:   gchatHeader{Title: Title(m)},
				Sections: []gchatSection{{Widgets: widgets}},
			},
		}},
	}
}
