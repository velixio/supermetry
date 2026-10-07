package core

import (
	"bytes"
	"errors"
	"testing"
	"time"
)

func mustLoc(t *testing.T, name string) *time.Location {
	t.Helper()
	loc, err := time.LoadLocation(name)
	if err != nil {
		t.Fatal(err)
	}
	return loc
}

func TestDayOfLocalMidnight(t *testing.T) {
	ktm := mustLoc(t, "Asia/Kathmandu") // +5:45
	kol := mustLoc(t, "Asia/Kolkata")   // +5:30
	ny := mustLoc(t, "America/New_York")
	cases := []struct {
		ts   string
		loc  *time.Location
		want string
	}{
		{"2026-07-01T18:14:59Z", ktm, "2026-07-01"}, // 23:59:59 local
		{"2026-07-01T18:15:00Z", ktm, "2026-07-02"}, // 00:00 local
		{"2026-07-01T18:29:59Z", kol, "2026-07-01"},
		{"2026-07-01T18:30:00Z", kol, "2026-07-02"},
		{"2026-07-02T03:59:59Z", ny, "2026-07-01"}, // EDT -4
		{"2026-07-02T04:00:00Z", ny, "2026-07-02"},
		{"2026-06-30T20:00:00Z", ktm, "2026-07-01"},
	}
	for _, c := range cases {
		ts, _ := time.Parse(time.RFC3339, c.ts)
		if got := DayOf(ts, c.loc).String(); got != c.want {
			t.Errorf("DayOf(%s, %s) = %s, want %s", c.ts, c.loc, got, c.want)
		}
	}
}

func TestDayBounds(t *testing.T) {
	ktm := mustLoc(t, "Asia/Kathmandu")
	d, _ := ParseDay("2026-07-01")
	s, e := d.Bounds(ktm)
	if s.Format(time.RFC3339) != "2026-06-30T18:15:00Z" || e.Format(time.RFC3339) != "2026-07-01T18:15:00Z" {
		t.Fatalf("bounds = %s..%s", s, e)
	}
}

func TestVisitorIDDeterministicAndScoped(t *testing.T) {
	salt1 := bytes.Repeat([]byte{1}, 32)
	salt2 := bytes.Repeat([]byte{2}, 32)
	a := VisitorID(salt1, 1, "10.0.0.1", "UA")
	if a != VisitorID(salt1, 1, "10.0.0.1", "UA") {
		t.Fatal("not deterministic")
	}
	if a == VisitorID(salt1, 2, "10.0.0.1", "UA") {
		t.Fatal("site must be part of the hash (P2)")
	}
	if a == VisitorID(salt2, 1, "10.0.0.1", "UA") {
		t.Fatal("salt must be part of the hash")
	}
	if a == VisitorID(salt1, 1, "10.0.0.1", "UB") || a == VisitorID(salt1, 1, "10.0.0.2", "UA") {
		t.Fatal("ip and ua must be part of the hash")
	}
	// The separator prevents ip/ua boundary ambiguity.
	if VisitorID(salt1, 1, "10.0.0.1", "1UA") == VisitorID(salt1, 1, "10.0.0.11", "UA") {
		t.Fatal("ip/ua concatenation is ambiguous")
	}
}

func TestSaltsRandomPerSiteDay(t *testing.T) {
	s := NewSalts(nil)
	d, _ := ParseDay("2026-07-01")
	a := s.Get(1, d)
	if len(a) != SaltLen {
		t.Fatalf("salt len %d", len(a))
	}
	if !bytes.Equal(a, s.Get(1, d)) {
		t.Fatal("salt must be stable for (site, day)")
	}
	if bytes.Equal(a, s.Get(1, d+1)) || bytes.Equal(a, s.Get(2, d)) {
		t.Fatal("salt must differ per site and per day")
	}
	// Random, not derived: a second store gets a different salt.
	if bytes.Equal(a, NewSalts(nil).Get(1, d)) {
		t.Fatal("salt must be random")
	}
}

func TestSaltsPersistHookAndPurge(t *testing.T) {
	var persisted []SaltKey
	s := NewSalts(func(k SaltKey, salt []byte) { persisted = append(persisted, k) })
	d, _ := ParseDay("2026-07-01")
	s.Get(1, d)
	s.Get(1, d)
	if len(persisted) != 1 || persisted[0] != (SaltKey{1, d}) {
		t.Fatalf("persist hook calls: %v", persisted)
	}
	s.Put(SaltKey{1, d - 5}, bytes.Repeat([]byte{9}, SaltLen))
	s.Get(1, d-1)
	if n := s.Purge(d - 1); n != 1 { // drop days before d-1
		t.Fatalf("purged %d, want 1", n)
	}
	if s.Len() != 2 {
		t.Fatalf("len %d, want 2", s.Len())
	}
}

func TestSaltsUseInjectedRandomSource(t *testing.T) {
	want := bytes.Repeat([]byte{0x5a}, SaltLen)
	s := NewSaltsWithReader(nil, bytes.NewReader(want))
	d, _ := ParseDay("2026-07-01")

	got, err := s.GetOrCreate(1, d)
	if err != nil {
		t.Fatal(err)
	}
	if !bytes.Equal(got, want) {
		t.Fatalf("salt = %x, want injected bytes %x", got, want)
	}
}

func TestSaltsReturnRandomSourceErrorsWithoutInstallingSalt(t *testing.T) {
	wantErr := errors.New("entropy unavailable")
	persisted := false
	s := NewSaltsWithReader(func(SaltKey, []byte) { persisted = true }, errorReader{wantErr})
	d, _ := ParseDay("2026-07-01")

	if _, err := s.GetOrCreate(1, d); !errors.Is(err, wantErr) {
		t.Fatalf("GetOrCreate error = %v, want %v", err, wantErr)
	}
	if s.Len() != 0 {
		t.Fatal("failed salt generation installed an unusable salt")
	}
	if persisted {
		t.Fatal("failed salt generation called the persist hook")
	}
}

type errorReader struct{ err error }

func (r errorReader) Read([]byte) (int, error) { return 0, r.err }
