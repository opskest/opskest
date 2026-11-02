package alert

import (
	"context"
	"strconv"
	"strings"
	"time"

	"opskest/internal/model"
)

// pendingEvent is one resolved Down/Recovered event sitting in the hold
// buffer, waiting out the storm-detection window before it is sent.
type pendingEvent struct {
	kind       model.EventKind
	item       Item
	channelIDs []int64
}

// stormState tracks, per kind, the most recent Down/Recovered timestamp
// seen for each distinct monitor id, so the intake goroutine can tell
// whether the system is currently "in a storm": at least
// storm_threshold *monitors* (not raw events — a flapping monitor firing
// several Down events in one window must still count once) changing
// within storm_window_s. It is only ever touched by the single intake
// goroutine, so it needs no locking.
type stormState struct {
	downAt, recAt map[int64]time.Time
}

func (s *stormState) record(kind model.EventKind, monitorID int64, at time.Time) {
	var m *map[int64]time.Time
	switch kind {
	case model.EvDown:
		m = &s.downAt
	case model.EvRecovered:
		m = &s.recAt
	default:
		return
	}
	if *m == nil {
		*m = make(map[int64]time.Time)
	}
	(*m)[monitorID] = at
}

// count returns how many distinct monitors had a kind event (Down or
// Recovered) within window of now, pruning anything older in the process.
func (s *stormState) count(kind model.EventKind, now time.Time, window time.Duration) int {
	var m map[int64]time.Time
	switch kind {
	case model.EvDown:
		m = s.downAt
	case model.EvRecovered:
		m = s.recAt
	default:
		return 0
	}

	cutoff := now.Add(-window)
	n := 0
	for id, t := range m {
		if t.After(cutoff) {
			n++
		} else {
			delete(m, id)
		}
	}
	return n
}

// runIntake is the dispatcher's single event-processing goroutine: it
// resolves each published event into an Item, holds Down/Recovered events
// for opts.Hold to detect a storm, and sends everything else immediately.
// Running this on one goroutine keeps the hold buffer and stormState
// lock-free.
func (d *Dispatcher) runIntake(ctx context.Context) {
	defer d.wg.Done()

	var hold []pendingEvent
	var timerC <-chan time.Time

	for {
		select {
		case <-ctx.Done():
			return

		case ev, ok := <-d.intake:
			if !ok {
				return
			}
			item, channelIDs, resolved := d.resolve(ev)
			if !resolved {
				continue
			}

			if ev.Kind == model.EvDown || ev.Kind == model.EvRecovered {
				d.storm.record(ev.Kind, item.MonitorID, ev.At)
				hold = append(hold, pendingEvent{kind: ev.Kind, item: item, channelIDs: channelIDs})
				if timerC == nil {
					timerC = time.NewTimer(d.opts.Hold).C
				}
			} else {
				d.dispatchImmediate(ev.Kind, item, channelIDs)
			}

		case <-timerC:
			d.flushHold(hold)
			hold = nil
			timerC = nil
		}
	}
}

// resolve turns ev into an Item plus the channel ids that should hear
// about it, using Lookup. false means the monitor is unknown and the
// event is dropped.
func (d *Dispatcher) resolve(ev model.Event) (Item, []int64, bool) {
	info, ok := d.lk.AlertInfo(ev.MonitorID)
	if !ok {
		return Item{}, nil, false
	}
	it := Item{
		MonitorID: ev.MonitorID,
		Name:      info.Name,
		Group:     info.Group,
		Target:    info.Target,
		Runbook:   info.Runbook,
		Status:    ev.Status.String(),
		Reason:    ev.Reason,
		Since:     ev.Since,
		Duration:  ev.Duration,
		LatencyMs: ev.LatencyMs,
		Days:      ev.Days,
		URL:       monitorURL(d.opts.AppURL, ev.MonitorID),
		Code:      ev.Code,
		Response:  ev.Response,
	}
	return it, info.ChannelIDs, true
}

