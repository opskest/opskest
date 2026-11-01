package engine

import (
	"math"
	"time"

	"opskest/internal/model"
)

// MonitorSummary is the live, in-memory view of one monitor carried over
// SSE and GET /api/monitors (spec §14). The JSON tags are consumed as-is by
// web/src/lib/types.ts. Uptime values are percentages 0–100 (ruling R6), or
// null when there is nothing to count.
type MonitorSummary struct {
	ID          int64             `json:"id"`
	Name        string            `json:"name"`
	Type        model.MonitorType `json:"type"`
	GroupID     *int64            `json:"groupId"`
	TagIDs      []int64           `json:"tagIds"`
	Target      string            `json:"target"`
	Status      model.Status      `json:"status"`
	Since       int64             `json:"since"`
	LatencyMs   int               `json:"latencyMs"`
	Uptime24h   *float64          `json:"uptime24h"`
	Uptime7d    *float64          `json:"uptime7d"`
	Uptime30d   *float64          `json:"uptime30d"`
	Uptime90d   *float64          `json:"uptime90d"`
	Beats       string            `json:"beats"`
	Lat         []int             `json:"lat"`
	LastCheckAt int64             `json:"lastCheckAt"`
	IntervalS   int               `json:"intervalS"`
	CertDays    *int              `json:"certDays"`
	DomainDays  *int              `json:"domainDays"`
	Ack         *AckInfo          `json:"ack"`
	Reason      string            `json:"reason"`
	ErrClass    model.ErrClass    `json:"errClass"`
	Paused      bool              `json:"paused"`
	Maint       bool              `json:"maint"`
	OutageID    *int64            `json:"outageId"`
	Runbook     string            `json:"runbook"`
	Vendor      bool              `json:"vendor"` // 3rd-party status feed: not one of your own monitors
	// SuppressedBy is the down parent holding this monitor's outage alerts.
	SuppressedBy *int64 `json:"suppressedBy"`
}

// AckInfo says who acknowledged the monitor's open outage, and when (ms).
type AckInfo struct {
	By string `json:"by"`
	At int64  `json:"at"`
}

// emptyIDs keeps tagIds a JSON [] rather than null. It has zero capacity,
// so an append by a caller can never write into it.
var emptyIDs = []int64{}

// daysUntil is the whole number of days from now until t, rounded down
// (negative once t has passed).
func daysUntil(t, now time.Time) int {
	return int(math.Floor(float64(t.Sub(now)) / float64(24*time.Hour)))
}

// summaryLocked returns en's summary, rebuilding it only when the entry
// changed since the cached copy or the UTC hour rolled over (uptime
// windows, cert and domain days all move with time). Callers hold en.mu.
// Everything the cached value points at is immutable once built, so
// handing the same slices/pointers to several callers is safe.
func (en *entry) summaryLocked(now time.Time, sp *uptimeSpans) MonitorSummary {
	if en.cacheOK && en.cacheVer == en.ver && en.cacheHour == sp.hourNow {
		return en.cache
	}
	m := en.m
	s := MonitorSummary{
		ID:          m.ID,
		Name:        m.Name,
		Type:        m.Type,
		GroupID:     m.GroupID,
		TagIDs:      m.TagIDs,
		Target:      m.Target,
		Status:      en.disp,
		Since:       en.dispSince.UnixMilli(),
		LatencyMs:   en.lastLat,
		LastCheckAt: en.lastAt,
		IntervalS:   m.IntervalS,
		Reason:      en.reason,
		ErrClass:    en.errClass,
		Paused:      en.paused,
		Maint:       en.maint,
		Runbook:     m.Runbook,
		Vendor:      m.Settings.Vendor,
	}
	if s.TagIDs == nil {
		s.TagIDs = emptyIDs
	}
	s.Uptime24h, s.Uptime7d, s.Uptime30d, s.Uptime90d = en.win.uptimes(*sp)

	var buf [summaryBeats]byte
	n := 0
	lat := make([]int, 0, min(en.ring.n, summaryBeats))
	en.ring.last(summaryBeats, func(b beat) {
		buf[n] = b.c
		n++
		lat = append(lat, int(b.lat))
	})
	s.Beats = string(buf[:n])
	s.Lat = lat

	if na := en.rt.CertNotAfter; na != nil {
		d := daysUntil(time.UnixMilli(*na), now)
		s.CertDays = &d
	}
	if exp := en.rt.DomainExpiresAt; exp != nil {
		d := daysUntil(time.UnixMilli(*exp), now)
		s.DomainDays = &d
	}
	if o := en.outage; o != nil {
		id := o.ID
		s.OutageID = &id
		s.SuppressedBy = o.SuppressedBy
		if o.AckedBy != nil {
			at := int64(0)
			if o.AckedAt != nil {
				at = *o.AckedAt
			}
			s.Ack = &AckInfo{By: *o.AckedBy, At: at}
		}
	}

	en.cache, en.cacheVer, en.cacheHour, en.cacheOK = s, en.ver, sp.hourNow, true
	return s
}
