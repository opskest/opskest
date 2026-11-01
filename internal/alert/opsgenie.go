package alert

import (
	"context"
	"errors"
	"fmt"
	"strings"

	"opskest/internal/model"
	"opskest/internal/store"
)

// Opsgenie base URLs, unexported package vars so tests can point
// them at an httptest server.
var (
	opsgenieBaseURLUS = "https://api.opsgenie.com"
	opsgenieBaseURLEU = "https://api.eu.opsgenie.com"
)

type opsgenieCreatePayload struct {
	Message     string `json:"message"`
	Alias       string `json:"alias"`
	Description string `json:"description,omitempty"`
	Source      string `json:"source"`
}

type opsgenieClosePayload struct {
	Source string `json:"source"`
}

// opsgenieSender never groups: the dispatcher always calls it with
// a single-item Message, one per monitor, so the alias used to create and
// later close an alert stays stable.
type opsgenieSender struct{}

func (opsgenieSender) Send(ctx context.Context, cfg store.ChannelConfig, m Message) error {
	if len(m.Items) == 0 {
		return errors.New("alert: opsgenie message has no items")
	}
	it := m.Items[0]

	base := opsgenieBaseURLUS
	if cfg.Region == "eu" {
		base = opsgenieBaseURLEU
	}
	alias := fmt.Sprintf("opskest-%d", it.MonitorID)
	headers := map[string]string{"Authorization": "GenieKey " + cfg.APIKey}

	if m.Kind == model.EvRecovered {
		url := fmt.Sprintf("%s/v2/alerts/%s/close?identifierType=alias", base, alias)
		return postJSON(ctx, url, headers, opsgenieClosePayload{Source: "opskest"})
	}

	// Every message carries the target (full URL on internal
	// channels — every channel here is internal) and the runbook.
	description := ReasonLine(it)
	if m.Custom != "" {
		description = m.Custom
	}
	if it.Target != "" {
		description = strings.TrimSpace(description + "\nTarget: " + it.Target)
	}
	if it.Runbook != "" {
		description = strings.TrimSpace(description + "\nRunbook: " + it.Runbook)
	}
	if it.URL != "" {
		description = strings.TrimSpace(description + "\n" + it.URL)
	}

	url := base + "/v2/alerts"
	return postJSON(ctx, url, headers, opsgenieCreatePayload{
		Message:     Title(m),
		Alias:       alias,
		Description: description,
		Source:      "opskest",
	})
}
