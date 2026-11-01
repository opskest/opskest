package engine

import (
	"reflect"
	"testing"
	"time"

	"opskest/internal/model"
)

var (
	t0 = time.Date(2026, 9, 25, 10, 0, 0, 0, time.UTC)
	t1 = t0.Add(time.Minute)

	okRes   = model.Result{OK: true, LatencyMs: 12}
	degRes  = model.Result{OK: true, Degraded: true, LatencyMs: 900, Reason: "slow: 900 ms > 500 ms"}
	failRes = model.Result{OK: false, LatencyMs: -1, Reason: "HTTP 503", ErrClass: model.ErrHTTPStatus}
)

func st(s model.Status, fails int) State {
	return State{Status: s, Since: t0, Fails: fails, UnderStatus: s}
}

func TestStepTable(t *testing.T) {
	cases := []struct {
		name      string
		cur       State
		r         model.Result
		retries   int
		want      model.Status
		wantFails int
		wantSince time.Time
		events    []model.EventKind
	}{
		{"UP+ok", st(model.StatusUp, 0), okRes, 2, model.StatusUp, 0, t0, nil},
		{"UP+fail retries=2 → PENDING(1)", st(model.StatusUp, 0), failRes, 2, model.StatusPending, 1, t1, nil},
		{"PENDING(1)+fail retries=2 → PENDING(2)", st(model.StatusPending, 1), failRes, 2, model.StatusPending, 2, t0, nil},
		{"PENDING(2)+fail retries=2 → DOWN", st(model.StatusPending, 2), failRes, 2, model.StatusDown, 3, t1, []model.EventKind{model.EvDown}},
		{"UP+fail retries=0 → DOWN", st(model.StatusUp, 0), failRes, 0, model.StatusDown, 1, t1, []model.EventKind{model.EvDown}},
		{"DOWN+ok → UP", st(model.StatusDown, 3), okRes, 2, model.StatusUp, 0, t1, []model.EventKind{model.EvRecovered}},
		{"DOWN+degraded → DEGRADED", st(model.StatusDown, 3), degRes, 2, model.StatusDegraded, 0, t1, []model.EventKind{model.EvRecovered, model.EvDegraded}},
		{"UP+degraded → DEGRADED", st(model.StatusUp, 0), degRes, 2, model.StatusDegraded, 0, t1, []model.EventKind{model.EvDegraded}},
		{"DEGRADED+ok → UP", st(model.StatusDegraded, 0), okRes, 2, model.StatusUp, 0, t1, []model.EventKind{model.EvDegradedCleared}},
		{"DEGRADED+degraded → DEGRADED", st(model.StatusDegraded, 0), degRes, 2, model.StatusDegraded, 0, t0, nil},
		{"DEGRADED+fail retries=0 → DOWN", st(model.StatusDegraded, 0), failRes, 0, model.StatusDown, 1, t1, []model.EventKind{model.EvDown}},
		{"DEGRADED+fail retries=1 → PENDING", st(model.StatusDegraded, 0), failRes, 1, model.StatusPending, 1, t1, nil},
		{"PENDING+ok → UP (never down)", st(model.StatusPending, 1), okRes, 2, model.StatusUp, 0, t1, nil},
		{"PENDING+degraded → DEGRADED", st(model.StatusPending, 1), degRes, 2, model.StatusDegraded, 0, t1, []model.EventKind{model.EvDegraded}},
		{"UNKNOWN+ok → UP", st(model.StatusUnknown, 0), okRes, 2, model.StatusUp, 0, t1, nil},
		{"UNKNOWN+fail retries=0 → DOWN", st(model.StatusUnknown, 0), failRes, 0, model.StatusDown, 1, t1, []model.EventKind{model.EvDown}},
		{"UNKNOWN+fail retries=2 → PENDING", st(model.StatusUnknown, 0), failRes, 2, model.StatusPending, 1, t1, nil},
		{"DOWN+fail → DOWN", st(model.StatusDown, 3), failRes, 2, model.StatusDown, 4, t0, nil},
	}
	for _, c := range cases {
		tr := Step(c.cur, c.r, c.retries, t1)
		if tr.Next.Status != c.want {
			t.Errorf("%s: status = %v, want %v", c.name, tr.Next.Status, c.want)
		}
		if tr.Next.UnderStatus != c.want {
			t.Errorf("%s: under status = %v, want %v", c.name, tr.Next.UnderStatus, c.want)
		}
		if tr.Next.Fails != c.wantFails {
			t.Errorf("%s: fails = %d, want %d", c.name, tr.Next.Fails, c.wantFails)
		}
		if !tr.Next.Since.Equal(c.wantSince) {
			t.Errorf("%s: since = %v, want %v", c.name, tr.Next.Since, c.wantSince)
		}
		if len(tr.Events) != 0 || len(c.events) != 0 {
			if !reflect.DeepEqual(tr.Events, c.events) {
				t.Errorf("%s: events = %v, want %v", c.name, tr.Events, c.events)
			}
		}
	}
}

// TestStepRetriesSequence walks UP → PENDING → PENDING → DOWN with
// retries=2 and checks Since is the flip time.
func TestStepRetriesSequence(t *testing.T) {
	s := st(model.StatusUp, 0)
	var all []model.EventKind
	times := []time.Time{t0.Add(10 * time.Second), t0.Add(30 * time.Second), t0.Add(50 * time.Second)}
	for _, at := range times {
		tr := Step(s, failRes, 2, at)
		s = tr.Next
		all = append(all, tr.Events...)
	}
	if s.Status != model.StatusDown || !s.Since.Equal(times[2]) {
		t.Fatalf("after 3 fails: %+v, want DOWN since %v", s, times[2])
	}
	if !reflect.DeepEqual(all, []model.EventKind{model.EvDown}) {
		t.Fatalf("events = %v, want [down]", all)
	}
}
