package model

import "time"

// EventKind is the kind of notable thing that happened to a monitor.
type EventKind string

const (
	EvDown            EventKind = "down"
	EvRecovered       EventKind = "recovered"
	EvDegraded        EventKind = "degraded"
	EvDegradedCleared EventKind = "degraded_cleared"
	EvReminder        EventKind = "reminder"
	EvCertExpiring    EventKind = "cert_expiring"
	EvDomainExpiring  EventKind = "domain_expiring"
	EvTest            EventKind = "test"
	// EvRegional: the monitor fails from some countries (worldwide
	// checks) while others reach it; EvRegionalCleared when they recover.
	EvRegional        EventKind = "regional"
	EvRegionalCleared EventKind = "regional_cleared"
	// EvLatencyHigh: a response-time percentile over the monitor's window
	// exceeded its limit (internal/latalert); EvLatencyCleared when it's
	// back under. Reason carries the measured value and the limit.
	EvLatencyHigh    EventKind = "latency_high"
	EvLatencyCleared EventKind = "latency_cleared"
)

// Event is emitted by the engine's state machine for alerting and audit.
type Event struct {
	Kind      EventKind
	MonitorID int64
	At        time.Time
	Status    Status
	Reason    string
	ErrClass  ErrClass
	Since     time.Time
	Duration  time.Duration
	LatencyMs int
	Days      int
	OutageID  int64
	// Code and Response describe the check that triggered the event: the
	// HTTP status and a short snippet of the response body (alerts only).
	Code     int
	Response string
}
