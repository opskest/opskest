package api

import (
	"encoding/csv"
	"encoding/json"
	"net/http"
	"strconv"
	"strings"

	"opskest/internal/model"
)

// exportRow is the shape both export formats produce and import accepts
// back: group/tags/channels resolved to names (never ids, which aren't
// portable across a restore or between environments), and Settings as-is.
// It never carries a monitor's Secrets (BasicPass, BearerToken, or the
// secret-header value map) - those fields live only on model.Monitor,
// which this type never embeds. Settings.Headers itself is already
// secret-safe by construction (see model/monitor.go: a secret header's
// Value is only ever populated in Monitor.Secrets.Headers, never in
// Settings.Headers), and monitorToExportRow defensively re-clears it
// anyway so this holds even if that invariant is ever weakened elsewhere.
type exportRow struct {
	Name           string         `json:"name"`
	Type           string         `json:"type"`
	Target         string         `json:"target"`
	Group          string         `json:"group,omitempty"`
	Tags           []string       `json:"tags,omitempty"`
	Channels       []string       `json:"channels,omitempty"`
	IntervalS      int            `json:"intervalS"`
	RetryIntervalS int            `json:"retryIntervalS"`
	Retries        int            `json:"retries"`
	TimeoutMs      int            `json:"timeoutMs"`
	DegradedMs     int            `json:"degradedMs"`
	SSLWarnDays    int            `json:"sslWarnDays"`
	UpsideDown     bool           `json:"upsideDown"`
	ReminderMin    int            `json:"reminderMin"`
	Paused         bool           `json:"paused"`
	Description    string         `json:"description,omitempty"`
	Runbook        string         `json:"runbook,omitempty"`
	Settings       model.Settings `json:"settings"`
}

// csvHeader is the fixed column order for both the CSV export and the
// CSV template: snake_case, since this file is meant to be opened in a
// spreadsheet, unlike the rest of the API which is camelCase JSON.
var csvHeader = []string{
	"name", "type", "target", "group", "tags", "channels",
	"interval_s", "retry_interval_s", "retries", "timeout_ms", "degraded_ms", "ssl_warn_days",
	"upside_down", "reminder_min", "paused", "description", "runbook", "settings",
}

func monitorToExportRow(m *model.Monitor, groupByID, tagByID, chanByID map[int64]string) exportRow {
	row := exportRow{
		Name: m.Name, Type: string(m.Type), Target: m.Target,
		IntervalS: m.IntervalS, RetryIntervalS: m.RetryIntervalS, Retries: m.Retries,
		TimeoutMs: m.TimeoutMs, DegradedMs: m.DegradedMs, SSLWarnDays: m.SSLWarnDays,
		UpsideDown: m.UpsideDown, ReminderMin: m.ReminderMin, Paused: m.Paused,
		Description: m.Description, Runbook: m.Runbook, Settings: m.Settings,
	}
	if m.GroupID != nil {
		row.Group = groupByID[*m.GroupID]
	}
	for _, tid := range m.TagIDs {
		if n, ok := tagByID[tid]; ok {
			row.Tags = append(row.Tags, n)
		}
	}
	for _, cid := range m.ChannelIDs {
		if n, ok := chanByID[cid]; ok {
			row.Channels = append(row.Channels, n)
		}
	}

	// Defense in depth: never let a secret header's value leave this
	// process, even if the invariant that Settings.Headers only ever
	// carries a value for non-secret headers is ever weakened. This
	// builds a fresh slice rather than mutating in place, since
	// m.Settings.Headers is a live in-memory monitor's slice shared with
	// the engine.
	if len(m.Settings.Headers) > 0 {
		headers := make([]model.Header, len(m.Settings.Headers))
		for i, h := range m.Settings.Headers {
			headers[i] = h
			if headers[i].Secret {
				headers[i].Value = ""
			}
		}
		row.Settings.Headers = headers
	}

	return row
}

