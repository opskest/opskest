package api

import (
	"encoding/json"
	"net/http"
	"strings"
	"testing"

	"opskest/internal/model"
)

func TestExportJSONRoundTrip(t *testing.T) {
	env := newTestEnv(t)

	groupResp := env.do(http.MethodPost, "/api/groups", map[string]any{"name": "Production"}, "admin")
	if groupResp.status != http.StatusCreated {
		t.Fatalf("create group: status = %d, body = %s", groupResp.status, groupResp.body)
	}
	var group struct {
		ID int64 `json:"id"`
	}
	groupResp.json(t, &group)

	in := httpMonitorIn("web-1", "https://example.invalid/health")
	in.GroupID = &group.ID
	in.Description = "Primary web health check"
	in.Secrets.BearerToken = strPtr("super-secret-token")
	in.Headers = []HeaderIn{
		{Name: "X-Public", Value: strPtr("hello"), Secret: false},
		{Name: "Authorization", Value: strPtr("Bearer abc123"), Secret: true},
	}
	env.createMonitor(in)

	exportResp := env.do(http.MethodGet, "/api/export/monitors?format=json", nil, "admin")
	if exportResp.status != http.StatusOK {
		t.Fatalf("export status = %d, body = %s", exportResp.status, exportResp.body)
	}
	if strings.Contains(string(exportResp.body), "super-secret-token") || strings.Contains(string(exportResp.body), "Bearer abc123") {
		t.Fatalf("export body leaked a secret: %s", exportResp.body)
	}
	var exported struct {
		Monitors []exportRow `json:"monitors"`
	}
	exportResp.json(t, &exported)
	if len(exported.Monitors) != 1 {
		t.Fatalf("exported monitors = %d, want 1", len(exported.Monitors))
	}
	row := exported.Monitors[0]
	if row.Group != "Production" {
		t.Fatalf("row.Group = %q, want Production", row.Group)
	}
	if len(row.Settings.Headers) != 2 {
		t.Fatalf("row.Settings.Headers = %+v, want 2", row.Settings.Headers)
	}
	for _, h := range row.Settings.Headers {
		if h.Secret && h.Value != "" {
			t.Fatalf("secret header %q exported a value", h.Name)
		}
	}

	// Import the exported rows into a *fresh* store and confirm it
	// reproduces the monitor (minus secrets, which import can't know).
	env2 := newTestEnv(t)
	body, err := marshalJSON(exported.Monitors)
	if err != nil {
		t.Fatalf("marshal: %v", err)
	}
	importResp := env2.doRaw(http.MethodPost, "/api/import/monitors?dryRun=false", "application/json", body, "admin")
	if importResp.status != http.StatusOK {
		t.Fatalf("import status = %d, body = %s", importResp.status, importResp.body)
	}
	var importOut struct {
		Summary importSummary `json:"summary"`
	}
	importResp.json(t, &importOut)
	if importOut.Summary.Create != 1 || importOut.Summary.Error != 0 || importOut.Summary.Skip != 0 {
		t.Fatalf("summary = %+v, want 1 create", importOut.Summary)
	}

	listResp := env2.do(http.MethodGet, "/api/monitors", nil, "admin")
	var list struct {
		Monitors []map[string]any `json:"monitors"`
	}
	listResp.json(t, &list)
	if len(list.Monitors) != 1 {
		t.Fatalf("monitors after import = %d, want 1", len(list.Monitors))
	}
}

func TestImportCSVWithQuotedCommas(t *testing.T) {
	env := newTestEnv(t)

	csvBody := "name,type,target,group,tags,channels,interval_s,retry_interval_s,retries,timeout_ms,degraded_ms,ssl_warn_days,upside_down,reminder_min,paused,description,runbook,settings\n" +
		`"Site, with a comma",http,https://example.invalid/a,,web,,60,20,2,10000,0,14,false,0,false,"desc, with comma",,"{""method"":""GET"",""bodyType"":""none"",""authType"":""none"",""acceptedCodes"":""200-299""}"` + "\n"

	resp := env.doRaw(http.MethodPost, "/api/import/monitors?dryRun=true", "text/csv", []byte(csvBody), "admin")
	if resp.status != http.StatusOK {
		t.Fatalf("status = %d, body = %s", resp.status, resp.body)
	}
	var out struct {
		Rows    []importRowResult `json:"rows"`
		Summary importSummary     `json:"summary"`
	}
	resp.json(t, &out)
	if len(out.Rows) != 1 {
		t.Fatalf("rows = %d, want 1", len(out.Rows))
	}
	if out.Rows[0].Name != "Site, with a comma" {
		t.Fatalf("row name = %q, want the comma preserved", out.Rows[0].Name)
	}
	if out.Rows[0].Action != "create" {
		t.Fatalf("row action = %q, errors = %v, want create", out.Rows[0].Action, out.Rows[0].Errors)
	}
	if out.Summary.Create != 1 {
		t.Fatalf("summary = %+v", out.Summary)
	}
}

