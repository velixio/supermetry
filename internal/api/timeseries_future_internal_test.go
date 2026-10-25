package api

import (
	"encoding/json"
	"testing"
	"time"
)

func futureRows(times ...string) []map[string]any {
	rows := make([]map[string]any, 0, len(times))
	for _, value := range times {
		rows = append(rows, map[string]any{"time": value, "visitors": float64(0), "pageviews": float64(0)})
	}
	return rows
}

func blankTimes(rows []map[string]any) []string {
	var blank []string
	for _, row := range rows {
		if row["visitors"] == nil && row["pageviews"] == nil {
			blank = append(blank, row["time"].(string))
		}
	}
	return blank
}

func TestBlankFutureBucketsHoursInSiteTimeZone(t *testing.T) {
	kolkata, err := time.LoadLocation("Asia/Kolkata")
	if err != nil {
		t.Fatal(err)
	}
	// 2026-10-26 08:10 UTC is 13:40 in Kolkata (+5:30): the 13:00 bucket is still filling.
	now := time.Date(2026, 10, 26, 8, 10, 0, 0, time.UTC)
	rows := futureRows("2026-10-26T00:00:00", "2026-10-26T12:00:00", "2026-10-26T13:00:00", "2026-10-26T14:00:00", "2026-10-26T23:00:00")
	rows[2]["pageviews"] = float64(3)
	blankFutureBuckets(rows, []Metric{Visitors, Pageviews}, "hour", now, kolkata)
	if got := blankTimes(rows); len(got) != 2 || got[0] != "2026-10-26T14:00:00" || got[1] != "2026-10-26T23:00:00" {
		t.Fatalf("blank buckets = %v, want 14:00 and 23:00", got)
	}
	// Closed buckets without data stay 0; the current bucket keeps its value.
	if rows[0]["visitors"] != float64(0) || rows[2]["pageviews"] != float64(3) {
		t.Fatalf("past or current bucket changed: %v %v", rows[0], rows[2])
	}
	// The same instant in UTC is still 08:00, so every afternoon hour is future there.
	utcRows := futureRows("2026-10-26T08:00:00", "2026-10-26T09:00:00")
	blankFutureBuckets(utcRows, []Metric{Visitors, Pageviews}, "hour", now, time.UTC)
	if got := blankTimes(utcRows); len(got) != 1 || got[0] != "2026-10-26T09:00:00" {
		t.Fatalf("UTC blank buckets = %v", got)
	}
}

func TestBlankFutureBucketsDaysWeeksMonths(t *testing.T) {
	kathmandu, err := time.LoadLocation("Asia/Kathmandu")
	if err != nil {
		t.Fatal(err)
	}
	// 2026-10-25 18:30 UTC is already 2026-10-26 00:15 in Kathmandu (+5:45).
	now := time.Date(2026, 10, 25, 18, 30, 0, 0, time.UTC)
	days := futureRows("2026-10-25", "2026-10-26", "2026-10-27")
	blankFutureBuckets(days, []Metric{Visitors, Pageviews}, "day", now, kathmandu)
	if got := blankTimes(days); len(got) != 1 || got[0] != "2026-10-27" {
		t.Fatalf("day blanks = %v", got)
	}
	weeks := futureRows("2026-10-19", "2026-10-26", "2026-11-02")
	blankFutureBuckets(weeks, []Metric{Visitors, Pageviews}, "week", now, kathmandu)
	if got := blankTimes(weeks); len(got) != 1 || got[0] != "2026-11-02" {
		t.Fatalf("week blanks = %v", got)
	}
	months := futureRows("2026-09-01", "2026-10-01", "2026-11-01")
	blankFutureBuckets(months, []Metric{Visitors, Pageviews}, "month", now, kathmandu)
	if got := blankTimes(months); len(got) != 1 || got[0] != "2026-11-01" {
		t.Fatalf("month blanks = %v", got)
	}
	// A future bucket that holds data (clock skew) keeps it.
	skewed := futureRows("2026-10-27")
	skewed[0]["visitors"] = float64(2)
	blankFutureBuckets(skewed, []Metric{Visitors, Pageviews}, "day", now, kathmandu)
	if skewed[0]["visitors"] != float64(2) {
		t.Fatalf("future bucket with data was blanked: %v", skewed[0])
	}
	encoded, err := json.Marshal(months[2])
	if err != nil || string(encoded) != `{"pageviews":null,"time":"2026-11-01","visitors":null}` {
		t.Fatalf("future bucket JSON = %s, %v", encoded, err)
	}
}
