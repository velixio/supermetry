package otlpingest

import (
	"fmt"
	"strings"
	"testing"
	"time"

	"supermetry/internal/obslogs"
)

func TestLogIssueEventsSeverityGate(t *testing.T) {
	recs := []obslogs.Record{
		{Severity: 13, Body: "disk almost full"},
		{Severity: 17, Body: "order 1001 failed", Service: "api", Release: "api@1.2.0", Environment: "production", TS: testNow},
		{Severity: 21, Body: "panic: nil map"},
		{Severity: 0, Body: "boom", Attributes: map[string]any{"exception.type": "ValueError", "exception.message": "bad id 7"}},
	}
	evs, capped := logIssueEvents(recs, 100)
	if len(evs) != 3 || capped != 0 {
		t.Fatalf("len=%d capped=%d", len(evs), capped)
	}
	e := evs[0]
	if e.Kind != "error" || e.Level != "error" || e.Title != "order 1001 failed" || e.Release != "api@1.2.0" ||
		e.Environment != "production" || e.SDKName != "otlp-logs" || e.Transaction != "api" || !e.Handled || e.Fingerprint == "" {
		t.Fatalf("evs[0]=%+v", e)
	}
	if evs[1].Level != "fatal" {
		t.Fatalf("level %q", evs[1].Level)
	}
	if evs[2].ErrorType != "ValueError" || evs[2].Title != "ValueError: bad id 7" {
		t.Fatalf("evs[2]=%+v", evs[2])
	}
}

func TestLogIssueEventsGroupByTemplate(t *testing.T) {
	evs, _ := logIssueEvents([]obslogs.Record{
		{Severity: 17, Body: "order 1001 failed"},
		{Severity: 17, Body: "order 77 failed"},
	}, 100)
	if len(evs) != 2 || evs[0].Fingerprint != evs[1].Fingerprint {
		t.Fatalf("fingerprints differ: %+v", evs)
	}
}

func TestLogIssueEventsStackFromAttributes(t *testing.T) {
	var lines []string
	for i := 0; i < 80; i++ {
		lines = append(lines, fmt.Sprintf("  at frame%d (file.go:%d)", i, i))
	}
	evs, _ := logIssueEvents([]obslogs.Record{{Severity: 17, Body: "x", Attributes: map[string]any{
		"exception.type": "E", "exception.stacktrace": strings.Join(lines, "\n") + "\n",
	}}}, 100)
	if len(evs) != 1 {
		t.Fatal("no event")
	}
	if n := strings.Count(strings.TrimRight(evs[0].Frames, "\n"), "\n") + 1; n != maxStackLines {
		t.Fatalf("frames has %d lines, want %d", n, maxStackLines)
	}
}

func TestLogIssueEventsStructuredBodyTitle(t *testing.T) {
	evs, _ := logIssueEvents([]obslogs.Record{
		{Severity: 17, Body: `{"k":"v"}`},
		{Severity: 17, Body: ""},
	}, 100)
	if len(evs) != 2 || evs[0].Title != `{"k":"v"}` || evs[1].Title != "Error log" {
		t.Fatalf("%+v", evs)
	}
}

func TestLogIssueEventsCap(t *testing.T) {
	recs := make([]obslogs.Record, 150)
	for i := range recs {
		recs[i] = obslogs.Record{Severity: 17, Body: "same", TS: testNow.Add(time.Duration(i))}
	}
	evs, capped := logIssueEvents(recs, 100)
	if len(evs) != 100 || capped != 50 {
		t.Fatalf("len=%d capped=%d", len(evs), capped)
	}
}

func TestLogIssueEventsScrubs(t *testing.T) {
	evs, _ := logIssueEvents([]obslogs.Record{{Severity: 17, Body: "login failed for 203.0.113.9"}}, 100)
	if len(evs) != 1 || strings.Contains(evs[0].Title, "203.0.113.9") {
		t.Fatalf("%+v", evs)
	}
}

func TestLogIssueEventIDStable(t *testing.T) {
	r := obslogs.Record{Severity: 17, Body: "x", TS: testNow, Service: "s"}
	a, _ := logIssueEvents([]obslogs.Record{r}, 100)
	b, _ := logIssueEvents([]obslogs.Record{r}, 100)
	r2 := r
	r2.TS = testNow.Add(time.Second)
	c, _ := logIssueEvents([]obslogs.Record{r2}, 100)
	if a[0].ID == "" || a[0].ID != b[0].ID || a[0].ID == c[0].ID {
		t.Fatalf("ids %q %q %q", a[0].ID, b[0].ID, c[0].ID)
	}
	tr, sp := make([]byte, 16), make([]byte, 8)
	tr[0], sp[0] = 1, 2
	r3 := obslogs.Record{Severity: 17, Body: "x", TraceID: tr, SpanID: sp, Attributes: map[string]any{"exception.type": "ValueError", "exception.message": "bad id 7"}}
	d, _ := logIssueEvents([]obslogs.Record{r3}, 100)
	if want := exceptionEventID(tr, sp, "ValueError", "bad id 7"); want == "" || d[0].ID != want {
		t.Fatalf("id %q want %q", d[0].ID, want)
	}
	if exceptionEventID(nil, sp, "a", "b") != "" {
		t.Fatal("nil trace must give empty id")
	}
}