func TestImportBadRowsReportedNotFatal(t *testing.T) {
	env := newTestEnv(t)

	rows := []exportRow{
		{Name: "good", Type: "http", Target: "https://example.invalid/ok", IntervalS: 60, RetryIntervalS: 20, Retries: 2, TimeoutMs: 10000, SSLWarnDays: 14, Settings: model.Settings{Method: "GET", BodyType: "none", AuthType: "none", AcceptedCodes: "200-299"}},
		{Name: "", Type: "http", Target: "not a url", IntervalS: 60, RetryIntervalS: 20, TimeoutMs: 10000},
		{Name: "bad-type", Type: "carrier-pigeon", Target: "x"},
	}
	body, err := marshalJSON(rows)
	if err != nil {
		t.Fatalf("marshal: %v", err)
	}
	resp := env.doRaw(http.MethodPost, "/api/import/monitors?dryRun=true", "application/json", body, "admin")
	if resp.status != http.StatusOK {
		t.Fatalf("status = %d, body = %s", resp.status, resp.body)
	}
	var out struct {
		Rows    []importRowResult `json:"rows"`
		Summary importSummary     `json:"summary"`
	}
	resp.json(t, &out)
	if len(out.Rows) != 3 {
		t.Fatalf("rows = %d, want 3", len(out.Rows))
	}
	if out.Rows[0].Action != "create" {
		t.Fatalf("row 0 action = %q, want create", out.Rows[0].Action)
	}
	if out.Rows[1].Action != "error" || out.Rows[2].Action != "error" {
		t.Fatalf("rows 1,2 actions = %q, %q, want error, error", out.Rows[1].Action, out.Rows[2].Action)
	}
	if out.Summary.Create != 1 || out.Summary.Error != 2 {
		t.Fatalf("summary = %+v, want 1 create, 2 error", out.Summary)
	}
}

func TestImportDuplicatesSkipped(t *testing.T) {
	env := newTestEnv(t)
	env.createMonitor(httpMonitorIn("existing", "https://example.invalid/dup"))

	rows := []exportRow{
		{Name: "existing", Type: "http", Target: "https://example.invalid/dup", IntervalS: 60, RetryIntervalS: 20, Retries: 2, TimeoutMs: 10000, SSLWarnDays: 14, Settings: model.Settings{Method: "GET", BodyType: "none", AuthType: "none", AcceptedCodes: "200-299"}},
	}
	body, err := marshalJSON(rows)
	if err != nil {
		t.Fatalf("marshal: %v", err)
	}
	resp := env.doRaw(http.MethodPost, "/api/import/monitors?dryRun=true", "application/json", body, "admin")
	if resp.status != http.StatusOK {
		t.Fatalf("status = %d, body = %s", resp.status, resp.body)
	}
	var out struct {
		Rows    []importRowResult `json:"rows"`
		Summary importSummary     `json:"summary"`
	}
	resp.json(t, &out)
	if out.Rows[0].Action != "skip" {
		t.Fatalf("action = %q, want skip (duplicate name+type+target)", out.Rows[0].Action)
	}
	if out.Summary.Skip != 1 {
		t.Fatalf("summary = %+v, want 1 skip", out.Summary)
	}
}

func TestImportQuickURLBulkAdd(t *testing.T) {
	env := newTestEnv(t)

	body := []byte(`{"urls":["https://a.example.invalid/","https://b.example.invalid/"],"type":"http","intervalS":45}`)
	resp := env.doRaw(http.MethodPost, "/api/import/monitors?dryRun=false", "application/json", body, "admin")
	if resp.status != http.StatusOK {
		t.Fatalf("status = %d, body = %s", resp.status, resp.body)
	}
	var out struct {
		Rows    []importRowResult `json:"rows"`
		Summary importSummary     `json:"summary"`
	}
	resp.json(t, &out)
	if out.Summary.Create != 2 {
		t.Fatalf("summary = %+v, want 2 create", out.Summary)
	}

	listResp := env.do(http.MethodGet, "/api/monitors", nil, "admin")
	var list struct {
		Monitors []map[string]any `json:"monitors"`
	}
	listResp.json(t, &list)
	if len(list.Monitors) != 2 {
		t.Fatalf("monitors after quick add = %d, want 2", len(list.Monitors))
	}
}

func TestExportTemplateCSV(t *testing.T) {
	env := newTestEnv(t)
	resp := env.do(http.MethodGet, "/api/export/template.csv", nil, "admin")
	if resp.status != http.StatusOK {
		t.Fatalf("status = %d", resp.status)
	}
	lines := strings.Split(strings.TrimSpace(string(resp.body)), "\n")
	if len(lines) != 3 {
		t.Fatalf("template lines = %d, want header + 2 examples", len(lines))
	}
	if !strings.HasPrefix(lines[0], "name,type,target") {
		t.Fatalf("header = %q", lines[0])
	}
}

