package alert

import (
	"context"
	"errors"
	"fmt"
	"strings"

	"opskest/internal/model"
	"opskest/internal/store"
)

// pagerdutyEventsURL is the PagerDuty Events API v2 endpoint. It's an
// unexported package var so tests can point it at an httptest server.
var pagerdutyEventsURL = "https://events.pagerduty.com/v2/enqueue"

type pagerdutyPayload struct {
	RoutingKey  string          `json:"routing_key"`
	EventAction string          `json:"event_action"`
	DedupKey    string          `json:"dedup_key"`
	Payload     pagerdutyDetail `json:"payload"`
	Client      string          `json:"client,omitempty"`
	ClientURL   string          `json:"client_url,omitempty"`
}

type pagerdutyDetail struct {
	Summary       string            `json:"summary"`
	Source        string            `json:"source"`
	Severity      string            `json:"severity"`
	CustomDetails map[string]string `json:"custom_details,omitempty"`
}

// pagerdutySender never groups (§8.3): the dispatcher always calls it
// with a single-item Message, one per monitor, so dedup_key stays stable
// across trigger/resolve pairs.
type pagerdutySender struct{}

func (pagerdutySender) Send(ctx context.Context, cfg store.ChannelConfig, m Message) error {
	if len(m.Items) == 0 {
		return errors.New("alert: pagerduty message has no items")
	}
	it := m.Items[0]

	action := "trigger"
	severity := "critical"
	if m.Kind == model.EvRecovered {
		action = "resolve"
		severity = "info"
	}

	var customDetails map[string]string
	if it.Runbook != "" {
		customDetails = map[string]string{"runbook": it.Runbook}
	}

	payload := pagerdutyPayload{
		RoutingKey:  cfg.RoutingKey,
		EventAction: action,
		DedupKey:    fmt.Sprintf("opskest-%d", it.MonitorID),
		Payload: pagerdutyDetail{
			Summary:       pagerdutySummary(m, it),
			Source:        it.Target,
			Severity:      severity,
			CustomDetails: customDetails,
		},
		Client:    "opskest",
		ClientURL: it.URL,
	}
	return postJSON(ctx, pagerdutyEventsURL, nil, payload)
}

// pagerdutySummary appends the failure reason, latency and runbook (§8.6:
// every message carries the runbook) to the title. Duration is
// deliberately left out here: Title already spells it out for Recovered
// ("(down 12m)"), and a fresh Down has none yet.
func pagerdutySummary(m Message, it Item) string {
	title := Title(m)

	var parts []string
	if it.Reason != "" {
		parts = append(parts, it.Reason)
	}
	if it.LatencyMs > 0 {
		parts = append(parts, fmt.Sprintf("%d ms", it.LatencyMs))
	}
	if it.Runbook != "" {
		parts = append(parts, "Runbook: "+it.Runbook)
	}
	if len(parts) == 0 {
		return title
	}
	return title + ": " + strings.Join(parts, " · ")
}
