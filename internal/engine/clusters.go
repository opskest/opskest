package engine

import (
	"slices"
	"sort"

	"opskest/internal/model"
)

// Cluster is a likely common cause: at least 2 affected
// monitors (DOWN, PENDING or DEGRADED) on the same target host, or sharing
// an error class. Since is the earliest member's status start (ms). For a
// host cluster ErrClass is the members' most common error class.
type Cluster struct {
	Kind       string         `json:"kind"` // "host" | "errClass"
	Key        string         `json:"key"`
	MonitorIDs []int64        `json:"monitorIds"`
	Since      int64          `json:"since"`
	ErrClass   model.ErrClass `json:"errClass"`
}

const minClusterSize = 2

func affectedStatus(s model.Status) bool {
	return s == model.StatusDown || s == model.StatusPending || s == model.StatusDegraded
}

// setAffected keeps e.affected in step with en's displayed status, so
// Clusters costs O(affected monitors) rather than O(all). Callers hold
// en.mu (lock order: Engine.mu → entry.mu → affMu).
// A 3rd-party feed (vendor) is neither affected nor a dependency parent: it
// is someone else's service.
func (e *Engine) setAffected(id int64, disp model.Status, vendor bool) {
	e.affMu.Lock()
	if vendor {
		delete(e.affected, id)
		delete(e.parentDown, id)
		e.affMu.Unlock()
		return
	}
	if affectedStatus(disp) {
		e.affected[id] = struct{}{}
	} else {
		delete(e.affected, id)
	}
	if disp == model.StatusDown || disp == model.StatusMaintenance {
		e.parentDown[id] = struct{}{}
	} else {
		delete(e.parentDown, id)
	}
	e.affMu.Unlock()
}

// downParent returns the first of parents that is currently down or in
// maintenance, or nil. affMu is a leaf lock, so this is safe to call while
// holding an entry's locks (it never touches another entry's).
func (e *Engine) downParent(parents []int64) *int64 {
	if len(parents) == 0 {
		return nil
	}
	e.affMu.Lock()
	defer e.affMu.Unlock()
	for _, p := range parents {
		if _, down := e.parentDown[p]; down {
			id := p
			return &id
		}
	}
	return nil
}

type clusterMember struct {
	id    int64
	host  string
	class model.ErrClass
	since int64
}

// Clusters groups the currently affected monitors by host and by error
// class. Host clusters come first (the more specific cause), each kind
// ordered by size, then by how long it has lasted. Never nil.
func (e *Engine) Clusters() []Cluster {
	e.affMu.Lock()
	ids := make([]int64, 0, len(e.affected))
	for id := range e.affected {
		ids = append(ids, id)
	}
	e.affMu.Unlock()
	out := []Cluster{}
	if len(ids) < minClusterSize {
		return out
	}

	members := make([]clusterMember, 0, len(ids))
	e.mu.RLock()
	for _, id := range ids {
		en := e.entries[id]
		if en == nil {
			continue
		}
		en.mu.Lock()
		// A group is a consequence of its children and a manual status is a
		// person's call; neither is evidence of a shared cause.
		if affectedStatus(en.disp) && en.m.Type != model.TypeGroup && en.m.Type != model.TypeManual {
			members = append(members, clusterMember{id: id, host: en.host, class: en.errClass, since: en.dispSince.UnixMilli()})
		}
		en.mu.Unlock()
	}
	e.mu.RUnlock()

	byHost := map[string][]clusterMember{}
	byClass := map[model.ErrClass][]clusterMember{}
	for _, m := range members {
		if m.host != "" {
			byHost[m.host] = append(byHost[m.host], m)
		}
		if m.class != model.ErrNone {
			byClass[m.class] = append(byClass[m.class], m)
		}
	}

	var hosts, classes []Cluster
	for host, ms := range byHost {
		if len(ms) >= minClusterSize {
			c := newCluster("host", host, ms)
			c.ErrClass = commonClass(ms)
			hosts = append(hosts, c)
		}
	}
	for class, ms := range byClass {
		if len(ms) >= minClusterSize {
			c := newCluster("errClass", string(class), ms)
			c.ErrClass = class
			classes = append(classes, c)
		}
	}
	sortClusters(hosts)
	sortClusters(classes)
	out = append(out, hosts...)
	return append(out, classes...)
}

func newCluster(kind, key string, ms []clusterMember) Cluster {
	c := Cluster{Kind: kind, Key: key, MonitorIDs: make([]int64, 0, len(ms)), Since: ms[0].since}
	for _, m := range ms {
		c.MonitorIDs = append(c.MonitorIDs, m.id)
		if m.since < c.Since {
			c.Since = m.since
		}
	}
	slices.Sort(c.MonitorIDs)
	return c
}

func commonClass(ms []clusterMember) model.ErrClass {
	counts := map[model.ErrClass]int{}
	best, bestN := model.ErrNone, 0
	for _, m := range ms {
		if m.class == model.ErrNone {
			continue
		}
		counts[m.class]++
		if n := counts[m.class]; n > bestN || (n == bestN && m.class < best) {
			best, bestN = m.class, n
		}
	}
	return best
}

func sortClusters(cs []Cluster) {
	sort.Slice(cs, func(i, j int) bool {
		a, b := cs[i], cs[j]
		if len(a.MonitorIDs) != len(b.MonitorIDs) {
			return len(a.MonitorIDs) > len(b.MonitorIDs)
		}
		if a.Since != b.Since {
			return a.Since < b.Since
		}
		return a.Key < b.Key
	})
}