func TestExportRequiresAdmin(t *testing.T) {
	env := newTestEnv(t)
	if resp := env.do(http.MethodGet, "/api/export/monitors", nil, "viewer"); resp.status != http.StatusForbidden {
		t.Fatalf("viewer export status = %d, want 403", resp.status)
	}
	if resp := env.doRaw(http.MethodPost, "/api/import/monitors?dryRun=true", "application/json", []byte(`[]`), "viewer"); resp.status != http.StatusForbidden {
		t.Fatalf("viewer import status = %d, want 403", resp.status)
	}
}

func marshalJSON(v any) ([]byte, error) {
	return json.Marshal(v)
}

// TestImportAcceptsOwnExport: the {"monitors":[...]} file GET /api/export
// produces imports as-is, and when a monitor appears twice the copy with
// a group wins.
func TestImportAcceptsOwnExport(t *testing.T) {
	env := newTestEnv(t)
	body := []byte(`{"monitors":[
		{"name":"GitHub Status","type":"http","target":"https://www.githubstatus.com/api/v2/status.json","intervalS":30,"settings":{"vendor":true}},
		{"name":"GitHub Status","type":"http","target":"https://www.githubstatus.com/api/v2/status.json","group":"3rd Party Services","intervalS":30,"settings":{"vendor":true}},
		{"name":"Site","type":"http","target":"https://example.com/","group":"Websites","intervalS":60}
	]}`)
	resp := env.doRaw(http.MethodPost, "/api/import/monitors", "application/json", body, "admin")
	if resp.status != http.StatusOK {
		t.Fatalf("import = %d %s", resp.status, resp.body)
	}
	list, _ := env.store.ListMonitors(t.Context())
	if len(list) != 2 {
		t.Fatalf("created %d monitors, want 2", len(list))
	}
	groups, _ := env.store.ListGroups(t.Context())
	byID := map[int64]string{}
	for _, g := range groups {
		byID[g.ID] = g.Name
	}
	for _, m := range list {
		if m.Name == "GitHub Status" && (m.GroupID == nil || byID[*m.GroupID] != "3rd Party Services" || !m.Settings.Vendor) {
			t.Fatalf("github monitor = %+v", m)
		}
	}
}

// TestImportedGroupsReachTheEngine: groups created by an import show up
// in the live data (engine), not only in the database.
func TestImportedGroupsReachTheEngine(t *testing.T) {
	env := newTestEnv(t)
	body := []byte(`{"monitors":[{"name":"Site","type":"http","target":"https://example.com/","group":"Websites","intervalS":60}]}`)
	if r := env.doRaw(http.MethodPost, "/api/import/monitors", "application/json", body, "admin"); r.status != http.StatusOK {
		t.Fatalf("import = %d %s", r.status, r.body)
	}
	found := false
	for _, g := range env.engine.Groups() {
		if g.Name == "Websites" {
			found = true
		}
	}
	if !found {
		t.Fatalf("engine groups = %+v, want Websites", env.engine.Groups())
	}
}

// TestExportImportAlertSettings: a response-time alert survives an
// export→import round trip; parent ids don't (they name monitors in the
// exporting instance), so the imported monitor has no dependencies.
func TestExportImportAlertSettings(t *testing.T) {
	env := newTestEnv(t)
	parent := env.createMonitor(httpMonitorIn("gateway", "https://example.com/gw"))
	in := httpMonitorIn("checkout", "https://example.com/co")
	in.Settings.LatencyAlert = &model.LatencyAlert{Percentile: 99, ThresholdMs: 1200, Factor: 2.5, WindowMin: 20}
	in.Settings.DependsOn = []int64{parent.ID}
	env.createMonitor(in)

	exp := env.do(http.MethodGet, "/api/export/monitors?format=json", nil, "admin")
	var exported struct {
		Monitors []exportRow `json:"monitors"`
	}
	exp.json(t, &exported)

	env2 := newTestEnv(t)
	body, err := marshalJSON(exported.Monitors)
	if err != nil {
		t.Fatal(err)
	}
	if r := env2.doRaw(http.MethodPost, "/api/import/monitors?dryRun=false", "application/json", body, "admin"); r.status != http.StatusOK {
		t.Fatalf("import: %d %s", r.status, r.body)
	}
	var got *model.Monitor
	for _, m := range env2.engine.Monitors() {
		if m.Name == "checkout" {
			got = m
		}
	}
	if got == nil {
		t.Fatal("checkout not imported")
	}
	la := got.Settings.LatencyAlert
	if la == nil || la.Percentile != 99 || la.ThresholdMs != 1200 || la.Factor != 2.5 || la.WindowMin != 20 {
		t.Errorf("latency alert after import = %+v", la)
	}
	if len(got.Settings.DependsOn) != 0 {
		t.Errorf("imported monitor kept foreign parent ids %v", got.Settings.DependsOn)
	}
}
