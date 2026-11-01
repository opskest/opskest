package api

import (
	"context"
	"errors"
	"net/http"
	"slices"
	"time"

	"opskest/internal/auth"
	"opskest/internal/check"
	"opskest/internal/model"
	"opskest/internal/store"
)

// dnsResolveTimeout bounds a "what does this resolve to right now" lookup.
const dnsResolveTimeout = 5 * time.Second

// dnsResolveIn names what to resolve: a saved dns monitor by id, or an
// unsaved one's target/record/resolver straight from the form.
type dnsResolveIn struct {
	ID       *int64 `json:"id"`
	Target   string `json:"target"`
	Record   string `json:"record"`
	Resolver string `json:"resolver"`
}

type dnsResolveOut struct {
	Record   string   `json:"record"`
	Values   []string `json:"values"`   // current answers, sorted
	Expected []string `json:"expected"` // the saved monitor's list (id form only)
}

// handleDNSResolve answers "what does this record resolve to right now",
// for the form's "Use current answers" and the rotate dialog's preview.
// It never writes anything.
func (s *server) handleDNSResolve(w http.ResponseWriter, r *http.Request) {
	var in dnsResolveIn
	if err := decode(r, &in, 0); err != nil {
		writeErr(w, http.StatusBadRequest, err.Error(), nil)
		return
	}

	probe := model.NewMonitor(model.TypeDNS)
	var expected []string
	if in.ID != nil {
		m, ok := s.dnsMonitor(w, r, *in.ID)
		if !ok {
			return
		}
		probe = m
		expected = m.Settings.DNSExpected()
	} else {
		probe.Name = "resolve"
		probe.Target = in.Target
		probe.Settings.Record = in.Record
		probe.Settings.Resolver = in.Resolver
		if probe.Settings.Record == "" {
			probe.Settings.Record = "A"
		}
		if fe := probe.Validate(); len(fe) > 0 {
			writeErr(w, http.StatusBadRequest, "validation error", fe)
			return
		}
	}

	values, ok := s.resolveNow(w, r, probe)
	if !ok {
		return
	}
	writeJSON(w, http.StatusOK, dnsResolveOut{Record: probe.Settings.Record, Values: values, Expected: nonNil(expected)})
}

// handleDNSRotate replaces a dns monitor's expected values with the list
// the admin confirmed (normally what the preview just resolved) and runs
// a check straight away so the dashboard reflects it.
func (s *server) handleDNSRotate(w http.ResponseWriter, r *http.Request) {
	id, err := pathID(r, "id")
	if err != nil {
		writeErr(w, http.StatusBadRequest, "invalid id", nil)
		return
	}
	var in struct {
		Values []string `json:"values"`
	}
	if err := decode(r, &in, 0); err != nil {
		writeErr(w, http.StatusBadRequest, err.Error(), nil)
		return
	}
	stored, ok := s.dnsMonitor(w, r, id)
	if !ok {
		return
	}
	if len(in.Values) == 0 {
		writeErr(w, http.StatusBadRequest, "validation error", model.FieldErrors{"settings.expectedValues": "at least one value required"})
		return
	}

	values := slices.Clone(in.Values)
	slices.Sort(values)
	values = slices.Compact(values)

	m := stored.Clone()
	previous := nonNil(slices.Clone(m.Settings.DNSExpected()))
	m.Settings.Expected = ""
	m.Settings.ExpectedValues = values
	m.ApplyDefaults()
	if fe := m.Validate(); len(fe) > 0 {
		writeErr(w, http.StatusBadRequest, "validation error", fe)
		return
	}

	m.UpdatedAt = s.d.Now()
	if err := s.d.Store.UpdateMonitor(r.Context(), m); err != nil {
		writeErr(w, http.StatusInternalServerError, "update failed", nil)
		return
	}
	s.d.Engine.UpsertMonitor(m)
	s.d.Engine.RunNow(m.ID)
	u, _ := auth.UserFrom(r.Context())
	s.d.Store.Audit(r.Context(), &u.ID, "rotate_dns", "monitor", &m.ID, m.Name)

	writeJSON(w, http.StatusOK, map[string]any{
		"previous": previous,
		"current":  values,
		"match":    m.Settings.Match,
	})
}

// dnsMonitor loads monitor id, answering 404 (never 403) when it is
// missing or not a dns monitor.
func (s *server) dnsMonitor(w http.ResponseWriter, r *http.Request, id int64) (*model.Monitor, bool) {
	m, err := s.d.Store.GetMonitor(r.Context(), id)
	if err != nil {
		if errors.Is(err, store.ErrNotFound) {
			writeErr(w, http.StatusNotFound, "not found", nil)
		} else {
			writeErr(w, http.StatusInternalServerError, "fetch failed", nil)
		}
		return nil, false
	}
	if m.Type != model.TypeDNS {
		writeErr(w, http.StatusNotFound, "not found", nil)
		return nil, false
	}
	return m, true
}

// resolveNow looks m's record up through its resolver; on failure it
// writes a 502 carrying the classified reason (never the raw error).
func (s *server) resolveNow(w http.ResponseWriter, r *http.Request, m *model.Monitor) ([]string, bool) {
	ctx, cancel := context.WithTimeout(r.Context(), dnsResolveTimeout)
	defer cancel()
	values, err := check.ResolveDNS(ctx, m.Target, m.Settings.Record, m.Settings.Resolver)
	if err != nil {
		_, reason := check.ClassifyErr(err)
		writeErr(w, http.StatusBadGateway, "lookup failed: "+model.TrimReason(reason), nil)
		return nil, false
	}
	if len(values) == 0 {
		writeErr(w, http.StatusBadGateway, "lookup returned no "+m.Settings.Record+" records", nil)
		return nil, false
	}
	return values, true
}

func nonNil(s []string) []string {
	if s == nil {
		return []string{}
	}
	return s
}
