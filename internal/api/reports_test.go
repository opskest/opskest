package api

import (
	"context"
	"encoding/csv"
	"net/http"
	"strings"
	"testing"
	"time"
)

// ageMonitor backdates a monitor's creation (store + engine), so a report
// for the current month counts it as existing all month.
func ageMonitor(t *testing.T, env *testEnv, id int64, at time.Time) {
	t.Helper()
	ctx := context.Background()
	if _, err := env.store.W.ExecContext(ctx, `UPDATE monitors SET created_at=? WHERE id=?`, at.UnixMilli(), id); err != nil {
		t.Fatal(err)
	}
	m, err := env.store.GetMonitor(ctx, id)
	if err != nil {
		t.Fatal(err)
	}
	env.engine.UpsertMonitor(m)
}

func addOutage(t *testing.T, env *testEnv, id, monitorID int64, start, end time.Time) {
	t.Helper()
	var endMs any
	if !end.IsZero() {
		endMs = end.UnixMilli()
	}
	if _, err := env.store.W.ExecContext(context.Background(),
		`INSERT INTO outages(id, monitor_id, started_at, ended_at) VALUES (?,?,?,?)`, id, monitorID, start.UnixMilli(), endMs); err != nil {
		t.Fatal(err)
	}
}

type slaResp struct {
	Month       string  `json:"month"`
	MonthToDate bool    `json:"monthToDate"`
	TargetPct   float64 `json:"targetPct"`
	Total       int     `json:"total"`
	Summary     struct {
		Monitors int     `json:"monitors"`
		Met      int     `json:"met"`
		Breached int     `json:"breached"`
		Outages  int     `json:"outages"`
		Downtime float64 `json:"downtimeMin"`
		WorstID  *int64  `json:"worstId"`
	} `json:"summary"`
	Rows []struct {
		ID           int64    `json:"id"`
		Availability *float64 `json:"availabilityPct"`
		DowntimeMin  float64  `json:"downtimeMin"`
		Outages      int      `json:"outages"`
		LongestMin   float64  `json:"longestMin"`
		MttrMin      *float64 `json:"mttrMin"`
		Met          *bool    `json:"met"`
		BudgetMin    float64  `json:"budgetMin"`
		BudgetUsed   float64  `json:"budgetUsedPct"`
	} `json:"rows"`
}

func TestSLAReportClipsAndBudgets(t *testing.T) {
	env := newTestEnv(t)
	loc := env.store.Loc
	now := time.Now().In(loc)
	monthStart := time.Date(now.Year(), now.Month(), 1, 0, 0, 0, 0, loc)
	if now.Sub(monthStart) < 3*time.Hour {
		t.Skip("too early in the month for the fixture")
	}
	month := monthStart.Format("2006-01")

	flaky := env.createMonitor(httpMonitorIn("flaky", "https://example.com/f"))
	solid := env.createMonitor(httpMonitorIn("solid", "https://example.com/s"))
	ageMonitor(t, env, flaky.ID, monthStart.AddDate(0, -1, 0))
	ageMonitor(t, env, solid.ID, monthStart.AddDate(0, -1, 0))

	// 30 min straddling the month start: only 10 count. Then a 60-min one.
	addOutage(t, env, 9001, flaky.ID, monthStart.Add(-20*time.Minute), monthStart.Add(10*time.Minute))
	addOutage(t, env, 9002, flaky.ID, monthStart.Add(time.Hour), monthStart.Add(2*time.Hour))
	// Last month entirely: ignored.
	addOutage(t, env, 9003, solid.ID, monthStart.Add(-48*time.Hour), monthStart.Add(-47*time.Hour))

	r := env.do(http.MethodGet, "/api/reports/sla?month="+month+"&scope=all&target=99.9", nil, "viewer")
	if r.status != http.StatusOK {
		t.Fatalf("status %d body %s", r.status, r.body)
	}
	var got slaResp
	r.json(t, &got)
	if !got.MonthToDate || got.Total != 2 || got.Summary.Outages != 2 || got.Summary.Downtime != 70 {
		t.Fatalf("summary = %+v total=%d mtd=%v", got.Summary, got.Total, got.MonthToDate)
	}
	f := got.Rows[0] // worst first
	if f.ID != flaky.ID || f.DowntimeMin != 70 || f.Outages != 2 || f.LongestMin != 60 {
		t.Fatalf("flaky row = %+v", f)
	}
	if f.MttrMin == nil || *f.MttrMin != 45 { // (30 + 60) / 2, unclipped repair times
		t.Errorf("mttr = %v, want 45", f.MttrMin)
	}
	days := monthStart.AddDate(0, 1, 0).Sub(monthStart).Minutes()
	if want := round2(0.001 * days); f.BudgetMin != want {
		t.Errorf("budget = %v, want %v", f.BudgetMin, want)
	}
	if want := round2(100 * 70 / (0.001 * days)); f.BudgetUsed != want {
		t.Errorf("budget used = %v, want %v", f.BudgetUsed, want)
	}
	s := got.Rows[1]
	if s.ID != solid.ID || s.DowntimeMin != 0 || s.Availability == nil || *s.Availability != 100 || s.Met == nil || !*s.Met {
		t.Errorf("solid row = %+v", s)
	}
	if got.Summary.WorstID == nil || *got.Summary.WorstID != flaky.ID {
		t.Errorf("worst = %v", got.Summary.WorstID)
	}

	// CSV: header + one line per monitor.
	c := env.do(http.MethodGet, "/api/reports/sla?month="+month+"&scope=all&format=csv", nil, "viewer")
	recs, err := csv.NewReader(strings.NewReader(string(c.body))).ReadAll()
	if err != nil || len(recs) != 3 || recs[0][0] != "monitor_id" || recs[1][1] != "flaky" {
		t.Fatalf("csv = %q err=%v", c.body, err)
	}
	if !strings.Contains(c.header.Get("Content-Disposition"), "sla-"+month+"-all.csv") {
		t.Errorf("disposition = %q", c.header.Get("Content-Disposition"))
	}
}

