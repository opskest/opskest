package api

import (
	"bytes"
	"context"
	"encoding/csv"
	"encoding/json"
	"errors"
	"fmt"
	"io"
	"net/http"
	"net/url"
	"strconv"
	"strings"

	"opskest/internal/auth"
	"opskest/internal/logx"
	"opskest/internal/model"
	"opskest/internal/store"
)

// maxImportBodyBytes and maxImportRows bound one import request: large
// enough for any real bulk import, small enough that a malformed or
// hostile body can't tie up the request goroutine building thousands of
// monitors that will just be rejected anyway.
const (
	maxImportBodyBytes = 5 << 20
	maxImportRows      = 5000
)

// rowInput is the normalized shape every import source (JSON array, CSV,
// quick URL bulk-add) is parsed into before validation and store writes.
type rowInput struct {
	Name           string
	Type           string
	Target         string
	GroupName      string
	TagNames       []string
	ChannelNames   []string
	IntervalS      int
	RetryIntervalS int
	Retries        int
	TimeoutMs      int
	DegradedMs     int
	SSLWarnDays    int
	UpsideDown     bool
	ReminderMin    int
	Paused         bool
	Description    string
	Runbook        string
	Settings       model.Settings
	HasSettings    bool
}

type importRowResult struct {
	Index    int               `json:"index"`
	Name     string            `json:"name"`
	Type     string            `json:"type"`
	Target   string            `json:"target"`
	Action   string            `json:"action"` // create|skip|error
	Errors   model.FieldErrors `json:"errors,omitempty"`
	Warnings []string          `json:"warnings,omitempty"`
}

type importSummary struct {
	Create int `json:"create"`
	Skip   int `json:"skip"`
	Error  int `json:"error"`
}

// importCtx caches the name -> id lookups an import needs (groups, tags,
// channels) and the dedup keys already claimed, so a batch of thousands
// of rows costs a handful of queries rather than one per row.
type importCtx struct {
	ctx        context.Context
	s          *server
	dryRun     bool
	groupCache map[string]int64 // lowercased name -> id
	tagCache   map[string]int64
	chanByName map[string]int64 // never auto-created; unknown -> warning
	seenKey    map[string]bool  // "type|target" already claimed (existing db + earlier rows this batch)
	seenName   map[string]bool  // lowercased name already claimed
}

func newImportCtx(ctx context.Context, s *server, dryRun bool) (*importCtx, error) {
	groups, err := s.d.Store.ListGroups(ctx)
	if err != nil {
		return nil, err
	}
	tags, err := s.d.Store.ListTags(ctx)
	if err != nil {
		return nil, err
	}
	channels, err := s.d.Store.ListChannels(ctx)
	if err != nil {
		return nil, err
	}
	monitors, err := s.d.Store.ListMonitors(ctx)
	if err != nil {
		return nil, err
	}

	ic := &importCtx{
		ctx: ctx, s: s, dryRun: dryRun,
		groupCache: map[string]int64{}, tagCache: map[string]int64{}, chanByName: map[string]int64{},
		seenKey: map[string]bool{}, seenName: map[string]bool{},
	}
	for _, g := range groups {
		ic.groupCache[strings.ToLower(g.Name)] = g.ID
	}
	for _, t := range tags {
		ic.tagCache[strings.ToLower(t.Name)] = t.ID
	}
	for _, c := range channels {
		ic.chanByName[strings.ToLower(c.Name)] = c.ID
	}
	for _, m := range monitors {
		ic.seenKey[string(m.Type)+"|"+m.Target+"|"+strings.ToLower(m.Name)] = true
		if m.Name != "" {
			ic.seenName[strings.ToLower(m.Name)] = true
		}
	}
	return ic, nil
}

