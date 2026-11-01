package api

import (
	"net/http"
	"testing"
)

func TestMonitorDependsOnValidation(t *testing.T) {
	env := newTestEnv(t)
	a := env.createMonitor(httpMonitorIn("a", "https://example.com/a"))
	inB := httpMonitorIn("b", "https://example.com/b")
	inB.Settings.DependsOn = []int64{a.ID}
	b := env.createMonitor(inB)
	if len(b.Settings.DependsOn) != 1 || b.Settings.DependsOn[0] != a.ID {
		t.Fatalf("dependsOn not stored: %+v", b.Settings.DependsOn)
	}

	cases := map[string][]int64{
		"loop":     {b.ID},
		"self":     {a.ID},
		"unknown":  {424242},
		"too many": {b.ID, 1001, 1002, 1003, 1004, 1005},
	}
	for name, deps := range cases {
		in := httpMonitorIn("a", "https://example.com/a")
		in.Settings.DependsOn = deps
		r := env.do(http.MethodPut, "/api/monitors/"+itoa(a.ID), in, "admin")
		if r.status != http.StatusBadRequest || !bytesContains(r.body, "settings.dependsOn") {
			t.Errorf("%s: status %d body %s, want 400 on settings.dependsOn", name, r.status, r.body)
		}
	}
}

// TestDeletingParentDetachesChildren: a deleted parent's id (which SQLite
// may hand to the next new monitor) is removed from its children.
func TestDeletingParentDetachesChildren(t *testing.T) {
	env := newTestEnv(t)
	parent := env.createMonitor(httpMonitorIn("parent", "https://example.com/p"))
	in := httpMonitorIn("child", "https://example.com/c")
	in.Settings.DependsOn = []int64{parent.ID}
	child := env.createMonitor(in)

	if r := env.do(http.MethodDelete, "/api/monitors/"+itoa(parent.ID), nil, "admin"); r.status != http.StatusNoContent {
		t.Fatalf("delete: %d %s", r.status, r.body)
	}
	got := env.do(http.MethodGet, "/api/monitors/"+itoa(child.ID), nil, "admin")
	var out MonitorOut
	got.json(t, &out)
	if len(out.Settings.DependsOn) != 0 {
		t.Fatalf("child still depends on deleted parent: %v", out.Settings.DependsOn)
	}
	if m, _ := env.engine.Monitor(child.ID); m == nil || len(m.Settings.DependsOn) != 0 {
		t.Fatalf("engine copy not detached")
	}
	// And the child can still be saved (no stale "doesn't exist" error).
	if r := env.do(http.MethodPut, "/api/monitors/"+itoa(child.ID), httpMonitorIn("child", "https://example.com/c"), "admin"); r.status != http.StatusOK {
		t.Fatalf("resave child: %d %s", r.status, r.body)
	}
}
