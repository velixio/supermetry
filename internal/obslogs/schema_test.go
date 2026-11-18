package obslogs_test

import (
	"context"
	"strings"
	"testing"
	"time"

	"supermetry/internal/obslogs"
)

// The table must have no place to put a raw IP, email or user id: identity is
// the keyed user_hash only (spec 4.3).
func TestLogsSchemaHasNoIdentityColumns(t *testing.T) {
	pool := fixture(t)
	rows, err := pool.Query(context.Background(), `select column_name from information_schema.columns where table_name = 'logs' and table_schema = current_schema()`)
	if err != nil {
		t.Fatal(err)
	}
	defer rows.Close()
	n := 0
	for rows.Next() {
		var name string
		if err := rows.Scan(&name); err != nil {
			t.Fatal(err)
		}
		n++
		for _, bad := range []string{"ip", "email", "user_id", "address"} {
			if strings.Contains(name, bad) {
				t.Fatalf("logs has identity column %q", name)
			}
		}
	}
	if n != 15 {
		t.Fatalf("logs has %d columns, want 15", n)
	}
}

// A stored row keeps only what the front door passed: no hidden enrichment.
func TestWriterStoresUserHashNotUserID(t *testing.T) {
	pool := fixture(t)
	w := obslogs.NewWriter(pool, obslogs.WriterConfig{Flush: time.Hour, Now: func() time.Time { return now }}, nil)
	r := rec(0, "login")
	r.UserHash = []byte{0xca, 0xfe}
	r.Attributes = map[string]any{"http.route": "/login"}
	w.Enqueue([]obslogs.Record{r})
	w.Close()
	var dump string
	if err := pool.QueryRow(context.Background(), `select l::text from logs l`).Scan(&dump); err != nil {
		t.Fatal(err)
	}
	for _, leak := range []string{"@", "203.0.113", "user.id"} {
		if strings.Contains(dump, leak) {
			t.Fatalf("row text contains %q: %s", leak, dump)
		}
	}
}
