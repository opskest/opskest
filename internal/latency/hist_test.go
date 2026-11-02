package latency

import (
	"bytes"
	"math"
	"math/rand"
	"sort"
	"testing"
)

func TestBinMonotonic(t *testing.T) {
	if Bin(0) != 0 {
		t.Fatalf("Bin(0) = %d, want 0", Bin(0))
	}
	prev := Bin(1)
	for ms := 2; ms <= 120_000; ms++ {
		b := Bin(ms)
		if b < prev {
			t.Fatalf("Bin(%d) = %d < Bin(%d) = %d", ms, b, ms-1, prev)
		}
		prev = b
	}
	if b := Bin(60_000); b > 127 {
		t.Fatalf("Bin(60000) = %d, want <= 127", b)
	}
}

func TestEncodeDecodeRoundTrip(t *testing.T) {
	h := Hist{}
	for _, ms := range []int{0, 1, 2, 2, 50, 51, 900, 30_000, 30_000} {
		h.Add(ms)
	}
	h.Add(-1) // failed checks carry no latency: ignored
	got := Decode(h.Encode())
	if len(got) != len(h) || got.Count() != 9 {
		t.Fatalf("round trip = %v, want %v", got, h)
	}
	for b, n := range h {
		if got[b] != n {
			t.Fatalf("bin %d: got %d want %d", b, got[b], n)
		}
	}
	if (Hist{}).Encode() != nil {
		t.Fatalf("empty hist should encode to nil")
	}
}

func TestMergeBlobs(t *testing.T) {
	a, b, all := Hist{}, Hist{}, Hist{}
	r := rand.New(rand.NewSource(1))
	for i := 0; i < 500; i++ {
		ms := r.Intn(3000)
		all.Add(ms)
		if i%2 == 0 {
			a.Add(ms)
		} else {
			b.Add(ms)
		}
	}
	if !bytes.Equal(MergeBlobs(nil, a.Encode()), a.Encode()) {
		t.Fatalf("MergeBlobs(nil, a) != a")
	}
	if !bytes.Equal(MergeBlobs(a.Encode(), nil), a.Encode()) {
		t.Fatalf("MergeBlobs(a, nil) != a")
	}
	if !bytes.Equal(MergeBlobs(a.Encode(), b.Encode()), all.Encode()) {
		t.Fatalf("MergeBlobs(a, b) != hist of all samples")
	}
}

func TestCorruptDecodesEmpty(t *testing.T) {
	for _, blob := range [][]byte{{0xff}, {0x01}, {0xff, 0xff, 0xff, 0xff, 0xff, 0xff, 0xff, 0xff, 0xff, 0xff, 0x01}} {
		if n := Decode(blob).Count(); n != 0 {
			t.Fatalf("Decode(%x).Count() = %d, want 0", blob, n)
		}
	}
	// A corrupt side of a merge is treated as empty, not propagated.
	good := Hist{100: 3}.Encode()
	if !bytes.Equal(MergeBlobs([]byte{0xff}, good), good) {
		t.Fatalf("merge with corrupt blob should keep the good side")
	}
}

func TestQuantileAccuracy(t *testing.T) {
	r := rand.New(rand.NewSource(42))
	const n = 100_000
	samples := make([]int, n)
	h := Hist{}
	for i := range samples {
		// Lognormal, median ~150ms, long tail — the shape of real HTTP latency.
		ms := int(math.Exp(5.0 + 0.6*r.NormFloat64()))
		samples[i] = ms
		h.Add(ms)
	}
	sort.Ints(samples)
	for _, q := range []float64{0.5, 0.9, 0.95, 0.99} {
		exact := samples[int(math.Ceil(q*n))-1]
		got, ok := h.Quantile(q, samples[0], samples[n-1])
		if !ok {
			t.Fatalf("q%.2f: no value", q)
		}
		if diff := math.Abs(float64(got - exact)); diff > 0.05*float64(exact)+1 {
			t.Errorf("q%.2f = %d, exact %d (off by %.1f%%)", q, got, exact, 100*diff/float64(exact))
		}
	}
}

func TestQuantileEdges(t *testing.T) {
	if _, ok := (Hist{}).Quantile(0.5, 0, 0); ok {
		t.Fatalf("empty hist should have no quantile")
	}
	h := Hist{}
	h.Add(1000)
	// Single sample: clamping to the bucket's exact min/max makes it exact.
	if got, _ := h.Quantile(0.99, 1000, 1000); got != 1000 {
		t.Fatalf("single-sample p99 = %d, want 1000", got)
	}
	// min > max means "no clamp".
	if got, _ := h.Quantile(0.5, 1, 0); got < 950 || got > 1050 {
		t.Fatalf("unclamped p50 = %d, want ~1000", got)
	}
}
