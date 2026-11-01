package store

import (
	"context"
	"errors"
	"testing"
	"time"
)

func TestIncidentLifecycle(t *testing.T) {
	s := newTestStore(t)
	ctx := context.Background()

	m1 := createTestMonitor(t, s, "incident-mon-1")
	m2 := createTestMonitor(t, s, "incident-mon-2")

	page := &Page{Slug: "main", Title: "Main status", Visibility: "public", Layout: "list"}
	if err := s.CreatePage(ctx, page); err != nil {
		t.Fatalf("CreatePage: %v", err)
	}

	alice, err := s.CreateUser(ctx, "alice@example.com", "Alice", RoleViewer, time.Now())
	if err != nil {
		t.Fatalf("CreateUser(alice): %v", err)
	}
	bob, err := s.CreateUser(ctx, "bob@example.com", "", RoleViewer, time.Now())
	if err != nil {
		t.Fatalf("CreateUser(bob): %v", err)
	}

	inc := &Incident{
		Title:      "API errors",
		Status:     "investigating",
		Impact:     "major",
		CreatedBy:  &alice.ID,
		MonitorIDs: []int64{m1.ID, m2.ID},
		PageIDs:    []int64{page.ID},
	}
	if err := s.CreateIncident(ctx, inc, "We are investigating reports of API errors."); err != nil {
		t.Fatalf("CreateIncident: %v", err)
	}
	if inc.ID == 0 {
		t.Fatal("CreateIncident: ID not set")
	}
	if inc.CreatedAt == 0 || inc.UpdatedAt == 0 {
		t.Fatal("CreateIncident: timestamps not set")
	}
	if inc.ResolvedAt != nil {
		t.Fatalf("ResolvedAt = %v, want nil", inc.ResolvedAt)
	}

	got, err := s.GetIncident(ctx, inc.ID)
	if err != nil {
		t.Fatalf("GetIncident: %v", err)
	}
	sortInt64s(got.MonitorIDs)
	wantMonitors := []int64{m1.ID, m2.ID}
	sortInt64s(wantMonitors)
	if len(got.MonitorIDs) != 2 || got.MonitorIDs[0] != wantMonitors[0] || got.MonitorIDs[1] != wantMonitors[1] {
		t.Fatalf("MonitorIDs = %v, want %v", got.MonitorIDs, wantMonitors)
	}
	if len(got.PageIDs) != 1 || got.PageIDs[0] != page.ID {
		t.Fatalf("PageIDs = %v, want [%d]", got.PageIDs, page.ID)
	}
	if len(got.Updates) != 1 {
		t.Fatalf("Updates = %d entries, want 1", len(got.Updates))
	}
	if got.Updates[0].Status != "investigating" {
		t.Fatalf("first update status = %q, want investigating", got.Updates[0].Status)
	}
	if got.Updates[0].CreatedByName != "Alice" {
		t.Fatalf("first update CreatedByName = %q, want Alice", got.Updates[0].CreatedByName)
	}

	// Add an "identified" update.
	u1 := &IncidentUpdate{IncidentID: inc.ID, Status: "identified", Body: "Root cause found.", CreatedBy: &bob.ID}
	if err := s.AddIncidentUpdate(ctx, u1); err != nil {
		t.Fatalf("AddIncidentUpdate(identified): %v", err)
	}
	if u1.ID == 0 || u1.CreatedAt == 0 {
		t.Fatal("AddIncidentUpdate: ID/CreatedAt not set")
	}

	got, err = s.GetIncident(ctx, inc.ID)
	if err != nil {
		t.Fatalf("GetIncident: %v", err)
	}
	if got.Status != "identified" {
		t.Fatalf("Status = %q, want identified", got.Status)
	}
	if got.ResolvedAt != nil {
		t.Fatalf("ResolvedAt = %v, want nil before resolution", got.ResolvedAt)
	}
	if len(got.Updates) != 2 {
		t.Fatalf("Updates = %d entries, want 2", len(got.Updates))
	}
	if got.Updates[1].CreatedByName != "bob@example.com" {
		t.Fatalf("second update CreatedByName = %q, want fallback to email", got.Updates[1].CreatedByName)
	}

	// Add a "resolved" update.
	u2 := &IncidentUpdate{IncidentID: inc.ID, Status: "resolved", Body: "Fixed.", CreatedBy: &alice.ID}
	if err := s.AddIncidentUpdate(ctx, u2); err != nil {
		t.Fatalf("AddIncidentUpdate(resolved): %v", err)
	}

	got, err = s.GetIncident(ctx, inc.ID)
	if err != nil {
		t.Fatalf("GetIncident: %v", err)
	}
	if got.Status != "resolved" {
		t.Fatalf("Status = %q, want resolved", got.Status)
	}
	if got.ResolvedAt == nil {
		t.Fatal("ResolvedAt = nil, want set after resolution")
	}
	if len(got.Updates) != 3 {
		t.Fatalf("Updates = %d entries, want 3", len(got.Updates))
	}

	resolvedAt := *got.ResolvedAt
	fourteenDaysMs := int64(14) * 24 * 3600 * 1000

	// Included: active incidents are always included, and this resolved
	// incident is included when since <= resolved_at.
	forPage, err := s.IncidentsForPage(ctx, page.ID, resolvedAt-fourteenDaysMs)
	if err != nil {
		t.Fatalf("IncidentsForPage(included): %v", err)
	}
	if !containsIncident(forPage, inc.ID) {
		t.Fatalf("IncidentsForPage(since=resolvedAt-14d) missing incident %d: %+v", inc.ID, forPage)
	}

	// Excluded: since is after resolved_at, and there is no other active
	// incident on this page.
	forPage, err = s.IncidentsForPage(ctx, page.ID, resolvedAt+1)
	if err != nil {
		t.Fatalf("IncidentsForPage(excluded): %v", err)
	}
	if containsIncident(forPage, inc.ID) {
		t.Fatalf("IncidentsForPage(since=resolvedAt+1) should exclude resolved incident %d: %+v", inc.ID, forPage)
	}

	// A still-active incident on the same page is always included,
	// regardless of since.
	active := &Incident{
		Title:      "Ongoing degradation",
		Status:     "monitoring",
		Impact:     "minor",
		PageIDs:    []int64{page.ID},
		MonitorIDs: []int64{m1.ID},
	}
	if err := s.CreateIncident(ctx, active, "Keeping an eye on latency."); err != nil {
		t.Fatalf("CreateIncident(active): %v", err)
	}
	forPage, err = s.IncidentsForPage(ctx, page.ID, resolvedAt+1)
	if err != nil {
		t.Fatalf("IncidentsForPage(active always included): %v", err)
	}
	if !containsIncident(forPage, active.ID) {
		t.Fatalf("IncidentsForPage should always include active incident %d: %+v", active.ID, forPage)
	}
	if containsIncident(forPage, inc.ID) {
		t.Fatalf("IncidentsForPage should still exclude resolved incident %d: %+v", inc.ID, forPage)
	}

	if err := s.DeleteIncident(ctx, inc.ID); err != nil {
		t.Fatalf("DeleteIncident: %v", err)
	}
	if _, err := s.GetIncident(ctx, inc.ID); !errors.Is(err, ErrNotFound) {
		t.Fatalf("GetIncident after delete: err = %v, want ErrNotFound", err)
	}
}

