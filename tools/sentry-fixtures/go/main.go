// Generator for sentry-go fixtures. Cases: captured-exception, recovered-panic.
// This is its own module so the SDK never enters the main go.mod.
package main

import (
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
	default:
		fmt.Fprintln(os.Stderr, "unknown case")
		os.Exit(2)
	}
	sentry.Flush(5 * time.Second)
}
