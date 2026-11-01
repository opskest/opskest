// Package hub fans engine state out over Server-Sent Events.
//
// One Hub services every SSE client. Each second (or faster if something
// changed), it takes a dirty-set from the Source, builds one JSON delta,
// and fans it to every client via a non-blocking send. Slow clients whose
// per-client buffers fill up are disconnected.
package hub

import (
	"bytes"
	"context"
	"encoding/json"
	"fmt"
	"log/slog"
	"net/http"
	"sync"
	"sync/atomic"
	"time"

	"opskest/internal/engine"
	"opskest/internal/logx"
	"opskest/internal/store"
)

const (
	tickInterval  = 1 * time.Second
	pingInterval  = 15 * time.Second
	clientBufSize = 4
)

// Source is the read-only interface the Hub polls each tick.
type Source interface {
	Summaries() []engine.MonitorSummary
	Summary(int64) (engine.MonitorSummary, bool)
	TakeDirty() engine.Dirty
	Groups() []store.Group
	ActiveOutages() []store.Outage
	ActiveMaintenance() []store.Maintenance
	Clusters() []engine.Cluster
	Connectivity() (bool, time.Time)
}

// PageScope determines which monitors a scoped SSE stream carries and
// how their data is sanitized. It is re-evaluated each tick.
type PageScope struct {
	MonitorIDs  map[int64]string // id → display name ("" = monitor name)
	Public      bool
	ShowLatency bool
	// HideMaintenance masks maintenance status for pages that do not show it.
	HideMaintenance bool
}

// Hub fans engine state to SSE clients.
type Hub struct {
	src     Source
	version string
	now     func() time.Time
	log     *slog.Logger

	mu      sync.Mutex
	clients map[*client]struct{}

	// gen advances whenever engine state changes. A scoped (status page)
	// snapshot is built once per generation and shared by every viewer of
	// that page, instead of once per viewer per tick.
	gen     atomic.Uint64
	scopeMu sync.Mutex
	scoped  map[string]*scopedEntry
}

// scopedEntry is the last snapshot built for one page. ver advances only
// when the bytes differ, so a viewer is sent data only when it changed.
type scopedEntry struct {
	gen  uint64
	ver  uint64
	data []byte
}

// maxScopedEntries bounds the cache; an edited page gets a new key, so old
// ones are dropped wholesale once there are this many.
const maxScopedEntries = 256

type client struct {
	ch   chan []byte
	done chan struct{}
}

// New creates a Hub. Call Run in a goroutine.
func New(src Source, version string, now func() time.Time, log *slog.Logger) *Hub {
	return &Hub{
		src:     src,
		version: version,
		now:     now,
		log:     log,
		clients: make(map[*client]struct{}),
		scoped:  make(map[string]*scopedEntry),
	}
}

// Run ticks every second, builds deltas and fans them out. It blocks until
// ctx is cancelled.
func (h *Hub) Run(ctx context.Context) {
	tick := time.NewTicker(tickInterval)
	defer tick.Stop()
	lastPing := h.now()

	for {
		select {
		case <-ctx.Done():
			h.mu.Lock()
			for c := range h.clients {
				close(c.ch)
				delete(h.clients, c)
			}
			h.mu.Unlock()
			return
		case <-tick.C:
			dirty := h.src.TakeDirty()
			now := h.now()

			if dirty.Changed != nil || dirty.Removed != nil ||
				dirty.Groups || dirty.Outages || dirty.Maintenance || dirty.System {
				data := h.buildDelta(dirty)
				h.gen.Add(1)
				h.fanOut(data)
			}

			if now.Sub(lastPing) >= pingInterval {
				lastPing = now
				h.fanOut([]byte(": ping\n\n"))
			}
		}
	}
}

// snapshot builds the initial SSE snapshot payload.
func (h *Hub) snapshot() []byte {
	online, since := h.src.Connectivity()
	data := map[string]any{
		"type":        "snapshot",
		"monitors":    h.src.Summaries(),
		"groups":      h.src.Groups(),
		"outages":     h.src.ActiveOutages(),
		"maintenance": h.src.ActiveMaintenance(),
		"clusters":    h.src.Clusters(),
		"system": map[string]any{
			"online": online,
			"since":  since.UnixMilli(),
		},
		"version": h.version,
	}
	b, _ := json.Marshal(data)
	return sseEvent("snapshot", b)
}

// buildDelta builds a delta SSE payload from what changed.
func (h *Hub) buildDelta(d engine.Dirty) []byte {
	data := map[string]any{"type": "delta"}

	if len(d.Changed) > 0 {
		sums := make([]engine.MonitorSummary, 0, len(d.Changed))
		for _, id := range d.Changed {
			if s, ok := h.src.Summary(id); ok {
				sums = append(sums, s)
			}
		}
		data["monitors"] = sums
	}
	if len(d.Removed) > 0 {
		data["removed"] = d.Removed
	}
	if d.Groups {
		data["groups"] = h.src.Groups()
	}
	if d.Outages {
		data["outages"] = h.src.ActiveOutages()
	}
	if d.Maintenance {
		data["maintenance"] = h.src.ActiveMaintenance()
	}
	data["clusters"] = h.src.Clusters()

	if d.System {
		online, since := h.src.Connectivity()
		data["system"] = map[string]any{"online": online, "since": since.UnixMilli()}
	}

	b, _ := json.Marshal(data)
	return sseEvent("delta", b)
}

