package alert

import (
	"context"
	"crypto/hmac"
	"crypto/sha256"
	"encoding/hex"
	"encoding/json"
	"fmt"
	"net/http"
	"time"

	"opskest/internal/store"
)

// webhookMonitor is the "monitor" sub-object in the webhook payload
// (§8.1's config table).
type webhookMonitor struct {
	ID      int64  `json:"id"`
	Name    string `json:"name"`
	Group   string `json:"group"`
	Target  string `json:"target"`
	Runbook string `json:"runbook,omitempty"`
}

// webhookPayload is the JSON body a webhook channel receives. Standalone
// messages set Monitor/Status/Reason/... for the one affected monitor;
// a grouped storm message sets Monitors instead and leaves the
// single-monitor fields zero.
type webhookPayload struct {
	Event     string           `json:"event"`
	Grouped   bool             `json:"grouped,omitempty"`
	Monitor   *webhookMonitor  `json:"monitor,omitempty"`
	Monitors  []webhookMonitor `json:"monitors,omitempty"`
	Status    string           `json:"status,omitempty"`
	Reason    string           `json:"reason,omitempty"`
	Since     int64            `json:"since,omitempty"` // unix ms
	DurationS int64            `json:"duration_s,omitempty"`
	LatencyMs int              `json:"latency_ms,omitempty"`
	Code      int              `json:"code,omitempty"`
	Response  string           `json:"response,omitempty"`
	URL       string           `json:"url"`
	// Message is the channel's rendered template, when it has one.
	Message string `json:"message,omitempty"`
}

type webhookSender struct{}

func (webhookSender) Send(ctx context.Context, cfg store.ChannelConfig, m Message) error {
	body, err := json.Marshal(buildWebhookPayload(m))
	if err != nil {
		return fmt.Errorf("alert: marshal webhook payload: %w", err)
	}

	headers := map[string]string{"Content-Type": "application/json"}
	for k, v := range cfg.Headers {
		headers[k] = v
	}
	if cfg.HMACSecret != "" {
		mac := hmac.New(sha256.New, []byte(cfg.HMACSecret))
		mac.Write(body)
		headers["X-Opskest-Signature"] = "sha256=" + hex.EncodeToString(mac.Sum(nil))
	}

	return doRequest(ctx, http.MethodPost, cfg.URL, headers, body)
}

func buildWebhookPayload(m Message) webhookPayload {
	p := webhookPayload{
		Event: string(m.Kind),
		URL:   linkFor(m),
	}
	if !m.Grouped {
		p.Message = m.Custom
	}

	if m.Grouped {
		p.Grouped = true
		p.Monitors = make([]webhookMonitor, 0, len(m.Items))
		for _, it := range m.Items {
			p.Monitors = append(p.Monitors, webhookMonitor{
				ID: it.MonitorID, Name: it.Name, Group: it.Group, Target: it.Target, Runbook: it.Runbook,
			})
		}
		return p
	}

	if len(m.Items) == 0 {
		return p
	}
	it := m.Items[0]
	p.Monitor = &webhookMonitor{ID: it.MonitorID, Name: it.Name, Group: it.Group, Target: it.Target, Runbook: it.Runbook}
	p.Status = it.Status
	p.Reason = it.Reason
	if !it.Since.IsZero() {
		p.Since = it.Since.UnixMilli()
	}
	p.DurationS = int64(it.Duration / time.Second)
	p.LatencyMs = it.LatencyMs
	p.Code, p.Response = it.Code, it.Response
	return p
}
