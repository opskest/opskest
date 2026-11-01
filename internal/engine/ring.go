package engine

const (
	ringSize     = 100 // beats kept per monitor (global constraints)
	summaryBeats = 100 // beats carried in each MonitorSummary: the whole ring
)

// beat is one compact ring-buffer entry: 16 bytes.
type beat struct {
	ts  int64 // unix ms
	lat int32 // -1 = none
	c   byte  // model.Status.BeatChar(), or 'm' for a maintenance beat
}

// ring holds a monitor's last ringSize beats inline (no per-beat
// allocation). head is the next write index; n is how many are filled.
type ring struct {
	buf  [ringSize]beat
	head int
	n    int
}

func (r *ring) push(b beat) {
	r.buf[r.head] = b
	r.head = (r.head + 1) % ringSize
	if r.n < ringSize {
		r.n++
	}
}

// last calls fn for the newest k beats (or fewer), oldest first.
func (r *ring) last(k int, fn func(beat)) {
	if k > r.n {
		k = r.n
	}
	start := (r.head - k + ringSize) % ringSize
	for i := 0; i < k; i++ {
		fn(r.buf[(start+i)%ringSize])
	}
}
