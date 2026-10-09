package salts_test

import (
	"bytes"
	"context"
	"testing"
	"time"

	"supermetry/internal/db"
	"supermetry/internal/salts"
	"supermetry/internal/testpg"
)

type fakeClock struct {
	t time.Time
}

func (c *fakeClock) Now() time.Time { return c.t }

func TestVisitorIDScopedBySiteAndSalt(t *testing.T) {
	s1 := bytes.Repeat([]byte{1}, 32)
	s2 := bytes.Repeat([]byte{2}, 32)
	a := salts.VisitorID(s1, 1, "203.0.113.9", "UA")
	if a != salts.VisitorID(s1, 1, "203.0.113.9", "UA") {
		t.Fatal("not deterministic")
	}
	if a == salts.VisitorID(s1, 2, "203.0.113.9", "UA") {
		t.Fatal("same id across sites (cross-site tracking)")
	}
	if a == salts.VisitorID(s2, 1, "203.0.113.9", "UA") {
		t.Fatal("salt not applied")
	}
	if a == salts.VisitorID(s1, 1, "203.0.113.10", "UA") {
		t.Fatal("IP not applied")
	}
	if a == salts.VisitorID(s1, 1, "203.0.113.9", "different UA") {
		t.Fatal("user agent not applied")
	}
	if salts.VisitorID(s1, 1, "203.0.113.9", "9UA") == salts.VisitorID(s1, 1, "203.0.113.99", "UA") {
		t.Fatal("IP and user-agent boundary is ambiguous")
	}
}

func TestSaltsRandomRefreshedAndDeletedAfter48h(t *testing.T) {
	pool, _ := testpg.New(t)
	ctx := context.Background()
	if err := db.Migrate(ctx, pool); err != nil {
		t.Fatal(err)
	}
	clk := &fakeClock{t: time.Date(2026, 10, 7, 9, 0, 0, 0, time.UTC)}
	st := salts.New(pool, clk)
	if err := st.Refresh(ctx); err != nil {
		t.Fatal(err)
	}
	twoDaysAgo, ok := st.For(time.Date(2026, 10, 5, 0, 0, 0, 0, time.UTC))
	a, ok1 := st.For(time.Date(2026, 10, 7, 0, 0, 0, 0, time.UTC))
	b, ok2 := st.For(time.Date(2026, 10, 8, 0, 0, 0, 0, time.UTC))
	if !ok || len(twoDaysAgo) != 32 || !ok1 || !ok2 || bytes.Equal(a, b) || len(a) != 32 {
		t.Fatal("salts missing, equal or short")
	}
	if err := st.Refresh(ctx); err != nil {
		t.Fatal(err)
	}
	again, ok := st.For(time.Date(2026, 10, 7, 0, 0, 0, 0, time.UTC))
	if !ok || !bytes.Equal(a, again) {
		t.Fatal("refresh replaced an existing salt")
	}

	clk.t = clk.t.Add(5 * 24 * time.Hour)
	n, err := st.DeleteExpired(ctx)
	if err != nil || n == 0 {
		t.Fatalf("deleted=%d err=%v", n, err)
	}
	if _, ok := st.For(time.Date(2026, 10, 7, 0, 0, 0, 0, time.UTC)); ok {
		t.Fatal("expired salt still cached")
	}
}

func TestDeleteExpiredUsesStrictUTCPlus14DayBoundary(t *testing.T) {
	pool, _ := testpg.New(t)
	ctx := context.Background()
	if err := db.Migrate(ctx, pool); err != nil {
		t.Fatal(err)
	}
	clk := &fakeClock{t: time.Date(2026, 10, 7, 12, 0, 0, 0, time.UTC)}
	st := salts.New(pool, clk)
	if err := st.Refresh(ctx); err != nil {
		t.Fatal(err)
	}
	day := time.Date(2026, 10, 7, 0, 0, 0, 0, time.UTC)

	// In UTC+14, October 7 ends at 2026-10-07T10:00Z. At exactly
	// 48 hours after that boundary, the salt is not yet older than 48h.
	clk.t = time.Date(2026, 10, 9, 10, 0, 0, 0, time.UTC)
	if _, err := st.DeleteExpired(ctx); err != nil {
		t.Fatal(err)
	}
	if _, ok := st.For(day); !ok {
		t.Fatal("salt deleted at the 48-hour boundary")
	}

	clk.t = clk.t.Add(time.Nanosecond)
	if _, err := st.DeleteExpired(ctx); err != nil {
		t.Fatal(err)
	}
	if _, ok := st.For(day); ok {
		t.Fatal("salt retained after the 48-hour boundary")
	}
}

