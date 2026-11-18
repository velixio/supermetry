// Generator for sentry-go fixtures. Cases: captured-exception, recovered-panic.
// This is its own module so the SDK never enters the main go.mod.
package main

import (
	"context"
	"errors"
	"fmt"
	"os"
	"time"

	"github.com/getsentry/sentry-go"
)

func main() {
	if len(os.Args) < 2 {
		fmt.Fprintln(os.Stderr, "usage: go run . <case>")
		os.Exit(2)
	}
	err := sentry.Init(sentry.ClientOptions{
		Dsn:         os.Getenv("SENTRY_FIXTURE_DSN"),
		Release:     "fixture@1.0.0",
		Environment: "fixture",
		ServerName:  "fixture-host",
		// tracing only for the transaction case (EnableTracing is how this SDK turns it on)
		EnableTracing:    os.Args[1] == "transaction",
		TracesSampleRate: tracesRate(os.Args[1]),
	})
	if err != nil {
		fmt.Fprintln(os.Stderr, err)
		os.Exit(1)
	}
	switch os.Args[1] {
	case "captured-exception":
		// the SDK's explicit API is the only way Go reports an error
		sentry.CaptureException(errors.New("fixture captured exception"))
	case "recovered-panic":
		func() {
			defer sentry.Recover()
			var m map[string]int
			m["x"] = 1 // nil map write panics; sentry.Recover reports it
		}()
	case "transaction":
		root := sentry.StartSpan(context.Background(), "http.server", sentry.WithTransactionName("GET /api/items"), sentry.WithTransactionSource(sentry.SourceRoute))
		db := root.StartChild("db.query")
		db.Description = "select * from items where owner = 'fixture' and id = 42"
		time.Sleep(15 * time.Millisecond)
		db.Finish()
		web := root.StartChild("http.client")
		web.Description = "GET https://api.example.com/v1/items/12345?token=fixture-token"
		time.Sleep(10 * time.Millisecond)
		web.Finish()
		root.Finish()
	default:
		fmt.Fprintln(os.Stderr, "unknown case")
		os.Exit(2)
	}
	sentry.Flush(5 * time.Second)
}

func tracesRate(c string) float64 {
	if c == "transaction" {
		return 1
	}
	return 0
}
