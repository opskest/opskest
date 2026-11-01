package api

import (
	"context"
	"fmt"
	"hash/fnv"
	"sort"
	"sync"
	"time"

	"opskest/internal/store"
)

// A public status page's daily bars cost one rollup query per monitor. A
// page shared widely during an outage would otherwise repeat that for
// every visitor, so the rollup part is built at most once a minute per
// (page, exact monitor set, window) and served from memory in between.
// Incidents are layered on per request (one cheap query), so a renamed or
// unlinked incident never lingers in tooltips; live status never comes
// from here — it is read from the engine on every request.

const pageHistoryTTL = time.Minute

type dayIncident struct {
	ID     int64  `json:"id"`
	Title  string `json:"title"`
	Impact string `json:"impact"`
}

type dayEntry struct {
	Day         int64         `json:"day"`
	Uptime      *float64      `json:"uptime"`
	Down        int           `json:"down"`
	DownMinutes int           `json:"downMinutes"`
	Incidents   []dayIncident `json:"incidents,omitempty"`
}

type pageHistoryData struct {
	daily  map[int64][]dayEntry
	uptime map[int64]*float64 // uptime % over the page's history window
}

type pageCacheEntry struct {
	at   time.Time
	data pageHistoryData // never mutated after it is stored
}

// pageCache holds built rollup snapshots plus one build lock per key, so
// a slow rebuild of one big page never blocks requests for another.
type pageCache struct {
	mu      sync.Mutex
	entries map[string]pageCacheEntry
	locks   map[string]*sync.Mutex
}

func (c *pageCache) get(key string, now time.Time) (pageHistoryData, bool) {
	c.mu.Lock()
	defer c.mu.Unlock()
	e, ok := c.entries[key]
	if !ok || now.Sub(e.at) >= pageHistoryTTL {
		return pageHistoryData{}, false
	}
	return e.data, true
}

func (c *pageCache) lockFor(key string) *sync.Mutex {
	c.mu.Lock()
	defer c.mu.Unlock()
	if c.locks == nil {
		c.locks = map[string]*sync.Mutex{}
	}
	l, ok := c.locks[key]
	if !ok {
		l = &sync.Mutex{}
		c.locks[key] = l
	}
	return l
}

func (c *pageCache) put(key string, now time.Time, data pageHistoryData) {
	c.mu.Lock()
	defer c.mu.Unlock()
	if c.entries == nil {
		c.entries = map[string]pageCacheEntry{}
	}
	// Drop expired entries (and their locks) so edited pages and changed
	// monitor sets don't accumulate.
	for k, e := range c.entries {
		if now.Sub(e.at) >= pageHistoryTTL {
			delete(c.entries, k)
			delete(c.locks, k)
		}
	}
	c.entries[key] = pageCacheEntry{at: now, data: data}
}

// pageHistoryKey identifies a page's rollup snapshot by everything it is
// built from: the page, its window, and the exact set of monitor ids.
func pageHistoryKey(page *store.Page, monitorIDs map[int64]string) string {
	ids := make([]int64, 0, len(monitorIDs))
	for id := range monitorIDs {
		ids = append(ids, id)
	}
	sort.Slice(ids, func(i, j int) bool { return ids[i] < ids[j] })
	h := fnv.New64a()
	for _, id := range ids {
		fmt.Fprintf(h, "%d,", id)
	}
	return fmt.Sprintf("%d:%d:%x", page.ID, page.HistoryDays, h.Sum64())
}

// pageHistory returns the page's bars with that window's incidents laid
// onto each day.
func (s *server) pageHistory(ctx context.Context, page *store.Page, monitorIDs map[int64]string, now time.Time) (pageHistoryData, error) {
	key := pageHistoryKey(page, monitorIDs)
	base, ok := s.pages.get(key, now)
	if !ok {
		l := s.pages.lockFor(key)
		l.Lock()
		base, ok = s.pages.get(key, now) // another request may have built it meanwhile
		if !ok {
			var err error
			base, err = s.buildPageRollups(ctx, page, monitorIDs, now)
			if err != nil {
				l.Unlock()
				return pageHistoryData{}, err
			}
			s.pages.put(key, now, base)
		}
		l.Unlock()
	}
	return s.withDayIncidents(ctx, page, monitorIDs, base, now)
}

