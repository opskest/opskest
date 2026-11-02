// Package model holds the pure domain types shared by every opskest
// package: monitors, results, events, statuses and their validation.
// Nothing in this package touches the database, the network or the
// filesystem.
package model

import "fmt"

// Status is the state of a monitor, worst-first order:
// DOWN > PENDING > DEGRADED > UNKNOWN > MAINTENANCE > UP > PAUSED.
type Status uint8

const (
	StatusUnknown Status = iota
	StatusUp
	StatusDegraded
	StatusPending
	StatusDown
	StatusMaintenance
	StatusPaused
)

// String returns the lower-case wire form of the status.
func (s Status) String() string {
	switch s {
	case StatusUnknown:
		return "unknown"
	case StatusUp:
		return "up"
	case StatusDegraded:
		return "degraded"
	case StatusPending:
		return "pending"
	case StatusDown:
		return "down"
	case StatusMaintenance:
		return "maintenance"
	case StatusPaused:
		return "paused"
	default:
		return "unknown"
	}
}

// Severity ranks statuses worst-first: DOWN 6, PENDING 5, DEGRADED 4,
// UNKNOWN 3, MAINTENANCE 2, UP 1, PAUSED 0.
func (s Status) Severity() int {
	switch s {
	case StatusDown:
		return 6
	case StatusPending:
		return 5
	case StatusDegraded:
		return 4
	case StatusUnknown:
		return 3
	case StatusMaintenance:
		return 2
	case StatusUp:
		return 1
	case StatusPaused:
		return 0
	default:
		return 0
	}
}

// BeatChar is the single-byte code stored per heartbeat. Paused monitors
// don't produce beats, so it returns 'k' (unknown) as a harmless fallback.
func (s Status) BeatChar() byte {
	switch s {
	case StatusUnknown:
		return 'k'
	case StatusUp:
		return 'u'
	case StatusDegraded:
		return 'g'
	case StatusPending:
		return 'p'
	case StatusDown:
		return 'd'
	case StatusMaintenance:
		return 'm'
	case StatusPaused:
		return 'k'
	default:
		return 'k'
	}
}

// MarshalJSON emits the status as its string form, e.g. "down".
func (s Status) MarshalJSON() ([]byte, error) {
	return []byte(`"` + s.String() + `"`), nil
}

// UnmarshalJSON accepts the same string form MarshalJSON produces.
func (s *Status) UnmarshalJSON(data []byte) error {
	str := string(data)
	if len(str) >= 2 && str[0] == '"' && str[len(str)-1] == '"' {
		str = str[1 : len(str)-1]
	}
	v, ok := ParseStatus(str)
	if !ok {
		return fmt.Errorf("model: invalid status %q", str)
	}
	*s = v
	return nil
}

// ParseStatus parses the string form of a Status.
func ParseStatus(s string) (Status, bool) {
	switch s {
	case "unknown":
		return StatusUnknown, true
	case "up":
		return StatusUp, true
	case "degraded":
		return StatusDegraded, true
	case "pending":
		return StatusPending, true
	case "down":
		return StatusDown, true
	case "maintenance":
		return StatusMaintenance, true
	case "paused":
		return StatusPaused, true
	default:
		return StatusUnknown, false
	}
}

// Good reports whether the status counts toward "good" in uptime maths:
// Up, Degraded or Pending.
func (s Status) Good() bool {
	return s == StatusUp || s == StatusDegraded || s == StatusPending
}

// Bad reports whether the status counts toward "bad" in uptime maths: Down.
func (s Status) Bad() bool {
	return s == StatusDown
}