func rowToCSVRecord(row exportRow) []string {
	settingsJSON, _ := json.Marshal(row.Settings)
	return []string{
		row.Name, row.Type, row.Target, row.Group,
		strings.Join(row.Tags, ","), strings.Join(row.Channels, ","),
		strconv.Itoa(row.IntervalS), strconv.Itoa(row.RetryIntervalS), strconv.Itoa(row.Retries),
		strconv.Itoa(row.TimeoutMs), strconv.Itoa(row.DegradedMs), strconv.Itoa(row.SSLWarnDays),
		strconv.FormatBool(row.UpsideDown), strconv.Itoa(row.ReminderMin), strconv.FormatBool(row.Paused),
		row.Description, row.Runbook, string(settingsJSON),
	}
}

func (s *server) handleExportMonitors(w http.ResponseWriter, r *http.Request) {
	format := r.URL.Query().Get("format")
	if format == "" {
		format = "json"
	}
	if format != "json" && format != "csv" {
		writeErr(w, http.StatusBadRequest, "format must be json or csv", nil)
		return
	}

	ctx := r.Context()
	monitors, err := s.d.Store.ListMonitors(ctx)
	if err != nil {
		writeErr(w, http.StatusInternalServerError, "fetch failed", nil)
		return
	}
	groups, err := s.d.Store.ListGroups(ctx)
	if err != nil {
		writeErr(w, http.StatusInternalServerError, "fetch failed", nil)
		return
	}
	tags, err := s.d.Store.ListTags(ctx)
	if err != nil {
		writeErr(w, http.StatusInternalServerError, "fetch failed", nil)
		return
	}
	channels, err := s.d.Store.ListChannels(ctx)
	if err != nil {
		writeErr(w, http.StatusInternalServerError, "fetch failed", nil)
		return
	}

	groupByID := make(map[int64]string, len(groups))
	for _, g := range groups {
		groupByID[g.ID] = g.Name
	}
	tagByID := make(map[int64]string, len(tags))
	for _, t := range tags {
		tagByID[t.ID] = t.Name
	}
	chanByID := make(map[int64]string, len(channels))
	for _, c := range channels {
		chanByID[c.ID] = c.Name
	}

	rows := make([]exportRow, 0, len(monitors))
	for _, m := range monitors {
		rows = append(rows, monitorToExportRow(m, groupByID, tagByID, chanByID))
	}

	if format == "csv" {
		w.Header().Set("Content-Type", "text/csv; charset=utf-8")
		w.Header().Set("Content-Disposition", `attachment; filename="monitors-export.csv"`)
		cw := csv.NewWriter(w)
		_ = cw.Write(csvHeader)
		for _, row := range rows {
			_ = cw.Write(rowToCSVRecord(row))
		}
		cw.Flush()
		return
	}

	w.Header().Set("Content-Type", "application/json")
	w.Header().Set("Content-Disposition", `attachment; filename="monitors-export.json"`)
	_ = json.NewEncoder(w).Encode(map[string]any{"monitors": rows})
}

func (s *server) handleExportTemplateCSV(w http.ResponseWriter, r *http.Request) {
	w.Header().Set("Content-Type", "text/csv; charset=utf-8")
	w.Header().Set("Content-Disposition", `attachment; filename="monitors-template.csv"`)

	cw := csv.NewWriter(w)
	_ = cw.Write(csvHeader)

	maxRedirects := 10
	httpSettings, _ := json.Marshal(model.Settings{
		Method: "GET", BodyType: "none", AuthType: "none",
		AcceptedCodes: "200-299", MaxRedirects: &maxRedirects,
	})
	_ = cw.Write([]string{
		"Example HTTP Monitor", "http", "https://example.com/health", "Production", "web,critical", "",
		"60", "20", "2", "10000", "0", "14", "false", "0", "false",
		"Checks the health endpoint", "", string(httpSettings),
	})

	tcpSettings, _ := json.Marshal(model.Settings{})
	_ = cw.Write([]string{
		"Example TCP Monitor", "tcp", "db.example.com:5432", "Production", "database", "",
		"60", "20", "2", "10000", "0", "14", "false", "0", "false",
		"", "", string(tcpSettings),
	})

	cw.Flush()
}