func TestSLAReportScopesAndErrors(t *testing.T) {
	env := newTestEnv(t)
	g := env.do(http.MethodPost, "/api/groups", map[string]any{"name": "Payments"}, "admin")
	var grp struct {
		ID int64 `json:"id"`
	}
	g.json(t, &grp)
	in := httpMonitorIn("in group", "https://example.com/g")
	in.GroupID = &grp.ID
	member := env.createMonitor(in)
	other := env.createMonitor(httpMonitorIn("outside", "https://example.com/o"))
	page := env.do(http.MethodPost, "/api/pages", map[string]any{
		"slug": "slap", "title": "P", "visibility": "public", "layout": "list",
		"items": []map[string]any{{"monitorId": other.ID, "sort": 0}},
	}, "admin")
	var p struct {
		ID int64 `json:"id"`
	}
	page.json(t, &p)

	check := func(q string, wantIDs ...int64) {
		t.Helper()
		r := env.do(http.MethodGet, "/api/reports/sla?"+q, nil, "viewer")
		if r.status != http.StatusOK {
			t.Fatalf("%s: %d %s", q, r.status, r.body)
		}
		var got slaResp
		r.json(t, &got)
		if len(got.Rows) != len(wantIDs) {
			t.Fatalf("%s: %d rows, want %d", q, len(got.Rows), len(wantIDs))
		}
		for i, id := range wantIDs {
			if got.Rows[i].ID != id {
				t.Errorf("%s: row %d = %d, want %d", q, i, got.Rows[i].ID, id)
			}
		}
	}
	check("scope=group&id="+itoa(grp.ID), member.ID)
	check("scope=page&id="+itoa(p.ID), other.ID)
	check("scope=monitor&id="+itoa(member.ID), member.ID)

	for q, want := range map[string]int{
		"month=2026-13":           http.StatusBadRequest,
		"month=2019-01":           http.StatusBadRequest,
		"month=2099-01":           http.StatusBadRequest,
		"scope=team":              http.StatusBadRequest,
		"target=100":              http.StatusBadRequest,
		"scope=monitor&id=999999": http.StatusNotFound,
		"scope=group&id=999999":   http.StatusNotFound,
		"scope=page&id=999999":    http.StatusNotFound,
	} {
		if r := env.do(http.MethodGet, "/api/reports/sla?"+q, nil, "viewer"); r.status != want {
			t.Errorf("%s: %d, want %d", q, r.status, want)
		}
	}
	if r := env.do(http.MethodGet, "/api/reports/sla", nil, ""); r.status == http.StatusOK {
		t.Errorf("anonymous report allowed")
	}
}
