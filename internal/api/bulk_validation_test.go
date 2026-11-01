package api

import (
	"context"
	"fmt"
	"net/http"
	"testing"
)

func TestBulkValidatesWholeSelection(t *testing.T) {
	env := newTestEnv(t)
	m := env.createMonitor(httpMonitorIn("selected service", "https://example.com"))
	cases := []struct {
		name string
		body map[string]any
		want int
	}{
		{"empty", map[string]any{"ids": []int64{}, "action": "pause"}, 400},
		{"missing", map[string]any{"ids": []int64{m.ID, 999999}, "action": "pause"}, 404},
		{"negative", map[string]any{"ids": []int64{m.ID, -1}, "action": "pause"}, 400},
		{"bad project", map[string]any{"ids": []int64{m.ID}, "action": "move", "groupId": 999999}, 400},
		{"bad retries", map[string]any{"ids": []int64{m.ID}, "action": "retries", "retries": 11}, 400},
	}
	for _, tc := range cases {
		t.Run(tc.name, func(t *testing.T) {
			resp := env.do(http.MethodPost, "/api/monitors/bulk", tc.body, "admin")
			if resp.status != tc.want {
				t.Fatalf("status %d, want %d: %s", resp.status, tc.want, resp.body)
			}
			stored, err := env.store.GetMonitor(context.Background(), m.ID)
			if err != nil || stored.Paused || stored.GroupID != nil {
				t.Fatalf("invalid selection mutated monitor: %+v, %v", stored, err)
			}
			summary, ok := env.engine.Summary(m.ID)
			if !ok || summary.Paused {
				t.Fatal("invalid selection changed engine")
			}
		})
	}
}

func TestBulkDeduplicatesDeletes(t *testing.T) {
	env := newTestEnv(t)
	m := env.createMonitor(httpMonitorIn("delete once", "https://example.com"))
	resp := env.do(http.MethodPost, "/api/monitors/bulk", map[string]any{"ids": []int64{m.ID, m.ID}, "action": "delete"}, "admin")
	if resp.status != 200 {
		t.Fatalf("delete failed: %d %s", resp.status, resp.body)
	}
	var result struct{ Processed int }
	resp.json(t, &result)
	if result.Processed != 1 {
		t.Fatalf("processed %d, want 1", result.Processed)
	}
	if _, ok := env.engine.Summary(m.ID); ok {
		t.Fatal("deleted monitor still scheduled")
	}
}

func TestProjectRenameRejectsWhitespace(t *testing.T) {
	env := newTestEnv(t)
	g := createGroup(t, env, "Production")
	resp := env.do(http.MethodPut, "/api/groups/"+itoa(g), map[string]any{"name": "   "}, "admin")
	if resp.status != 400 {
		t.Fatalf("status %d, want 400: %s", resp.status, resp.body)
	}
}

// Fail the second database mutation, after validation succeeds. This exercises
// a real persistence failure rather than a rejected request or missing record.
func TestBulkReportsPartialPersistenceFailure(t *testing.T) {
	for _, action := range []string{"pause", "resume", "move", "retries", "delete"} {
		t.Run(action, func(t *testing.T) {
			env := newTestEnv(t)
			first := env.createMonitor(httpMonitorIn("first service", "https://first.example.com"))
			second := env.createMonitor(httpMonitorIn("second service", "https://second.example.com"))
			third := env.createMonitor(httpMonitorIn("third service", "https://third.example.com"))
			if action == "resume" {
				for _, m := range []MonitorOut{first, second, third} {
					if err := env.store.SetPaused(context.Background(), m.ID, true); err != nil {
						t.Fatal(err)
					}
					env.engine.SetPaused(m.ID, true)
				}
			}
			projectID := createGroup(t, env, "Production")
			operation := "UPDATE"
			if action == "delete" {
				operation = "DELETE"
			}
			_, err := env.store.W.ExecContext(context.Background(), fmt.Sprintf(`
				CREATE TRIGGER reject_bulk_mutation BEFORE %s ON monitors
				WHEN OLD.id = %d BEGIN SELECT RAISE(FAIL, 'injected storage failure'); END`, operation, second.ID))
			if err != nil {
				t.Fatal(err)
			}
			resp := env.do(http.MethodPost, "/api/monitors/bulk", map[string]any{
				"ids": []int64{first.ID, second.ID, third.ID}, "action": action,
				"groupId": projectID, "retries": 7,
			}, "admin")
			if resp.status != http.StatusInternalServerError {
				t.Fatalf("status %d, want 500: %s", resp.status, resp.body)
			}
			var result struct {
				Processed int
				FailedID  int64
				Error     string
			}
			resp.json(t, &result)
			if result.Processed != 1 || result.FailedID != second.ID || result.Error == "" {
				t.Fatalf("incorrect partial completion: %+v", result)
			}
			for _, original := range []MonitorOut{second, third} {
				stored, err := env.store.GetMonitor(context.Background(), original.ID)
				if err != nil {
					t.Fatal(err)
				}
				running, ok := env.engine.Monitor(original.ID)
				if !ok {
					t.Fatalf("failed or unattempted service %d stopped monitoring", original.ID)
				}
				wantPaused := action == "resume"
				if stored.Paused != wantPaused || running.Paused != wantPaused ||
					stored.GroupID != nil || running.GroupID != nil ||
					stored.Retries != original.Retries || running.Retries != original.Retries {
					t.Fatalf("failed or unattempted service %d changed", original.ID)
				}
			}
			if action == "delete" {
				if _, ok := env.engine.Monitor(first.ID); ok {
					t.Fatal("successfully deleted service remains scheduled")
				}
			} else {
				stored, err := env.store.GetMonitor(context.Background(), first.ID)
				if err != nil {
					t.Fatal(err)
				}
				switch action {
				case "pause", "resume":
					if stored.Paused != (action == "pause") {
						t.Fatal("first service mutation was not committed")
					}
				case "move":
					if stored.GroupID == nil || *stored.GroupID != projectID {
						t.Fatal("first service did not move")
					}
				case "retries":
					if stored.Retries != 7 {
						t.Fatal("first service retries did not change")
					}
				}
			}
		})
	}
}

func TestDeletePersistenceFailureKeepsMonitoring(t *testing.T) {
	env := newTestEnv(t)
	m := env.createMonitor(httpMonitorIn("keep monitoring", "https://example.com"))
	_, err := env.store.W.ExecContext(context.Background(), `
		CREATE TRIGGER reject_delete BEFORE DELETE ON monitors
		BEGIN SELECT RAISE(FAIL, 'injected storage failure'); END`)
	if err != nil {
		t.Fatal(err)
	}
	resp := env.do(http.MethodDelete, "/api/monitors/"+itoa(m.ID), nil, "admin")
	if resp.status != http.StatusInternalServerError {
		t.Fatalf("status %d, want 500: %s", resp.status, resp.body)
	}
	if _, err := env.store.GetMonitor(context.Background(), m.ID); err != nil {
		t.Fatal(err)
	}
	if _, ok := env.engine.Monitor(m.ID); !ok {
		t.Fatal("failed delete silently stopped monitoring")
	}
}
