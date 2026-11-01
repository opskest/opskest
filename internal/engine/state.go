// Package engine is the heart of opskest: it folds every check result
// into a per-monitor state machine held entirely in memory (status, the
// last 100 beats, uptime windows, open outage, certificate and domain
// expiry), persists through the batched store.Writer, and emits the events
// the alert dispatcher delivers. No database read ever happens on the
// check path.
package engine

import (
	"time"

	"opskest/internal/model"
)

// State is a monitor's state-machine position. Status is what the monitor
// displays; UnderStatus is the status ignoring maintenance (and pause), so
// a monitor that fails during a maintenance window displays MAINTENANCE
// while UnderStatus tracks DOWN. Since is when Status began; Fails counts
// consecutive failures.
type State struct {
	Status      model.Status
	Since       time.Time
	Fails       int
	UnderStatus model.Status
}

// Transition is the result of one Step.
type Transition struct {
	Next   State
	Events []model.EventKind
}

// Step applies one result to cur (modelled on Uptime Kuma's
// retries → PENDING → DOWN flow). It knows nothing about maintenance, the
// internet-loss guard, pause or upside-down mode; the engine layers those
// on top. retries is the number of confirmations after the first failure.
//
//	any + success          → UP        (Recovered if was DOWN; DegradedCleared if was DEGRADED)
//	any + success/degraded → DEGRADED  (Recovered if was DOWN; Degraded if was not DEGRADED)
//	UP/DEGRADED/UNKNOWN + failure, retries=0 → DOWN (Down)
//	UP/DEGRADED/UNKNOWN + failure, retries>0 → PENDING (fails=1)
//	PENDING + failure → fails++; DOWN (Down) once fails > retries
//	DOWN + failure → DOWN (reminders are the engine's job)
//
// Since changes only when Status changes, and then to now.
func Step(cur State, r model.Result, retries int, now time.Time) Transition {
	next := cur
	var evs []model.EventKind
	set := func(s model.Status) {
		if next.Status != s {
			next.Since = now
		}
		next.Status = s
	}

	if r.OK {
		next.Fails = 0
		if r.Degraded {
			set(model.StatusDegraded)
			if cur.Status == model.StatusDown {
				evs = append(evs, model.EvRecovered)
			}
			if cur.Status != model.StatusDegraded {
				evs = append(evs, model.EvDegraded)
			}
		} else {
			set(model.StatusUp)
			switch cur.Status {
			case model.StatusDown:
				evs = append(evs, model.EvRecovered)
			case model.StatusDegraded:
				evs = append(evs, model.EvDegradedCleared)
			}
		}
	} else {
		switch cur.Status {
		case model.StatusDown:
			next.Fails++
		case model.StatusPending:
			next.Fails++
			if next.Fails > retries {
				set(model.StatusDown)
				evs = append(evs, model.EvDown)
			}
		default: // UP, DEGRADED, UNKNOWN
			next.Fails = 1
			if retries <= 0 {
				set(model.StatusDown)
				evs = append(evs, model.EvDown)
			} else {
				set(model.StatusPending)
			}
		}
	}
	next.UnderStatus = next.Status
	return Transition{Next: next, Events: evs}
}