// resolveGroup returns the id for name, creating the group (outside dry
// run) if it doesn't exist yet. In dry run it never writes, and instead
// notes in the warning that the group will be created.
func (ic *importCtx) resolveGroup(name string) (*int64, string) {
	name = strings.TrimSpace(name)
	if name == "" {
		return nil, ""
	}
	key := strings.ToLower(name)
	if id, ok := ic.groupCache[key]; ok {
		return &id, ""
	}
	if ic.dryRun {
		return nil, fmt.Sprintf("group %q will be created", name)
	}
	g := &store.Group{Name: name}
	if err := ic.s.d.Store.CreateGroup(ic.ctx, g); err != nil {
		if errors.Is(err, store.ErrConflict) {
			if groups, lerr := ic.s.d.Store.ListGroups(ic.ctx); lerr == nil {
				for _, gg := range groups {
					if strings.EqualFold(gg.Name, name) {
						ic.groupCache[strings.ToLower(gg.Name)] = gg.ID
						return &gg.ID, ""
					}
				}
			}
		}
		return nil, fmt.Sprintf("failed to create group %q", name)
	}
	ic.groupCache[key] = g.ID
	return &g.ID, fmt.Sprintf("created group %q", name)
}

func (ic *importCtx) resolveTag(name string) (*int64, string) {
	name = strings.TrimSpace(name)
	if name == "" {
		return nil, ""
	}
	key := strings.ToLower(name)
	if id, ok := ic.tagCache[key]; ok {
		return &id, ""
	}
	if ic.dryRun {
		return nil, fmt.Sprintf("tag %q will be created", name)
	}
	tg := &store.Tag{Name: name}
	if err := ic.s.d.Store.CreateTag(ic.ctx, tg); err != nil {
		if errors.Is(err, store.ErrConflict) {
			if tags, lerr := ic.s.d.Store.ListTags(ic.ctx); lerr == nil {
				for _, tt := range tags {
					if strings.EqualFold(tt.Name, name) {
						ic.tagCache[strings.ToLower(tt.Name)] = tt.ID
						return &tt.ID, ""
					}
				}
			}
		}
		return nil, fmt.Sprintf("failed to create tag %q", name)
	}
	ic.tagCache[key] = tg.ID
	return &tg.ID, fmt.Sprintf("created tag %q", name)
}