func historyFrom(page *store.Page, now time.Time) int64 {
	days := page.HistoryDays
	if days <= 0 {
		days = 90
	}
	return now.Add(-time.Duration(days) * 24 * time.Hour).UnixMilli()
}

func (s *server) buildPageRollups(ctx context.Context, page *store.Page, monitorIDs map[int64]string, now time.Time) (pageHistoryData, error) {
	from, to := historyFrom(page, now), now.UnixMilli()
	intervals := map[int64]int{}
	for _, sum := range s.d.Engine.Summaries() {
		if _, on := monitorIDs[sum.ID]; on {
			intervals[sum.ID] = sum.IntervalS
		}
	}

	out := pageHistoryData{daily: map[int64][]dayEntry{}, uptime: map[int64]*float64{}}
	for id := range monitorIDs {
		rollups, err := s.d.Store.RollupSeries(ctx, "rollup_daily", id, from, to)
		if err != nil {
			return pageHistoryData{}, err
		}
		entries := make([]dayEntry, 0, len(rollups))
		var total store.Rollup
		for _, ru := range rollups {
			var uptime *float64
			if v, ok := ru.Uptime(); ok {
				uptime = &v
			}
			entries = append(entries, dayEntry{Day: ru.Bucket, Uptime: uptime, Down: ru.Down,
				DownMinutes: (ru.Down*intervals[id] + 59) / 60})
			total.Up += ru.Up
			total.Degraded += ru.Degraded
			total.Pending += ru.Pending
			total.Down += ru.Down
		}
		out.daily[id] = entries
		if v, ok := total.Uptime(); ok {
			out.uptime[id] = &v
		}
	}
	return out, nil
}

// withDayIncidents copies base, adding to each day the incidents linked
// to the page that spanned it: on the bars of the monitors they're linked
// to, or on every bar when linked to none. base itself is shared and
// never mutated.
func (s *server) withDayIncidents(ctx context.Context, page *store.Page, monitorIDs map[int64]string, base pageHistoryData, now time.Time) (pageHistoryData, error) {
	from, to := historyFrom(page, now), now.UnixMilli()
	incidents, err := s.d.Store.IncidentsForPage(ctx, page.ID, from)
	if err != nil {
		return pageHistoryData{}, err
	}
	incidents = s.visibleIncidents(page, incidents)
	if len(incidents) == 0 {
		return base, nil
	}

	type key struct{ monitor, day int64 }
	byDay := map[key][]dayIncident{}
	pageWide := map[int64][]dayIncident{}
	for _, inc := range incidents {
		end := to
		if inc.ResolvedAt != nil {
			end = *inc.ResolvedAt
		}
		start := inc.CreatedAt
		if start < from {
			start = from
		}
		di := dayIncident{ID: inc.ID, Title: inc.Title, Impact: inc.Impact}
		var linked []int64
		for _, mid := range inc.MonitorIDs {
			if _, on := monitorIDs[mid]; on {
				linked = append(linked, mid)
			}
		}
		// Advance calendar days, rather than assuming a 24-hour duration.
		// IANA transitions can shorten or lengthen a day by more than an hour.
		for d := s.d.Store.DayBucket(start); d <= end; d = store.NextCalendarDayStart(time.UnixMilli(d), s.d.Store.Loc).UnixMilli() {
			if len(inc.MonitorIDs) == 0 {
				pageWide[d] = append(pageWide[d], di)
			}
			for _, mid := range linked {
				byDay[key{mid, d}] = append(byDay[key{mid, d}], di)
			}
		}
	}

	out := pageHistoryData{daily: make(map[int64][]dayEntry, len(base.daily)), uptime: base.uptime}
	for id, days := range base.daily {
		cp := make([]dayEntry, len(days))
		for i, e := range days {
			if extra := append(append([]dayIncident(nil), byDay[key{id, e.Day}]...), pageWide[e.Day]...); len(extra) > 0 {
				e.Incidents = extra
			}
			cp[i] = e
		}
		out.daily[id] = cp
	}
	return out, nil
}