func containsIncident(incidents []Incident, id int64) bool {
	for _, inc := range incidents {
		if inc.ID == id {
			return true
		}
	}
	return false
}

func TestMaintenanceTargets(t *testing.T) {
	s := newTestStore(t)
	ctx := context.Background()

	mon := createTestMonitor(t, s, "maint-mon")
	group := &Group{Name: "maint-group"}
	if err := s.CreateGroup(ctx, group); err != nil {
		t.Fatalf("CreateGroup: %v", err)
	}

	m := &Maintenance{
		Title:       "Weekly window",
		Kind:        "weekly",
		Weekdays:    0b1000001, // Mon + Sun
		StartMinute: 120,
		DurationMin: 60,
		TZ:          "Asia/Kolkata",
		Active:      true,
		MonitorIDs:  []int64{mon.ID},
		GroupIDs:    []int64{group.ID},
	}
	if err := s.CreateMaintenance(ctx, m); err != nil {
		t.Fatalf("CreateMaintenance: %v", err)
	}
	if m.ID == 0 {
		t.Fatal("CreateMaintenance: ID not set")
	}

	list, err := s.ListMaintenance(ctx)
	if err != nil {
		t.Fatalf("ListMaintenance: %v", err)
	}
	if len(list) != 1 {
		t.Fatalf("ListMaintenance = %d entries, want 1", len(list))
	}
	got := list[0]
	if len(got.MonitorIDs) != 1 || got.MonitorIDs[0] != mon.ID {
		t.Fatalf("MonitorIDs = %v, want [%d]", got.MonitorIDs, mon.ID)
	}
	if len(got.GroupIDs) != 1 || got.GroupIDs[0] != group.ID {
		t.Fatalf("GroupIDs = %v, want [%d]", got.GroupIDs, group.ID)
	}

	// Deleting the group cascades to remove its maintenance_targets row,
	// leaving the monitor target intact.
	if err := s.DeleteGroup(ctx, group.ID); err != nil {
		t.Fatalf("DeleteGroup: %v", err)
	}

	list, err = s.ListMaintenance(ctx)
	if err != nil {
		t.Fatalf("ListMaintenance after group delete: %v", err)
	}
	if len(list) != 1 {
		t.Fatalf("ListMaintenance after group delete = %d entries, want 1", len(list))
	}
	got = list[0]
	if len(got.GroupIDs) != 0 {
		t.Fatalf("GroupIDs after group delete = %v, want empty", got.GroupIDs)
	}
	if len(got.MonitorIDs) != 1 || got.MonitorIDs[0] != mon.ID {
		t.Fatalf("MonitorIDs after group delete = %v, want [%d]", got.MonitorIDs, mon.ID)
	}

	if err := s.DeleteMaintenance(ctx, m.ID); err != nil {
		t.Fatalf("DeleteMaintenance: %v", err)
	}
	list, err = s.ListMaintenance(ctx)
	if err != nil {
		t.Fatalf("ListMaintenance after delete: %v", err)
	}
	if len(list) != 0 {
		t.Fatalf("ListMaintenance after delete = %d entries, want 0", len(list))
	}
}

