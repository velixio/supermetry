package obsmetrics

import (
	"testing"
	"time"
)

var (
	s1 = time.Date(2026, 11, 18, 10, 0, 0, 0, time.UTC)
	s2 = s1.Add(30 * time.Minute)
)

func TestCounterDeltas(t *testing.T) {
	c := NewDeltaCache(nil)
	key := []byte("k")
	steps := []struct {
		start time.Time
		cum   float64
		want  float64
		ok    bool
	}{
		{s1, 100, 0, false}, // baseline: the first point after a restart emits nothing
		{s1, 130, 30, true},
		{s2, 125, 125, true}, // new start: reset, delta is the new cumulative
		{s2, 140, 15, true},
	}
	for i, st := range steps {
		got, ok := c.Counter(key, st.start, st.cum)
		if ok != st.ok || got != st.want {
			t.Fatalf("step %d: got (%v,%v), want (%v,%v)", i, got, ok, st.want, st.ok)
		}
	}
}

func TestCounterDecreaseIsReset(t *testing.T) {
	c := NewDeltaCache(nil)
	c.Counter([]byte("k"), s1, 200)
	got, ok := c.Counter([]byte("k"), s1, 50)
	if !ok || got != 50 {
		t.Fatalf("got (%v,%v), want (50,true)", got, ok)
	}
}

func TestCounterExpiresAfterAnHour(t *testing.T) {
	now := s1
	c := NewDeltaCache(func() time.Time { return now })
	c.Counter([]byte("k"), s1, 100)
	now = now.Add(61 * time.Minute)
	if got, ok := c.Counter([]byte("k"), s1, 500); ok {
		t.Fatalf("expired entry must be a new baseline, got delta %v", got)
	}
}

func TestHistogramDelta(t *testing.T) {
	c := NewDeltaCache(nil)
	if _, _, _, ok := c.Histogram([]byte("h"), s1, 7, 90, []int64{2, 4, 1, 0}); ok {
		t.Fatal("first sighting must be a baseline")
	}
	n, sum, b, ok := c.Histogram([]byte("h"), s1, 25, 600, []int64{5, 14, 5, 1})
	if !ok || n != 18 || sum != 510 {
		t.Fatalf("got n=%d sum=%v ok=%v", n, sum, ok)
	}
	want := []int64{3, 10, 4, 1}
	for i := range want {
		if b[i] != want[i] {
			t.Fatalf("buckets = %v, want %v", b, want)
		}
	}
}

func TestDeltaCacheBounded(t *testing.T) {
	c := newDeltaCache(nil, 2)
	for _, k := range []string{"a", "b", "c", "d"} {
		c.Counter([]byte(k), s1, 1)
	}
	if c.Len() != 2 {
		t.Fatalf("len = %d, want 2", c.Len())
	}
}

func TestSeriesCap(t *testing.T) {
	r := NewRegistry(3)
	key := func(v string) []byte {
		return SeriesKey{Name: "m", Kind: "gauge", Attrs: map[string]string{"a": v}}.Hash()
	}
	admitted := 0
	for _, v := range []string{"1", "2", "3", "4"} {
		if r.Admit(1, key(v)) {
			admitted++
		}
	}
	if admitted != 3 {
		t.Fatalf("admitted %d, want 3", admitted)
	}
	if !r.Admit(1, key("2")) {
		t.Fatal("a known series must stay admitted after the cap is reached")
	}
	if !r.Admit(2, key("4")) {
		t.Fatal("the cap is per site")
	}
}

func TestBoundsChangeNewHash(t *testing.T) {
	a := SeriesKey{Name: "h", Kind: "histogram", Bounds: []float64{10, 50, 100}}.Hash()
	b := SeriesKey{Name: "h", Kind: "histogram", Bounds: []float64{10, 100}}.Hash()
	if string(a) == string(b) {
		t.Fatal("changed bounds must change the series hash")
	}
}