// process validates and resolves one row, returning the report row plus
// (only for action="create") the monitor ready to be persisted.
func (ic *importCtx) process(idx int, in rowInput) (importRowResult, *model.Monitor) {
	res := importRowResult{Index: idx, Name: strings.TrimSpace(in.Name), Type: strings.ToLower(strings.TrimSpace(in.Type)), Target: strings.TrimSpace(in.Target)}

	mtype := model.MonitorType(res.Type)
	switch {
	case mtype.Known():
	default:
		res.Action = "error"
		res.Errors = model.FieldErrors{"type": "unknown monitor type"}
		return res, nil
	}

	if mtype == model.TypeGroup {
		// A group names its children by monitor id, which means nothing in another instance.
		res.Action = "error"
		res.Errors = model.FieldErrors{"type": "group monitors can't be imported; create them in the app after the monitors they contain"}
		return res, nil
	}

	m := model.NewMonitor(mtype)
	m.Name = res.Name
	m.Target = res.Target
	m.IntervalS = in.IntervalS
	m.RetryIntervalS = in.RetryIntervalS
	m.Retries = in.Retries
	m.TimeoutMs = in.TimeoutMs
	m.DegradedMs = in.DegradedMs
	m.SSLWarnDays = in.SSLWarnDays
	m.UpsideDown = in.UpsideDown
	m.ReminderMin = in.ReminderMin
	m.Paused = in.Paused
	m.Description = in.Description
	m.Runbook = in.Runbook
	// A row with no settings checks from this server only; worldwide checks
	// send the target to a third party and are something to opt in to.
	if !in.HasSettings && (mtype == model.TypeHTTP || mtype == model.TypePing || mtype == model.TypeDNS) {
		m.Settings.RegionMode = "local"
	}
	if in.HasSettings {
		m.Settings = in.Settings
		// Parent ids name monitors in the exporting instance, not this one.
		m.Settings.DependsOn = nil
		m.Settings.Children = nil
		if m.Settings.AuthType == "oauth2" {
			// The client secret is never exported, so the grant cannot be rebuilt here.
			m.Settings.AuthType = "none"
			res.Warnings = append(res.Warnings, "OAuth2 authentication was dropped: the client secret is not exported; edit the monitor to set it again")
		}
	}

	if strings.TrimSpace(in.GroupName) != "" {
		gid, warn := ic.resolveGroup(in.GroupName)
		m.GroupID = gid
		if warn != "" {
			res.Warnings = append(res.Warnings, warn)
		}
	}
	for _, tn := range in.TagNames {
		tid, warn := ic.resolveTag(tn)
		if tid != nil {
			m.TagIDs = append(m.TagIDs, *tid)
		}
		if warn != "" {
			res.Warnings = append(res.Warnings, warn)
		}
	}
	for _, cn := range in.ChannelNames {
		cn = strings.TrimSpace(cn)
		if cn == "" {
			continue
		}
		if cid, ok := ic.chanByName[strings.ToLower(cn)]; ok {
			m.ChannelIDs = append(m.ChannelIDs, cid)
		} else {
			res.Warnings = append(res.Warnings, fmt.Sprintf("unknown channel %q, skipped", cn))
		}
	}

	// Fills any field left at its invalid zero value (IntervalS,
	// RetryIntervalS, TimeoutMs, and per-type basics like http's Method) -
	// exactly the gap a blank CSV cell or a sparse hand-written JSON row
	// leaves. Fields where zero is a legitimate explicit value (Retries,
	// SSLWarnDays, DegradedMs, ReminderMin) are untouched by design (see
	// model.Monitor.ApplyDefaults), so a real exported zero round-trips.
	m.ApplyDefaults()

	if fe := m.Validate(); len(fe) > 0 {
		res.Action = "error"
		res.Errors = fe
		return res, nil
	}

	nameKey := strings.ToLower(res.Name)
	// A duplicate is the same name, type and target: one URL may be
	// monitored under two names (e.g. in two groups) on purpose.
	dupKey := string(mtype) + "|" + res.Target + "|" + nameKey

	if ic.seenKey[dupKey] {
		res.Action = "skip"
		res.Warnings = append(res.Warnings, "duplicate: a monitor with this name, type and target already exists")
		return res, nil
	}
	if nameKey != "" && ic.seenName[nameKey] {
		res.Warnings = append(res.Warnings, "name already used by another monitor")
	}

	ic.seenKey[dupKey] = true
	if nameKey != "" {
		ic.seenName[nameKey] = true
	}

	res.Action = "create"
	return res, m
}

// ── Parsing ─────────────────────────────────────────────────────────

func parseImportBody(r *http.Request) ([]rowInput, error) {
	ct := r.Header.Get("Content-Type")
	body, err := io.ReadAll(r.Body)
	if err != nil {
		return nil, fmt.Errorf("read body: %w", err)
	}

	if strings.HasPrefix(strings.ToLower(ct), "text/csv") {
		return parseImportCSV(body)
	}

	trimmed := bytes.TrimSpace(body)
	if len(trimmed) == 0 {
		return nil, fmt.Errorf("empty body")
	}
	if trimmed[0] == '[' {
		return parseImportJSONArray(trimmed)
	}
	// opskest's own export: {"monitors":[...]}.
	var wrapped struct {
		Monitors json.RawMessage `json:"monitors"`
	}
	if json.Unmarshal(trimmed, &wrapped) == nil && len(wrapped.Monitors) > 0 && wrapped.Monitors[0] == '[' {
		return parseImportJSONArray(wrapped.Monitors)
	}

	var quick struct {
		URLs      []string `json:"urls"`
		GroupName string   `json:"groupName"`
		Type      string   `json:"type"`
		IntervalS int      `json:"intervalS"`
	}
	dec := json.NewDecoder(bytes.NewReader(trimmed))
	dec.DisallowUnknownFields()
	if err := dec.Decode(&quick); err != nil || len(quick.URLs) == 0 {
		return nil, fmt.Errorf(`invalid JSON body: expected an array of monitors or {"urls":[...]}`)
	}
	return quickURLRows(quick.URLs, quick.GroupName, quick.Type, quick.IntervalS), nil
}

