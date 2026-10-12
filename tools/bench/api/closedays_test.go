package main

import (
	"reflect"
	"testing"
	"time"
)

func civil(value string) time.Time {
	day, err := time.Parse(time.DateOnly, value)
	if err != nil {
		panic(err)
	}
	return day
}

func TestClosableDays(t *testing.T) {
	tests := []struct {
		name               string
		first, last, today string
		want               []string
	}{
		{"every loaded day is complete", "2026-09-30", "2026-10-02", "2026-10-12", []string{"2026-09-30", "2026-10-01", "2026-10-02"}},
		{"today stays open", "2026-10-10", "2026-10-12", "2026-10-12", []string{"2026-10-10", "2026-10-11"}},
		{"data only today", "2026-10-12", "2026-10-12", "2026-10-12", nil},
		{"today before the data", "2026-10-12", "2026-10-14", "2026-10-11", nil},
		{"first after last", "2026-10-05", "2026-10-01", "2026-10-12", nil},
		{"single day", "2026-10-01", "2026-10-01", "2026-10-12", []string{"2026-10-01"}},
	}
	for _, test := range tests {
		t.Run(test.name, func(t *testing.T) {
			var want []time.Time
			for _, value := range test.want {
				want = append(want, civil(value))
			}
			got := closableDays(civil(test.first), civil(test.last), civil(test.today))
			if !reflect.DeepEqual(got, want) {
				t.Fatalf("closableDays = %v, want %v", got, want)
			}
		})
	}
}

func TestCivilTodayUsesSiteTimeZone(t *testing.T) {
	now := time.Date(2026, 10, 12, 20, 0, 0, 0, time.UTC)
	tests := []struct {
		zone string
		want string
	}{
		{"UTC", "2026-10-12"},
		{"Asia/Kolkata", "2026-10-13"},
		{"Asia/Kathmandu", "2026-10-13"},
		{"America/Los_Angeles", "2026-10-12"},
	}
	for _, test := range tests {
		got, err := civilToday(now, test.zone)
		if err != nil {
			t.Fatal(err)
		}
		if !got.Equal(civil(test.want)) {
			t.Fatalf("civilToday(%s) = %s, want %s", test.zone, got.Format(time.DateOnly), test.want)
		}
	}
	if _, err := civilToday(now, "Not/AZone"); err == nil {
		t.Fatal("unknown zone must fail")
	}
}