func monitorURL(appURL string, id int64) string {
	return strings.TrimRight(appURL, "/") + "/monitors/" + strconv.FormatInt(id, 10)
}

// dispatchImmediate builds a standalone Message for a kind that is never
// held or grouped (everything except Down/Recovered) and queues it to
// every channel that should hear about it.
func (d *Dispatcher) dispatchImmediate(kind model.EventKind, it Item, channelIDs []int64) {
	msg := Message{Kind: kind, Items: []Item{it}, At: d.opts.Now(), AppURL: d.opts.AppURL, Loc: d.opts.Loc}
	msg.Title = Title(msg)
	for _, id := range channelIDs {
		d.enqueueToChannel(id, msg)
	}
}

// flushHold decides, per (kind, channel), whether the events collected
// during the last hold window amount to a storm: chat-type channels
// get one grouped message
// when at least storm_threshold events of that kind fall within
// storm_window_s; otherwise, and always for PagerDuty/Opsgenie, every
// event is sent as its own message.
func (d *Dispatcher) flushHold(hold []pendingEvent) {
	if len(hold) == 0 {
		return
	}
	now := d.opts.Now()
	threshold, window := d.settings()

	storming := map[model.EventKind]bool{
		model.EvDown:      d.storm.count(model.EvDown, now, window) >= threshold,
		model.EvRecovered: d.storm.count(model.EvRecovered, now, window) >= threshold,
	}

	type key struct {
		kind      model.EventKind
		channelID int64
	}
	byChannel := map[key][]Item{}
	// monitorIdx tracks, per (kind, channel), the position each monitor id
	// already occupies in byChannel[k] — so a monitor that fired more than
	// one event in this hold window (e.g. a quick flap) is folded into a
	// single, updated entry rather than listed twice in a grouped message.
	monitorIdx := map[key]map[int64]int{}
	var order []key
	for _, pe := range hold {
		for _, chID := range pe.channelIDs {
			k := key{pe.kind, chID}
			if _, seen := byChannel[k]; !seen {
				order = append(order, k)
				monitorIdx[k] = map[int64]int{}
			}
			if idx, dup := monitorIdx[k][pe.item.MonitorID]; dup {
				byChannel[k][idx] = pe.item // keep position, use latest data
			} else {
				monitorIdx[k][pe.item.MonitorID] = len(byChannel[k])
				byChannel[k] = append(byChannel[k], pe.item)
			}
		}
	}

	for _, k := range order {
		items := byChannel[k]
		ch, ok := d.channel(k.channelID)
		if !ok {
			continue
		}

		// A grouped message only makes sense with more than one distinct
		// monitor; if dedup left this channel with just one, send it as a
		// normal single message instead (so Title uses the singular
		// per-monitor headline, and "N monitors down" with N=1 never
		// appears).
		if storming[k.kind] && isChatType(ch.Type) && len(items) > 1 {
			msg := Message{Kind: k.kind, Items: items, At: now, AppURL: d.opts.AppURL, Grouped: true, Loc: d.opts.Loc}
			msg.Title = Title(msg)
			d.enqueueToChannel(k.channelID, msg)
			continue
		}

		for _, it := range items {
			msg := Message{Kind: k.kind, Items: []Item{it}, At: now, AppURL: d.opts.AppURL, Loc: d.opts.Loc}
			msg.Title = Title(msg)
			d.enqueueToChannel(k.channelID, msg)
		}
	}
}

// settings returns the current storm threshold/window, defaulting to the
// default of 5 within 30s when the caller didn't wire one up.
func (d *Dispatcher) settings() (int, time.Duration) {
	if d.opts.Settings == nil {
		return 5, 30 * time.Second
	}
	return d.opts.Settings()
}

// isChatType reports whether t is one of the channel types storms group
// . PagerDuty and Opsgenie are deliberately excluded: their
// dedup/alias keys must match individual monitors for auto-resolve to
// work.
func isChatType(t string) bool {
	switch t {
	case "slack", "email", "teams", "gchat", "webhook":
		return true
	default:
		return false
	}
}
