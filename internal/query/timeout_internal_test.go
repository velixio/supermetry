package query

import (
	"context"
	"testing"
	"time"

	"github.com/jackc/pgx/v5"

	"supermetry/internal/testpg"
)

func TestEngineQueryTimeoutReachesStatementTimeout(t *testing.T) {
	pool, _ := testpg.New(t)
	engine := NewEngine(pool, nil, nil)
	engine.SetQueryTimeout(2345 * time.Millisecond)
	t.Cleanup(func() { engine.SetQueryTimeout(10 * time.Second) })

	var got string
	err := WithStatementTimeout(context.Background(), pool, queryTimeout, func(tx pgx.Tx) error {
		return tx.QueryRow(context.Background(), "show statement_timeout").Scan(&got)
	})
	if err != nil {
		t.Fatal(err)
	}
	if got != "2345ms" {
		t.Fatalf("statement_timeout = %q, want 2345ms", got)
	}
}
