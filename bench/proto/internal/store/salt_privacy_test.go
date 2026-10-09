package store_test

import (
	"bytes"
	"context"
	"testing"
	"time"

	"supermetry/bench/proto/internal/core"
	"supermetry/bench/proto/internal/testdb"
)

func TestPurgeSaltsBeforeUsesExplicitCutoff(t *testing.T) {
	ctx := context.Background()
	st := testdb.Open(t)
	if _, err := st.Pool.Exec(ctx, `INSERT INTO sites (id, host, tz) VALUES (1, 'salt.test', 'UTC')`); err != nil {
		t.Fatal(err)
	}
	fakeNow := time.Date(2026, 7, 3, 12, 0, 0, 0, time.UTC)
	rows := []struct {
		day     string
		created time.Time
	}{
		{"2026-06-30", fakeNow.Add(-48*time.Hour - time.Nanosecond)},
		{"2026-07-01", fakeNow.Add(-48 * time.Hour)},
		{"2026-07-02", fakeNow.Add(-47 * time.Hour)},
	}
	for _, row := range rows {
		if _, err := st.Pool.Exec(ctx, `INSERT INTO salts (site_id, day, salt, created_at) VALUES (1, $1, $2, $3)`,
			row.day, bytes.Repeat([]byte{1}, core.SaltLen), row.created); err != nil {
			t.Fatal(err)
		}
	}

	n, err := st.PurgeSaltsBefore(ctx, fakeNow.Add(-48*time.Hour))
	if err != nil {
		t.Fatal(err)
	}
	if n != 1 {
		t.Fatalf("purged %d salts, want 1", n)
	}
	var days []time.Time
	rowsOut, err := st.Pool.Query(ctx, `SELECT day FROM salts ORDER BY day`)
	if err != nil {
		t.Fatal(err)
	}
	defer rowsOut.Close()
	for rowsOut.Next() {
		var day time.Time
		if err := rowsOut.Scan(&day); err != nil {
			t.Fatal(err)
		}
		days = append(days, day)
	}
	if err := rowsOut.Err(); err != nil {
		t.Fatal(err)
	}
	if len(days) != 2 || days[0].Format(time.DateOnly) != "2026-07-01" || days[1].Format(time.DateOnly) != "2026-07-02" {
		t.Fatalf("remaining salt days = %v", days)
	}
}
