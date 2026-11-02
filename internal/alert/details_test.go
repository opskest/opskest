package alert

import (
	"strings"
	"testing"
	"time"

	"opskest/internal/model"
)

func TestSlackDetailedAlert(t *testing.T) {
	ist := time.FixedZone("IST", 5*3600+1800)
	m := Message{
		Kind: model.EvDown, At: time.Date(2026, 9, 28, 7, 42, 39, 0, time.UTC), Loc: ist,
		Items: []Item{{
			MonitorID: 30, Name: "aips-inviteapp-reg-events", Target: "https://apis-events.example.com/inviteapp/api/health",
			Reason: "HTTP 502 (expected 200-299)", Code: 502,
			Response: "<html>\n<head><title>502 Bad Gateway</title></head>\n</html>",
		}},
	}
	payload := buildSlackPayload(m)
	if payload.Text != "*🔴 Down — aips-inviteapp-reg-events*" {
		t.Errorf("text = %q", payload.Text)
	}
	got := payload.Attachments[0].Blocks[0].(map[string]any)["text"].(map[string]any)["text"].(string)
	if strings.Contains(got, "Down —") {
		t.Error("title repeated inside the attachment")
	}
	for _, want := range []string{
		"*URL / Host:* `https://apis-events.example.com/inviteapp/api/health`",
		"*Response:*\n```&lt;html&gt;\n&lt;head&gt;&lt;title&gt;502 Bad Gateway&lt;/title&gt;&lt;/head&gt;\n&lt;/html&gt;```",
		"*Details:* HTTP 502 (expected 200-299)",
		"*Time:* 2026-09-28 13:12:39 IST",
		"*Monitor ID:* `30`",
	} {
		if !strings.Contains(got, want) {
			t.Errorf("missing %q in:\n%s", want, got)
		}
	}
	if strings.Contains(got, "HTTP 502 · HTTP 502") {
		t.Error("code repeated when the reason already has it")
	}

	up := Message{Kind: model.EvRecovered, At: m.At, Items: []Item{{MonitorID: 30, Name: "x", Target: "https://x", Code: 200, Response: `{"status":"ok"}`, Duration: 30 * time.Second}}}
	upText := buildSlackPayload(up).Attachments[0].Blocks[0].(map[string]any)["text"].(map[string]any)["text"].(string)
	if !strings.Contains(upText, "```{\"status\":\"ok\"}```") || !strings.Contains(upText, "*Details:* HTTP 200 · was down 30s") || strings.Contains(upText, "<!") {
		t.Errorf("recovery text:\n%s", upText)
	}
}
