// Package latency holds the compact latency histogram stored on every
// rollup row, so p50/p90/p95/p99 survive long after raw heartbeats are
// pruned.
//
// Bins grow by a factor of 1.1, so any quantile read back is within ~5%
// of the true sample value. The encoded form is a sparse list of
// (bin delta, count) uvarint pairs — a typical hourly row is ~15 bytes.
// It is merged inside SQLite by opskest_hist_merge (store/histfunc.go), so
// histograms accumulate through the single writer's upsert like every
// other rollup counter.
package latency

import (
	"encoding/binary"
	"math"
	"sort"
)

const (
	growth = 1.1
	// maxBin bounds a decoded bin index: anything past it is corruption
	// (60s already lands in bin 117).
	maxBin = 512
)

var lnGrowth = math.Log(growth)

// Bin returns the histogram bin for a latency in ms. ms <= 0 is bin 0.
func Bin(ms int) int {
	if ms <= 0 {
		return 0
	}
	return 1 + int(math.Floor(math.Log(float64(ms))/lnGrowth))
}

// Rep returns a bin's representative value: the geometric midpoint of
// [1.1^(bin-1), 1.1^bin), rounded to whole ms.
func Rep(bin int) int {
	if bin <= 0 {
		return 0
	}
	return int(math.Round(math.Pow(growth, float64(bin)-0.5)))
}

// Hist maps bin -> sample count.
type Hist map[int]uint64

// Add records one latency sample. Negative values (a failed check with no
// measured latency) are ignored.
func (h Hist) Add(ms int) {
	if ms < 0 {
		return
	}
	h[Bin(ms)]++
}

// Merge adds every count in o into h.
func (h Hist) Merge(o Hist) {
	for b, n := range o {
		h[b] += n
	}
}

// Count returns the total number of samples.
func (h Hist) Count() uint64 {
	var n uint64
	for _, c := range h {
		n += c
	}
	return n
}

func (h Hist) sortedBins() []int {
	bins := make([]int, 0, len(h))
	for b, n := range h {
		if n > 0 {
			bins = append(bins, b)
		}
	}
	sort.Ints(bins)
	return bins
}

// Encode returns the sparse wire form, or nil for an empty histogram (so
// it binds as SQL NULL and costs nothing on a row with no samples).
func (h Hist) Encode() []byte {
	bins := h.sortedBins()
	if len(bins) == 0 {
		return nil
	}
	out := make([]byte, 0, len(bins)*3)
	prev := 0
	for _, b := range bins {
		out = binary.AppendUvarint(out, uint64(b-prev))
		out = binary.AppendUvarint(out, h[b])
		prev = b
	}
	return out
}

// Decode parses Encode's output. Anything malformed decodes to an empty
// histogram: a bad blob costs one row's percentiles, never an error.
func Decode(b []byte) Hist {
	h := Hist{}
	bin := 0
	for len(b) > 0 {
		delta, n := binary.Uvarint(b)
		if n <= 0 {
			return Hist{}
		}
		b = b[n:]
		count, m := binary.Uvarint(b)
		if m <= 0 {
			return Hist{}
		}
		b = b[m:]
		if delta > maxBin || bin+int(delta) > maxBin {
			return Hist{}
		}
		bin += int(delta)
		h[bin] += count
	}
	return h
}

// MergeBlobs merges two encoded histograms. Either side may be nil (a
// legacy row, or a commit with no latency samples).
func MergeBlobs(a, b []byte) []byte {
	if len(a) == 0 {
		return b
	}
	if len(b) == 0 {
		return a
	}
	h := Decode(a)
	h.Merge(Decode(b))
	return h.Encode()
}

// Quantile returns the q-quantile (0 < q <= 1, nearest-rank) of h. The
// result is clamped to [min, max] when min <= max — callers pass a
// bucket's exact lat_min/lat_max, which makes p0/p100 exact. ok is false
// for an empty histogram.
func (h Hist) Quantile(q float64, min, max int) (int, bool) {
	total := h.Count()
	if total == 0 {
		return 0, false
	}
	rank := uint64(math.Ceil(q * float64(total)))
	if rank < 1 {
		rank = 1
	}
	if rank > total {
		rank = total
	}
	var seen uint64
	v := 0
	for _, b := range h.sortedBins() {
		seen += h[b]
		if seen >= rank {
			v = Rep(b)
			break
		}
	}
	if min <= max {
		if v < min {
			v = min
		}
		if v > max {
			v = max
		}
	}
	return v, true
}