func quickURLRows(urls []string, groupName, typ string, intervalS int) []rowInput {
	t := strings.ToLower(strings.TrimSpace(typ))
	if t == "" {
		t = string(model.TypeHTTP)
	}
	if intervalS == 0 {
		intervalS = 60
	}
	rows := make([]rowInput, 0, len(urls))
	for _, u := range urls {
		u = strings.TrimSpace(u)
		if u == "" {
			continue
		}
		rows = append(rows, rowInput{
			Name: deriveNameFromURL(u), Type: t, Target: u,
			GroupName: groupName, IntervalS: intervalS,
		})
	}
	return rows
}

func deriveNameFromURL(raw string) string {
	if u, err := url.Parse(raw); err == nil && u.Host != "" {
		return u.Host
	}
	return raw
}

func parseImportJSONArray(data []byte) ([]rowInput, error) {
	var raw []exportRow
	if err := json.Unmarshal(data, &raw); err != nil {
		return nil, fmt.Errorf("invalid JSON: %w", err)
	}
	rows := make([]rowInput, 0, len(raw))
	for _, er := range raw {
		rows = append(rows, rowInput{
			Name: er.Name, Type: er.Type, Target: er.Target, GroupName: er.Group,
			TagNames: er.Tags, ChannelNames: er.Channels,
			IntervalS: er.IntervalS, RetryIntervalS: er.RetryIntervalS, Retries: er.Retries,
			TimeoutMs: er.TimeoutMs, DegradedMs: er.DegradedMs, SSLWarnDays: er.SSLWarnDays,
			UpsideDown: er.UpsideDown, ReminderMin: er.ReminderMin, Paused: er.Paused,
			Description: er.Description, Runbook: er.Runbook,
			Settings: er.Settings, HasSettings: true,
		})
	}
	return rows, nil
}

func parseImportCSV(data []byte) ([]rowInput, error) {
	cr := csv.NewReader(bytes.NewReader(data))
	cr.FieldsPerRecord = -1

	header, err := cr.Read()
	if err != nil {
		return nil, fmt.Errorf("read CSV header: %w", err)
	}
	idx := make(map[string]int, len(header))
	for i, h := range header {
		idx[strings.ToLower(strings.TrimSpace(h))] = i
	}
	get := func(rec []string, key string) string {
		i, ok := idx[key]
		if !ok || i >= len(rec) {
			return ""
		}
		return rec[i]
	}
	getInt := func(rec []string, key string) int {
		v := strings.TrimSpace(get(rec, key))
		if v == "" {
			return 0
		}
		n, _ := strconv.Atoi(v)
		return n
	}
	getBool := func(rec []string, key string) bool {
		v := strings.ToLower(strings.TrimSpace(get(rec, key)))
		return v == "true" || v == "1" || v == "yes"
	}
	splitList := func(v string) []string {
		v = strings.TrimSpace(v)
		if v == "" {
			return nil
		}
		parts := strings.Split(v, ",")
		out := make([]string, 0, len(parts))
		for _, p := range parts {
			if p = strings.TrimSpace(p); p != "" {
				out = append(out, p)
			}
		}
		return out
	}

	var rows []rowInput
	for {
		rec, err := cr.Read()
		if err == io.EOF {
			break
		}
		if err != nil {
			return nil, fmt.Errorf("parse CSV: %w", err)
		}
		row := rowInput{
			Name: get(rec, "name"), Type: get(rec, "type"), Target: get(rec, "target"),
			GroupName: get(rec, "group"), TagNames: splitList(get(rec, "tags")), ChannelNames: splitList(get(rec, "channels")),
			IntervalS: getInt(rec, "interval_s"), RetryIntervalS: getInt(rec, "retry_interval_s"),
			Retries: getInt(rec, "retries"), TimeoutMs: getInt(rec, "timeout_ms"),
			DegradedMs: getInt(rec, "degraded_ms"), SSLWarnDays: getInt(rec, "ssl_warn_days"),
			UpsideDown: getBool(rec, "upside_down"), ReminderMin: getInt(rec, "reminder_min"),
			Paused: getBool(rec, "paused"), Description: get(rec, "description"), Runbook: get(rec, "runbook"),
		}
		if raw := strings.TrimSpace(get(rec, "settings")); raw != "" {
			if err := json.Unmarshal([]byte(raw), &row.Settings); err == nil {
				row.HasSettings = true
			}
		}
		rows = append(rows, row)
	}
	return rows, nil
}

