package appquery_test

import (
	"context"
	"testing"
)

// The event detail carries the trace id so the dashboard can link to the trace;
// an event without a trace has none.
func TestEventExposesItsTraceID(t *testing.T) {
	e := perfSetup(t)
	ctx := context.Background()
	trace := make([]byte, 16)
	trace[15] = 0x7f
	for id, tr := range map[string][]byte{"with-trace": trace, "no-trace": nil} {
		if _, err := e.pool.Exec(ctx, `insert into app_events(site_id,ts,received_at,event_id,kind,trace_id) values (7,$1,$1,$2,'error',$3)`, at(10, 0, 0), id, tr); err != nil {
			t.Fatal(err)
		}
		if _, err := e.pool.Exec(ctx, `insert into app_event_bodies(site_id,ts,event_id,body) values (7,$1,$2,'{}')`, at(10, 0, 0), id); err != nil {
			t.Fatal(err)
		}
	}
	got, err := e.q.Event(ctx, e.scope, "with-trace")
	if err != nil || got.TraceID != "000000000000000000000000000000"+"7f" {
		t.Fatalf("with trace = %+v %v", got, err)
	}
	if got, err = e.q.Event(ctx, e.scope, "no-trace"); err != nil || got.TraceID != "" {
		t.Fatalf("without trace = %+v %v", got, err)
	}
}
