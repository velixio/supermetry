package main

import (
	"encoding/json"
	"fmt"
	"io"
	"math"
	"slices"
	"sort"
	"strings"
	"time"
)

type Result struct {
	Route       string         `json:"route"`
	Method      string         `json:"method"`
	Request     string         `json:"request_set,omitempty"`
	Params      map[string]any `json:"params,omitempty"`
	Dataset     string         `json:"dataset"`
	Concurrency int            `json:"concurrency"`
	ColdMS      float64        `json:"cold_ms"`
	P50MS       float64        `json:"p50_ms"`
	P95MS       float64        `json:"p95_ms"`
	P99MS       float64        `json:"p99_ms"`
	RPS         float64        `json:"rps"`
	Bytes       int64          `json:"bytes"`
	Status      int            `json:"status"`
	DBQueries   float64        `json:"db_queries"`
	DBMS        float64        `json:"db_ms"`
	Allocs      float64        `json:"allocs"`
	Errors      int64          `json:"errors"`
}

type Report struct {
	Commit          string   `json:"commit"`
	GoVersion       string   `json:"go_version"`
	PostgresVersion string   `json:"postgres_version"`
	Machine         string   `json:"machine"`
	Tier            string   `json:"tier"`
	Seed            uint64   `json:"seed"`
	GeneratedAt     string   `json:"generated_at"`
	Results         []Result `json:"results"`
}

func percentile(values []time.Duration, p float64) time.Duration {
	if len(values) == 0 {
		return 0
	}
	ordered := slices.Clone(values)
	slices.Sort(ordered)
	rank := int(math.Ceil(p*float64(len(ordered)))) - 1
	rank = max(0, min(rank, len(ordered)-1))
	return ordered[rank]
}

type Regression struct {
	Key        string  `json:"key"`
	BaselineMS float64 `json:"baseline_ms"`
	CurrentMS  float64 `json:"current_ms"`
	Change     float64 `json:"change"`
	Missing    bool    `json:"missing,omitempty"`
}

func resultKey(result Result) string {
	params, _ := json.Marshal(result.Params)
	return fmt.Sprintf("%s|%s|%s|%s|c%d", result.Method, result.Route, result.Request, params, result.Concurrency)
}

func compareResults(baseline, current []Result, tolerance float64) []Regression {
	byKey := make(map[string]Result, len(current))
	for _, result := range current {
		byKey[resultKey(result)] = result
	}
	var regressions []Regression
	for _, previous := range baseline {
		key := resultKey(previous)
		now, ok := byKey[key]
		if !ok {
			regressions = append(regressions, Regression{Key: key, BaselineMS: previous.P95MS, Missing: true})
			continue
		}
		if previous.P95MS <= 0 {
			continue
		}
		change := (now.P95MS - previous.P95MS) / previous.P95MS
		if change > tolerance {
			regressions = append(regressions, Regression{Key: key, BaselineMS: previous.P95MS, CurrentMS: now.P95MS, Change: change})
		}
	}
	sort.Slice(regressions, func(i, j int) bool { return regressions[i].Key < regressions[j].Key })
	return regressions
}

func writeJSON(w io.Writer, report Report) error {
	encoder := json.NewEncoder(w)
	encoder.SetIndent("", "  ")
	return encoder.Encode(report)
}

func writeMarkdown(w io.Writer, report Report) error {
	rows := slices.Clone(report.Results)
	sort.SliceStable(rows, func(i, j int) bool { return rows[i].P95MS > rows[j].P95MS })
	_, err := fmt.Fprintf(w, "# API benchmark\n\nCommit: `%s`  \nGo: `%s`  \nPostgreSQL: `%s`  \nMachine: `%s`  \nTier: `%s`  \nSeed: `%d`  \nGenerated: `%s`\n\n", escapeCell(report.Commit), escapeCell(report.GoVersion), escapeCell(report.PostgresVersion), escapeCell(report.Machine), escapeCell(report.Tier), report.Seed, escapeCell(report.GeneratedAt))
	if err != nil {
		return err
	}
	if _, err := fmt.Fprintln(w, "| Route | Set | C | Cold ms | p50 | p95 | p99 | req/s | bytes | status | DB q | DB ms | errors |\n|---|---|---:|---:|---:|---:|---:|---:|---:|---:|---:|---:|---:|"); err != nil {
		return err
	}
	for _, row := range rows {
		if _, err := fmt.Fprintf(w, "| %s | %s | %d | %.2f | %.2f | %.2f | %.2f | %.1f | %d | %d | %.2f | %.2f | %d |\n", escapeCell(row.Method+" "+row.Route), escapeCell(row.Request), row.Concurrency, row.ColdMS, row.P50MS, row.P95MS, row.P99MS, row.RPS, row.Bytes, row.Status, row.DBQueries, row.DBMS, row.Errors); err != nil {
			return err
		}
	}
	return nil
}

func escapeCell(value string) string { return strings.ReplaceAll(value, "|", "\\|") }
