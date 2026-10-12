package main

import (
	"testing"
	"time"
)

func TestPercentile(t *testing.T) {
	t.Parallel()
	tests := []struct {
		name string
		in   []time.Duration
		p    float64
		want time.Duration
	}{
		{name: "empty", p: 0.95, want: 0},
		{name: "single", in: []time.Duration{7 * time.Millisecond}, p: 0.95, want: 7 * time.Millisecond},
		{name: "nearest rank median", in: []time.Duration{4 * time.Millisecond, time.Millisecond, 3 * time.Millisecond, 2 * time.Millisecond}, p: 0.50, want: 2 * time.Millisecond},
		{name: "nearest rank p95", in: []time.Duration{10, 20, 30, 40, 50, 60, 70, 80, 90, 100}, p: 0.95, want: 100},
	}
	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			t.Parallel()
			if got := percentile(tt.in, tt.p); got != tt.want {
				t.Fatalf("percentile(%v, %v) = %v, want %v", tt.in, tt.p, got, tt.want)
			}
		})
	}
}

func TestCompareResults(t *testing.T) {
	t.Parallel()
	tests := []struct {
		name     string
		baseline []Result
		current  []Result
		want     int
	}{
		{name: "regression", baseline: []Result{{Route: "GET /stats", Dataset: "S", Concurrency: 1, P95MS: 100}}, current: []Result{{Route: "GET /stats", Dataset: "S", Concurrency: 1, P95MS: 126}}, want: 1},
		{name: "improvement", baseline: []Result{{Route: "GET /stats", Dataset: "S", Concurrency: 8, P95MS: 100}}, current: []Result{{Route: "GET /stats", Dataset: "S", Concurrency: 8, P95MS: 60}}},
		{name: "missing route", baseline: []Result{{Route: "GET /stats", Dataset: "S", Concurrency: 1, P95MS: 100}}, current: nil, want: 1},
	}
	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			t.Parallel()
			if got := compareResults(tt.baseline, tt.current, 0.25); len(got) != tt.want {
				t.Fatalf("len(compareResults()) = %d, want %d; regressions=%v", len(got), tt.want, got)
			}
		})
	}
}
