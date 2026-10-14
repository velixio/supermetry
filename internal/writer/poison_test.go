package writer

import (
	"context"
	"errors"
	"testing"

	"github.com/jackc/pgx/v5/pgconn"
)

func TestWriteErrorClassification(t *testing.T) {
	for _, tc := range []struct {
		code string
		want bool
	}{{"22021", true}, {"23514", true}, {"42P01", true}, {"40001", false}, {"40P01", false}, {"08006", false}, {"57P01", false}} {
		if got := isPermanentWriteError(&pgconn.PgError{Code: tc.code}); got != tc.want {
			t.Errorf("%s permanent=%v want %v", tc.code, got, tc.want)
		}
	}
	if isPermanentWriteError(errors.New("connection reset")) {
		t.Fatal("network error classified permanent")
	}
}

func TestWriteEventsBisectedAroundPoisonRow(t *testing.T) {
	w := New(nil, nil, 3, 0, 0)
	seen := 0
	write := func(_ context.Context, rows []Event) error {
		for _, row := range rows {
			if row.SessionID == 2 {
				return &pgconn.PgError{Code: "22021"}
			}
		}
		seen += len(rows)
		return nil
	}
	before := PoisonDropped.Load()
	if err := w.retryEvents(context.Background(), context.Background(), []Event{{}, {SessionID: 2}, {}}, write); err != nil {
		t.Fatal(err)
	}
	if seen != 2 || PoisonDropped.Load()-before != 1 {
		t.Fatalf("good=%d poisonDropped=%d", seen, PoisonDropped.Load()-before)
	}
}

func TestSanitizeEventNULAndInvalidUTF8(t *testing.T) {
	e := Event{RevenueCurrency: "U\x00SD", Props: map[string]string{"bad\x00key": "a\x00b\xff"}}
	sanitizeEvent(&e)
	if e.RevenueCurrency != "USD" || e.Props["badkey"] != "ab�" {
		t.Fatalf("sanitized event=%+v", e)
	}
}