// ── Handler ─────────────────────────────────────────────────────────

func (s *server) handleImportMonitors(w http.ResponseWriter, r *http.Request) {
	dryRun := r.URL.Query().Get("dryRun") == "true"
	ctx := r.Context()

	r.Body = http.MaxBytesReader(w, r.Body, maxImportBodyBytes)
	rows, err := parseImportBody(r)
	if err != nil {
		writeErr(w, http.StatusBadRequest, err.Error(), nil)
		return
	}
	if len(rows) == 0 {
		writeErr(w, http.StatusBadRequest, "no rows to import", nil)
		return
	}
	if len(rows) > maxImportRows {
		writeErr(w, http.StatusBadRequest, "at most 5000 rows per request", nil)
		return
	}

	ic, err := newImportCtx(ctx, s, dryRun)
	if err != nil {
		writeErr(w, http.StatusInternalServerError, "fetch failed", nil)
		return
	}

	preferGrouped(rows)
	results := make([]importRowResult, 0, len(rows))
	monitors := make([]*model.Monitor, 0, len(rows))
	var sum importSummary
	for i, row := range rows {
		res, m := ic.process(i, row)
		results = append(results, res)
		switch res.Action {
		case "create":
			sum.Create++
			monitors = append(monitors, m)
		case "skip":
			sum.Skip++
		case "error":
			sum.Error++
		}
	}

	if dryRun {
		writeJSON(w, http.StatusOK, map[string]any{"rows": results, "summary": sum})
		return
	}

	u, _ := auth.UserFrom(ctx)
	now := s.d.Now()
	created := 0
	for _, m := range monitors {
		m.CreatedBy = &u.ID
		m.CreatedAt = now
		m.UpdatedAt = now
		if err := s.d.Store.CreateMonitor(ctx, m); err != nil {
			logx.Err(s.d.Log, "import: create monitor failed", err)
			continue
		}
		s.d.Engine.UpsertMonitor(m)
		created++
	}
	// The import may have created groups: the engine (and so the live
	// dashboard) must learn about them, like any other group change.
	_ = s.d.Engine.ReloadGroups(ctx)
	s.d.Store.Audit(ctx, &u.ID, "import", "monitor", nil, fmt.Sprintf("created=%d skip=%d error=%d", created, sum.Skip, sum.Error))

	writeJSON(w, http.StatusOK, map[string]any{"rows": results, "summary": sum})
}

// preferGrouped reorders rows so that when a file lists the same monitor
// (name + type + target) more than once, a copy that has a group is processed
// first; the usual duplicate check then skips the ungrouped copies.
func preferGrouped(rows []rowInput) {
	first := map[string]int{}
	for i := range rows {
		key := strings.ToLower(strings.TrimSpace(rows[i].Type)) + "|" + strings.TrimSpace(rows[i].Target) + "|" + strings.ToLower(strings.TrimSpace(rows[i].Name))
		j, seen := first[key]
		if !seen {
			first[key] = i
			continue
		}
		if strings.TrimSpace(rows[j].GroupName) == "" && strings.TrimSpace(rows[i].GroupName) != "" {
			rows[i], rows[j] = rows[j], rows[i]
		}
	}
}
