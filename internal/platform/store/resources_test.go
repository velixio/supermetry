package store

import (
	"context"
	"encoding/json"
	"reflect"
	"testing"
	"time"

	"supermetry/internal/db"
	"supermetry/internal/testpg"
)

func TestInsertResourcesRoundTrip(t *testing.T) {
	pool, _ := testpg.New(t)
	ctx := context.Background()
	if err := db.Migrate(ctx, pool); err != nil {
		t.Fatal(err)
	}
	var userID, orgID int64
	var siteID int32
	if err := pool.QueryRow(ctx, `insert into users(email,name,password_hash) values ('resources@example.com','Owner','hash') returning id`).Scan(&userID); err != nil {
		t.Fatal(err)
	}
	if err := pool.QueryRow(ctx, `insert into orgs(name,slug) values ('Resources','resources') returning id`).Scan(&orgID); err != nil {
		t.Fatal(err)
	}
	if err := pool.QueryRow(ctx, `insert into sites(org_id,public_id,name,timezone,retention_class) values ($1,'resources001','Resources','UTC',30) returning id`, orgID).Scan(&siteID); err != nil {
		t.Fatal(err)
	}

	threshold := int16(75)
	goalID, err := InsertGoal(ctx, pool, siteID, GoalRow{
		Kind: "scroll", Match: "wildcard", Value: "/blog/*", Name: "Deep read", ScrollThreshold: &threshold,
	})
	if err != nil {
		t.Fatal(err)
	}
	steps := json.RawMessage(`[{"type":"page","match":"exact","value":"/"},{"type":"event","match":"exact","value":"Signup"}]`)
	funnelID, err := InsertFunnel(ctx, pool, siteID, "Signup flow", steps)
	if err != nil {
		t.Fatal(err)
	}
	day := time.Date(2026, 10, 8, 0, 0, 0, 0, time.UTC)
	annotationID, err := InsertAnnotation(ctx, pool, siteID, day, "Campaign launched", userID)
	if err != nil {
		t.Fatal(err)
	}
	filters := json.RawMessage(`[{"dimension":"device","operator":"is","values":["Mobile"]}]`)
	segmentID, err := InsertSegment(ctx, pool, siteID, "Mobile visitors", filters, userID)
	if err != nil {
		t.Fatal(err)
	}

	var kind, match, value, name string
	var gotThreshold *int16
	if err := pool.QueryRow(ctx, `select kind,match,value,name,scroll_threshold from goals where id=$1 and site_id=$2`, goalID, siteID).
		Scan(&kind, &match, &value, &name, &gotThreshold); err != nil {
		t.Fatal(err)
	}
	if kind != "scroll" || match != "wildcard" || value != "/blog/*" || name != "Deep read" || gotThreshold == nil || *gotThreshold != threshold {
		t.Fatalf("goal round trip = %q/%q/%q/%q/%v", kind, match, value, name, gotThreshold)
	}
	var gotSteps, gotFilters []byte
	if err := pool.QueryRow(ctx, `select steps from funnels where id=$1 and site_id=$2`, funnelID, siteID).Scan(&gotSteps); err != nil {
		t.Fatal(err)
	}
	if !sameJSON(gotSteps, steps) {
		t.Fatalf("funnel steps = %s, want %s", gotSteps, steps)
	}
	var gotDay time.Time
	var gotText string
	var annotationAuthor int64
	if err := pool.QueryRow(ctx, `select day,text,created_by from annotations where id=$1 and site_id=$2`, annotationID, siteID).
		Scan(&gotDay, &gotText, &annotationAuthor); err != nil {
		t.Fatal(err)
	}
	if gotDay.Format(time.DateOnly) != "2026-10-08" || gotText != "Campaign launched" || annotationAuthor != userID {
		t.Fatalf("annotation round trip = %s/%q/%d", gotDay, gotText, annotationAuthor)
	}
	var gotSegmentName string
	var segmentAuthor int64
	if err := pool.QueryRow(ctx, `select name,filters,created_by from segments where id=$1 and site_id=$2`, segmentID, siteID).
		Scan(&gotSegmentName, &gotFilters, &segmentAuthor); err != nil {
		t.Fatal(err)
	}
	if gotSegmentName != "Mobile visitors" || !sameJSON(gotFilters, filters) || segmentAuthor != userID {
		t.Fatalf("segment round trip = %q/%s/%d", gotSegmentName, gotFilters, segmentAuthor)
	}
}

func sameJSON(left, right []byte) bool {
	var decodedLeft, decodedRight any
	return json.Unmarshal(left, &decodedLeft) == nil && json.Unmarshal(right, &decodedRight) == nil && reflect.DeepEqual(decodedLeft, decodedRight)
}
