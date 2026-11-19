package appquery_test

import (
	"context"
	"testing"
)

func TestIssueDetailLatestEventsCarrySDKName(t *testing.T) {
	e := setup(t)
	ctx := context.Background()
	issue := e.f.Issues["I2"]
	if _, err := e.f.Pool.Exec(ctx, `update app_events set sdk_name='otlp-logs' where site_id=$1 and issue_id=$2 and ts=(select max(ts) from app_events where site_id=$1 and issue_id=$2)`, e.f.SiteID, issue); err != nil {
		t.Fatal(err)
	}
	det, err := e.q.Issue(ctx, e.scope, issue, full())
	if err != nil {
		t.Fatal(err)
	}
	if det.LatestEvents[0].SDKName != "otlp-logs" || det.LatestEvents[1].SDKName != "" {
		t.Fatalf("sdk names %q %q", det.LatestEvents[0].SDKName, det.LatestEvents[1].SDKName)
	}
}
