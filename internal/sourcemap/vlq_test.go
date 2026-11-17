package sourcemap

import (
	"reflect"
	"strings"
	"testing"
)

func encodeVLQ(v int) string {
	u := uint64(v) << 1
	if v < 0 {
		u = uint64(-v)<<1 | 1
	}
	var sb strings.Builder
	for {
		d := u & 31
		u >>= 5
		if u > 0 {
			d |= 32
		}
		sb.WriteByte(b64[d])
		if u == 0 {
			return sb.String()
		}
	}
}

func TestDecodeVLQTable(t *testing.T) {
	for _, tc := range []struct {
		in  string
		val int
	}{
		{"A", 0}, {"C", 1}, {"D", -1}, {"gB", 16}, {"hB", -16}, {"uorrC", 1234567},
	} {
		v, n, err := decodeVLQ(tc.in, 0)
		if err != nil || v != tc.val || n != len(tc.in) {
			t.Errorf("decodeVLQ(%q) = %d,%d,%v want %d", tc.in, v, n, err, tc.val)
		}
		if got := encodeVLQ(tc.val); got != tc.in {
			t.Errorf("encodeVLQ(%d) = %q want %q", tc.val, got, tc.in)
		}
	}
}

func TestDecodeSegmentMulti(t *testing.T) {
	got, err := decodeSegment("AAgBC", nil)
	if err != nil || !reflect.DeepEqual(got, []int{0, 0, 16, 1}) {
		t.Fatalf("got %v, %v", got, err)
	}
	for _, bad := range []string{"g", "A!", "gggggggg", "/////////"} {
		if _, err := decodeSegment(bad, nil); err == nil {
			t.Errorf("%q: expected error", bad)
		}
	}
}

func TestVLQRoundTrip(t *testing.T) {
	for _, v := range []int{0, 1, -1, 15, 16, -16, 31, 32, 1 << 20, -(1 << 20), maxVLQ, -maxVLQ} {
		got, _, err := decodeVLQ(encodeVLQ(v), 0)
		if err != nil || got != v {
			t.Errorf("%d: got %d, %v", v, got, err)
		}
	}
}

func FuzzVLQ(f *testing.F) {
	for _, s := range []string{"A", "gB", "uorrC", "AAgBC", "", "////", "ggggggg"} {
		f.Add(s)
	}
	f.Fuzz(func(t *testing.T, s string) {
		vals, err := decodeSegment(s, nil)
		if err != nil {
			return
		}
		var re strings.Builder
		for _, v := range vals {
			re.WriteString(encodeVLQ(v))
		}
		back, err := decodeSegment(re.String(), nil)
		if err != nil || !reflect.DeepEqual(back, vals) {
			t.Fatalf("round trip mismatch: %v vs %v (%v)", vals, back, err)
		}
	})
}