func TestRefreshDoesNotRecreateExpiredSalt(t *testing.T) {
	pool, _ := testpg.New(t)
	ctx := context.Background()
	if err := db.Migrate(ctx, pool); err != nil {
		t.Fatal(err)
	}
	clk := &fakeClock{t: time.Date(2026, 10, 7, 9, 59, 0, 0, time.UTC)}
	store := salts.New(pool, clk)
	if err := store.Refresh(ctx); err != nil {
		t.Fatal(err)
	}
	expiredDay := time.Date(2026, 10, 5, 0, 0, 0, 0, time.UTC)

	clk.t = time.Date(2026, 10, 7, 10, 0, 0, 1, time.UTC)
	if _, err := store.DeleteExpired(ctx); err != nil {
		t.Fatal(err)
	}
	if err := store.Refresh(ctx); err != nil {
		t.Fatal(err)
	}

	if _, ok := store.For(expiredDay); ok {
		t.Fatal("refresh restored expired salt to cache")
	}
	var rows int
	if err := pool.QueryRow(ctx, `select count(*) from salts where day = $1`, expiredDay).Scan(&rows); err != nil {
		t.Fatal(err)
	}
	if rows != 0 {
		t.Fatalf("refresh recreated %d expired salt row(s)", rows)
	}
}

func TestSaltForHalfHourZoneUsesLocalDate(t *testing.T) {
	// 2026-10-07T19:00Z is 2026-10-08 00:30 in Asia/Kolkata, so it must
	// use the 2026-10-08 salt.
	kol, err := time.LoadLocation("Asia/Kolkata")
	if err != nil {
		t.Fatal(err)
	}
	day := salts.LocalDay(time.Date(2026, 10, 7, 19, 0, 0, 0, time.UTC), kol)
	if day.Format(time.DateOnly) != "2026-10-08" {
		t.Fatalf("got %s", day)
	}
}

func TestRefreshCoversLateEventTodayMinusTwoInUTCMinus10(t *testing.T) {
	pool, _ := testpg.New(t)
	ctx := context.Background()
	if err := db.Migrate(ctx, pool); err != nil {
		t.Fatal(err)
	}
	now := time.Date(2026, 10, 7, 0, 30, 0, 0, time.UTC)
	store := salts.New(pool, &fakeClock{t: now})
	if err := store.Refresh(ctx); err != nil {
		t.Fatal(err)
	}
	utcMinus10 := time.FixedZone("UTC-10", -10*60*60)
	lateLocalDay := salts.LocalDay(now.Add(-23*time.Hour), utcMinus10)
	if got := lateLocalDay.Format(time.DateOnly); got != "2026-10-05" {
		t.Fatalf("late local day=%s, want 2026-10-05", got)
	}
	if salt, ok := store.For(lateLocalDay); !ok || len(salt) != 32 {
		t.Fatal("late UTC-10 event could not resolve today-2 salt")
	}
}

func TestLocalDayHandlesQuarterHourAndDSTZones(t *testing.T) {
	tests := []struct {
		name string
		ts   time.Time
		zone string
		want string
	}{
		{
			name: "Kathmandu before local midnight",
			ts:   time.Date(2026, 7, 1, 18, 14, 59, 0, time.UTC),
			zone: "Asia/Kathmandu",
			want: "2026-07-01",
		},
		{
			name: "Kathmandu at local midnight",
			ts:   time.Date(2026, 7, 1, 18, 15, 0, 0, time.UTC),
			zone: "Asia/Kathmandu",
			want: "2026-07-02",
		},
		{
			name: "New York spring DST",
			ts:   time.Date(2026, 3, 8, 4, 30, 0, 0, time.UTC),
			zone: "America/New_York",
			want: "2026-03-07",
		},
		{
			name: "New York fall DST",
			ts:   time.Date(2026, 11, 1, 5, 30, 0, 0, time.UTC),
			zone: "America/New_York",
			want: "2026-11-01",
		},
	}
	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			loc, err := time.LoadLocation(tt.zone)
			if err != nil {
				t.Fatal(err)
			}
			if got := salts.LocalDay(tt.ts, loc).Format(time.DateOnly); got != tt.want {
				t.Fatalf("LocalDay(%s, %s) = %s, want %s", tt.ts, tt.zone, got, tt.want)
			}
		})
	}
}
