package scheduler

import "time"

// entry is one scheduled monitor. Only the loop goroutine touches it.
type entry struct {
	id       int64
	interval time.Duration
	// next is when the entry is due. While a run is in flight it is the slot
	// at which an overrun is detected and skipped; the completion replaces it.
	next time.Time
	// planned is the planned start of the run in flight (or of the last run).
	planned time.Time
	// pinned records a Reschedule that arrived during the run in flight: the
	// explicit time wins over the one the completion would compute.
	pinned bool
	// capAt bounds the next run the completion plans, set when the interval
	// changes during the run in flight (capped says it is set).
	capAt  time.Time
	capped bool
	// rerun records a "run now" (Reschedule with d <= 0) that arrived while
	// a run was in flight: the completion then runs it again immediately,
	// instead of the request being counted as an overrun skip.
	rerun bool
	index int // position in the heap, kept by entryHeap
}

// entryHeap is a container/heap min-heap ordered by next, then id so ties
// dispatch in a stable order.
type entryHeap []*entry

func (h entryHeap) Len() int { return len(h) }

func (h entryHeap) Less(i, j int) bool {
	if !h[i].next.Equal(h[j].next) {
		return h[i].next.Before(h[j].next)
	}
	return h[i].id < h[j].id
}

func (h entryHeap) Swap(i, j int) {
	h[i], h[j] = h[j], h[i]
	h[i].index = i
	h[j].index = j
}

func (h *entryHeap) Push(x any) {
	e := x.(*entry)
	e.index = len(*h)
	*h = append(*h, e)
}

func (h *entryHeap) Pop() any {
	old := *h
	n := len(old) - 1
	e := old[n]
	old[n] = nil
	e.index = -1
	*h = old[:n]
	return e
}