// fanOut sends data to every connected client. Clients whose buffers are
// full are disconnected.
func (h *Hub) fanOut(data []byte) {
	h.mu.Lock()
	defer h.mu.Unlock()

	for c := range h.clients {
		select {
		case c.ch <- data:
		default:
			// Buffer full: drop the client.
			close(c.ch)
			delete(h.clients, c)
		}
	}
}

func (h *Hub) addClient() *client {
	c := &client{
		ch:   make(chan []byte, clientBufSize),
		done: make(chan struct{}),
	}
	h.mu.Lock()
	h.clients[c] = struct{}{}
	h.mu.Unlock()
	return c
}

func (h *Hub) removeClient(c *client) {
	h.mu.Lock()
	if _, ok := h.clients[c]; ok {
		delete(h.clients, c)
	}
	h.mu.Unlock()
}

// ServeAll serves the full-access SSE stream: snapshot + deltas.
func (h *Hub) ServeAll(w http.ResponseWriter, r *http.Request) {
	flusher, ok := w.(http.Flusher)
	if !ok {
		http.Error(w, "streaming not supported", http.StatusInternalServerError)
		return
	}

	w.Header().Set("Content-Type", "text/event-stream")
	w.Header().Set("Cache-Control", "no-cache")
	w.Header().Set("Connection", "keep-alive")
	w.Header().Set("X-Accel-Buffering", "no")

	// Send initial snapshot.
	snap := h.snapshot()
	if _, err := w.Write(snap); err != nil {
		return
	}
	flusher.Flush()

	c := h.addClient()
	defer h.removeClient(c)

	ctx := r.Context()
	for {
		select {
		case <-ctx.Done():
			return
		case data, ok := <-c.ch:
			if !ok {
				// Hub closed our channel (slow client or shutdown).
				return
			}
			if _, err := w.Write(data); err != nil {
				return
			}
			flusher.Flush()
		}
	}
}

// ServeScoped serves an SSE stream filtered and sanitized for a status
// page. scope is re-evaluated each tick.
func (h *Hub) ServeScoped(w http.ResponseWriter, r *http.Request, key string, scope func() PageScope) {
	flusher, ok := w.(http.Flusher)
	if !ok {
		http.Error(w, "streaming not supported", http.StatusInternalServerError)
		return
	}

	w.Header().Set("Content-Type", "text/event-stream")
	w.Header().Set("Cache-Control", "no-cache")
	w.Header().Set("Connection", "keep-alive")
	w.Header().Set("X-Accel-Buffering", "no")

	// Send initial snapshot.
	snap, sent := h.scopedFor(key, scope)
	if _, err := w.Write(snap); err != nil {
		return
	}
	flusher.Flush()

	c := h.addClient()
	defer h.removeClient(c)

	ctx := r.Context()
	for {
		select {
		case <-ctx.Done():
			return
		case msg, ok := <-c.ch:
			if !ok {
				return
			}
			// A ping keeps a quiet page's connection from idling out at a
			// proxy or load balancer.
			if bytes.HasPrefix(msg, []byte(": ping")) {
				if _, err := w.Write(msg); err != nil {
					return
				}
				flusher.Flush()
				continue
			}
			// Full re-send of the page's snapshot, but only when it changed
			// since this viewer last got one; pings and unrelated monitors'
			// changes send nothing.
			data, ver := h.scopedFor(key, scope)
			if ver == sent {
				continue
			}
			sent = ver
			if _, err := w.Write(data); err != nil {
				return
			}
			flusher.Flush()
		}
	}
}

// scopedFor returns the page's current snapshot and its version, building it
// at most once per engine generation however many viewers there are. key
// must change whenever the page's own settings do.
func (h *Hub) scopedFor(key string, scope func() PageScope) ([]byte, uint64) {
	gen := h.gen.Load()
	h.scopeMu.Lock()
	defer h.scopeMu.Unlock()
	e := h.scoped[key]
	if e != nil && e.gen == gen {
		return e.data, e.ver
	}
	data := h.scopedSnapshot(scope())
	if e == nil {
		if len(h.scoped) >= maxScopedEntries {
			h.scoped = make(map[string]*scopedEntry)
		}
		e = &scopedEntry{}
		h.scoped[key] = e
	}
	if e.ver == 0 || !bytes.Equal(e.data, data) {
		e.data = data
		e.ver++
	}
	e.gen = gen
	return e.data, e.ver
}

func (h *Hub) scopedSnapshot(sc PageScope) []byte {
	allSums := h.src.Summaries()
	var monitors []PublicMonitor
	for _, s := range allSums {
		displayName, ok := sc.MonitorIDs[s.ID]
		if !ok {
			continue
		}
		pm := Sanitize(s, displayName, sc.Public, sc.ShowLatency)
		if sc.HideMaintenance {
			pm = HideMaintenance(pm)
		}
		monitors = append(monitors, pm)
	}
	if monitors == nil {
		monitors = []PublicMonitor{}
	}

	online, since := h.src.Connectivity()
	data := map[string]any{
		"type":     "snapshot",
		"monitors": monitors,
		"system": map[string]any{
			"online": online,
			"since":  since.UnixMilli(),
		},
	}
	b, _ := json.Marshal(data)
	return sseEvent("snapshot", b)
}

// sseEvent formats an SSE event: "event: <name>\ndata: <json>\n\n"
func sseEvent(name string, data []byte) []byte {
	return []byte(fmt.Sprintf("event: %s\ndata: %s\n\n", name, data))
}

// init ensures the logger is not used before it is set.
func init() {
	_ = logx.Err // ensure logx is importable
}
