package cronexpr

import (
	"strings"
	"testing"
	"time"
)

func mustTime(t *testing.T, s string) time.Time {
	t.Helper()
	v, err := time.Parse(time.RFC3339, s)
	if err != nil {
		t.Fatal(err)
	}
	return v
}

func TestNext(t *testing.T) {
	berlin, err := time.LoadLocation("Europe/Berlin")
	if err != nil {
		t.Skip("tzdata unavailable")
	}
	cases := []struct {
		name, expr, after, want string
		loc                     *time.Location
	}{
		{"every 15", "*/15 * * * *", "2026-11-20T10:07:00Z", "2026-11-20T10:15:00Z", nil},
		{"strictly after", "*/15 * * * *", "2026-11-20T10:15:00Z", "2026-11-20T10:30:00Z", nil},
		{"weekday 9 in Berlin", "0 9 * * 1-5", "2026-11-20T08:00:00Z", "2026-11-23T08:00:00Z", berlin},
		{"31st skips short months", "0 0 31 * *", "2026-11-01T00:00:00Z", "2026-12-31T00:00:00Z", nil},
		{"hourly macro", "@hourly", "2026-11-20T10:00:00Z", "2026-11-20T11:00:00Z", nil},
		{"names", "30 6 * jan mon", "2026-12-31T00:00:00Z", "2027-01-04T06:30:00Z", nil},
		{"dom and dow are OR", "0 0 1 * mon", "2026-11-02T00:00:00Z", "2026-11-09T00:00:00Z", nil},
		{"sunday as 7", "0 0 * * 7", "2026-11-20T00:00:00Z", "2026-11-22T00:00:00Z", nil},
		{"step from value", "5/20 * * * *", "2026-11-20T10:30:00Z", "2026-11-20T10:45:00Z", nil},
		{"leap day", "0 0 29 2 *", "2026-11-20T00:00:00Z", "2028-02-29T00:00:00Z", nil},
	}
	for _, c := range cases {
		t.Run(c.name, func(t *testing.T) {
			s, err := Parse(c.expr)
			if err != nil {
				t.Fatal(err)
			}
			got := s.Next(mustTime(t, c.after), c.loc)
			if want := mustTime(t, c.want); !got.Equal(want) {
				t.Fatalf("Next = %s, want %s", got.Format(time.RFC3339), want.Format(time.RFC3339))
			}
		})
	}
}

func TestParseErrors(t *testing.T) {
	cases := []struct{ expr, want string }{
		{"61 * * * *", "field 1"},
		{"* 24 * * *", "field 2"},
		{"* * 0 * *", "field 3"},
		{"* * * 13 *", "field 4"},
		{"* * * * 8", "field 5"},
		{"* * * *", "5 fields"},
		{"*/0 * * * *", "step"},
		{"5-1 * * * *", "backwards"},
		{"a * * * *", "invalid value"},
	}
	for _, c := range cases {
		if _, err := Parse(c.expr); err == nil || !strings.Contains(err.Error(), c.want) {
			t.Errorf("Parse(%q) error = %v, want containing %q", c.expr, err, c.want)
		}
	}
}

func TestNextInterval(t *testing.T) {
	ok := mustTime(t, "2026-11-20T10:00:00Z")
	if got := NextInterval(ok, 2, "hour"); !got.Equal(mustTime(t, "2026-11-20T12:00:00Z")) {
		t.Fatalf("2 hours after 10:00Z = %s", got)
	}
	if got := NextInterval(ok, 1, "week"); !got.Equal(mustTime(t, "2026-11-27T10:00:00Z")) {
		t.Fatalf("1 week = %s", got)
	}
	if !NextInterval(ok, 0, "hour").IsZero() || !NextInterval(ok, 1, "fortnight").IsZero() {
		t.Fatal("invalid interval should give the zero time")
	}
}

func FuzzParse(f *testing.F) {
	for _, seed := range []string{"* * * * *", "*/15 0-5 1,15 jan-mar mon", "@daily", "61 * * * *", "0 0 31 2 *", "5/7 * * * 7"} {
		f.Add(seed)
	}
	base := time.Date(2026, 11, 20, 10, 7, 0, 0, time.UTC)
	f.Fuzz(func(t *testing.T, expr string) {
		s, err := Parse(expr)
		if err != nil {
			return
		}
		next := s.Next(base, time.UTC)
		if !next.IsZero() && !next.After(base) {
			t.Fatalf("Next(%q) = %s does not advance", expr, next)
		}
	})
}