func TestPageSlugConflictAndItems(t *testing.T) {
	s := newTestStore(t)
	ctx := context.Background()

	mon1 := createTestMonitor(t, s, "page-mon-1")
	mon2 := createTestMonitor(t, s, "page-mon-2")
	group := &Group{Name: "page-group"}
	if err := s.CreateGroup(ctx, group); err != nil {
		t.Fatalf("CreateGroup: %v", err)
	}

	page := &Page{
		Slug:       "status",
		Title:      "Status",
		Visibility: "public",
		Layout:     "list",
		Items: []PageItem{
			{Sort: 0, MonitorID: &mon1.ID, DisplayName: "First"},
			{Sort: 1, GroupID: &group.ID, DisplayName: "Second"},
			{Sort: 2, MonitorID: &mon2.ID, DisplayName: "Third"},
		},
	}
	if err := s.CreatePage(ctx, page); err != nil {
		t.Fatalf("CreatePage: %v", err)
	}
	if page.ID == 0 {
		t.Fatal("CreatePage: ID not set")
	}

	dup := &Page{Slug: "status", Title: "Duplicate", Visibility: "public", Layout: "list"}
	if err := s.CreatePage(ctx, dup); !errors.Is(err, ErrConflict) {
		t.Fatalf("CreatePage(duplicate slug): err = %v, want ErrConflict", err)
	}

	got, err := s.GetPage(ctx, page.ID)
	if err != nil {
		t.Fatalf("GetPage: %v", err)
	}
	if len(got.Items) != 3 {
		t.Fatalf("Items = %d, want 3", len(got.Items))
	}
	for i, it := range got.Items {
		if it.Sort != i {
			t.Fatalf("Items[%d].Sort = %d, want %d", i, it.Sort, i)
		}
	}
	if got.Items[0].DisplayName != "First" || got.Items[2].DisplayName != "Third" {
		t.Fatalf("Items out of order: %+v", got.Items)
	}

	bySlug, err := s.GetPageBySlug(ctx, "status")
	if err != nil {
		t.Fatalf("GetPageBySlug: %v", err)
	}
	if bySlug.ID != page.ID {
		t.Fatalf("GetPageBySlug: ID = %d, want %d", bySlug.ID, page.ID)
	}

	// Updating replaces the items entirely.
	page.Items = []PageItem{
		{Sort: 0, MonitorID: &mon2.ID, DisplayName: "Only"},
	}
	page.Title = "Status Page"
	if err := s.UpdatePage(ctx, page); err != nil {
		t.Fatalf("UpdatePage: %v", err)
	}

	got, err = s.GetPage(ctx, page.ID)
	if err != nil {
		t.Fatalf("GetPage after update: %v", err)
	}
	if got.Title != "Status Page" {
		t.Fatalf("Title after update = %q, want %q", got.Title, "Status Page")
	}
	if len(got.Items) != 1 || got.Items[0].DisplayName != "Only" {
		t.Fatalf("Items after update = %+v, want single 'Only' item", got.Items)
	}

	if err := s.DeletePage(ctx, page.ID); err != nil {
		t.Fatalf("DeletePage: %v", err)
	}
	if _, err := s.GetPage(ctx, page.ID); !errors.Is(err, ErrNotFound) {
		t.Fatalf("GetPage after delete: err = %v, want ErrNotFound", err)
	}
}

func TestAlertAndAuditLog(t *testing.T) {
	s := newTestStore(t)
	ctx := context.Background()
	w := testWriter(t, s)

	mon := createTestMonitor(t, s, "log-mon")

	base := time.Now().UnixMilli()
	const n = 5
	for i := 0; i < n; i++ {
		ts := base + int64(i)*1000
		w.Do(OpAlertLog(AlertLogEntry{
			TS:        ts,
			MonitorID: &mon.ID,
			Event:     "down",
			OK:        i%2 == 0,
			Attempt:   i,
			Error:     "",
		}))
	}
	w.Flush()

	entries, err := s.ListAlertLog(ctx, 0, 0)
	if err != nil {
		t.Fatalf("ListAlertLog: %v", err)
	}
	if len(entries) != n {
		t.Fatalf("ListAlertLog = %d entries, want %d", len(entries), n)
	}
	for i := 0; i < len(entries)-1; i++ {
		if entries[i].TS < entries[i+1].TS {
			t.Fatalf("ListAlertLog not newest-first at %d: %+v", i, entries)
		}
	}

	// before paginates: it excludes entries at or after the cursor.
	before := entries[2].TS
	page, err := s.ListAlertLog(ctx, 0, before)
	if err != nil {
		t.Fatalf("ListAlertLog(before): %v", err)
	}
	for _, e := range page {
		if e.TS >= before {
			t.Fatalf("ListAlertLog(before=%d) returned ts=%d, want strictly less", before, e.TS)
		}
	}

	// limit is honored below the cap...
	small, err := s.ListAlertLog(ctx, 2, 0)
	if err != nil {
		t.Fatalf("ListAlertLog(limit=2): %v", err)
	}
	if len(small) != 2 {
		t.Fatalf("ListAlertLog(limit=2) = %d entries, want 2", len(small))
	}
	// ...and a limit above 200 is silently capped rather than erroring.
	if _, err := s.ListAlertLog(ctx, 10000, 0); err != nil {
		t.Fatalf("ListAlertLog(huge limit): %v", err)
	}

	alice, err := s.CreateUser(ctx, "auditor@example.com", "Auditor", RoleViewer, time.Now())
	if err != nil {
		t.Fatalf("UpsertUserOnLogin: %v", err)
	}
	for i := 0; i < n; i++ {
		if err := s.Audit(ctx, &alice.ID, "update", "monitor", &mon.ID, "changed interval"); err != nil {
			t.Fatalf("Audit: %v", err)
		}
	}

	audits, err := s.ListAudit(ctx, 0, 0)
	if err != nil {
		t.Fatalf("ListAudit: %v", err)
	}
	if len(audits) != n {
		t.Fatalf("ListAudit = %d entries, want %d", len(audits), n)
	}
	for i := 0; i < len(audits)-1; i++ {
		if audits[i].TS < audits[i+1].TS {
			t.Fatalf("ListAudit not newest-first at %d: %+v", i, audits)
		}
	}
	if audits[0].UserEmail != "auditor@example.com" {
		t.Fatalf("UserEmail = %q, want auditor@example.com", audits[0].UserEmail)
	}

	auditBefore := audits[2].TS
	auditPage, err := s.ListAudit(ctx, 0, auditBefore)
	if err != nil {
		t.Fatalf("ListAudit(before): %v", err)
	}
	for _, e := range auditPage {
		if e.TS >= auditBefore {
			t.Fatalf("ListAudit(before=%d) returned ts=%d, want strictly less", auditBefore, e.TS)
		}
	}

	if _, err := s.ListAudit(ctx, 10000, 0); err != nil {
		t.Fatalf("ListAudit(huge limit): %v", err)
	}
}
