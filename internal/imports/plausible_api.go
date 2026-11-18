package imports

import (
	"bytes"
	"context"
	"encoding/json"
	"fmt"
	"net/http"
	"strings"
	"time"
)

// DefaultPlausibleURL is the hosted Plausible origin used when no base URL is given.
const DefaultPlausibleURL = "https://plausible.io"

// plausiblePageSize is the largest page the Stats API v2 returns (public docs).
const plausiblePageSize = 10000

// plausiblePuller reads daily aggregates from the Plausible Stats API v2
// (POST /api/v2/query, Bearer API key). It maps the same tables the Plausible
// file import fills. Request and response shapes follow the public Stats API
// documentation; see the notes in the report for the parts not verified live.
type plausiblePuller struct {
	client   *pullClient
	base     string
	siteID   string
	key      string
	pageSize int
}

func (p *plausiblePuller) ChunkDays() int { return 31 }

type plausibleQuery struct {
	dim      string // "" for the daily totals
	dimCode  int16
	metrics  []string
	optional bool // an HTTP 400 skips the breakdown instead of failing the pull
	event    bool // goal rows: count events instead of pageviews
}

var plausibleSession = []string{"visitors", "visits", "pageviews", "bounce_rate", "visit_duration"}

var plausibleQueries = []plausibleQuery{
	{dim: "", metrics: plausibleSession},
	{dim: "event:page", dimCode: DimPage, metrics: []string{"visitors", "pageviews"}},
	{dim: "visit:entry_page", dimCode: DimEntry, metrics: []string{"visitors", "visits", "bounce_rate", "visit_duration"}},
	{dim: "visit:exit_page", dimCode: DimExit, metrics: []string{"visitors", "visits"}},
	{dim: "visit:source", dimCode: DimSource, metrics: plausibleSession},
	{dim: "visit:country", dimCode: DimCountry, metrics: plausibleSession},
	{dim: "visit:region_name", dimCode: DimRegion, metrics: plausibleSession, optional: true},
	{dim: "visit:city_name", dimCode: DimCity, metrics: plausibleSession, optional: true},
	{dim: "visit:device", dimCode: DimDevice, metrics: plausibleSession},
	{dim: "visit:browser", dimCode: DimBrowser, metrics: plausibleSession},
	{dim: "visit:os", dimCode: DimOS, metrics: plausibleSession},
	{dim: "event:goal", dimCode: DimEvent, metrics: []string{"visitors", "events"}, optional: true, event: true},
}

type plausibleResponse struct {
	Results []struct {
		Dimensions []any `json:"dimensions"`
		Metrics    []any `json:"metrics"`
	} `json:"results"`
	Error string `json:"error"`
}

func (p *plausiblePuller) Fetch(ctx context.Context, chunk DayRange) (Aggregate, ParseStats, error) {
	result := newAggregate()
	var stats ParseStats
	for _, query := range plausibleQueries {
		err := p.run(ctx, chunk, query, result, &stats)
		if err != nil {
			if query.optional && isRemoteRejection(err) {
				stats.badRow(0, fmt.Errorf("skipped %s: not supported by this Plausible server", query.dim))
				continue
			}
			return result, stats, err
		}
		if err := result.checkLimit(); err != nil {
			return result, stats, err
		}
	}
	return result, stats, nil
}

type remoteRejection struct{ message string }

func (e remoteRejection) Error() string { return e.message }

func isRemoteRejection(err error) bool {
	_, ok := err.(remoteRejection)
	return ok
}

func (p *plausiblePuller) run(ctx context.Context, chunk DayRange, query plausibleQuery, result Aggregate, stats *ParseStats) error {
	dimensions := []string{"time:day"}
	if query.dim != "" {
		dimensions = append(dimensions, query.dim)
	}
	for offset := 0; ; offset += p.pageSize {
		body, err := json.Marshal(map[string]any{
			"site_id":    p.siteID,
			"metrics":    query.metrics,
			"date_range": []string{chunk.From.Format(time.DateOnly), chunk.To.Format(time.DateOnly)},
			"dimensions": dimensions,
			"order_by":   [][]string{{"time:day", "asc"}, {query.metrics[0], "desc"}},
			"pagination": map[string]int{"limit": p.pageSize, "offset": offset},
		})
		if err != nil {
			return err
		}
		status, raw, err := p.client.do(ctx, p.key, func() (*http.Request, error) {
			request, err := http.NewRequest(http.MethodPost, p.base+"/api/v2/query", bytes.NewReader(body))
			if err != nil {
				return nil, err
			}
			request.Header.Set("Authorization", "Bearer "+p.key)
			request.Header.Set("Content-Type", "application/json")
			request.Header.Set("Accept", "application/json")
			return request, nil
		})
		if err != nil {
			return err
		}
		var parsed plausibleResponse
		_ = json.Unmarshal(raw, &parsed)
		switch {
		case status == http.StatusUnauthorized || status == http.StatusForbidden:
			return fmt.Errorf("%w: check the Plausible API key and that it can read %q", errRemoteAuth, p.siteID)
		case status == http.StatusNotFound:
			return fmt.Errorf("Plausible has no API at this address or does not know site %q (HTTP 404)", p.siteID)
		case status == http.StatusBadRequest || status == http.StatusUnprocessableEntity:
			message := parsed.Error
			if message == "" {
				message = snippet(raw, p.key)
			}
			return remoteRejection{fmt.Sprintf("Plausible rejected the query (HTTP %d): %s", status, redact(message, p.key))}
		case status != http.StatusOK:
			return fmt.Errorf("Plausible answered HTTP %d", status)
		}
		for _, row := range parsed.Results {
			if err := p.apply(query, row.Dimensions, row.Metrics, result); err != nil {
				stats.badRow(offset, err)
				continue
			}
			stats.Rows++
		}
		if len(parsed.Results) < p.pageSize {
			return nil
		}
	}
}

func (p *plausiblePuller) apply(query plausibleQuery, dimensions, values []any, result Aggregate) error {
	if len(dimensions) == 0 || len(values) != len(query.metrics) {
		return fmt.Errorf("unexpected row shape")
	}
	day, err := time.Parse(time.DateOnly, fmt.Sprint(dimensions[0]))
	if err != nil {
		return fmt.Errorf("invalid day %v", dimensions[0])
	}
	numbers := make(map[string]float64, len(values))
	for i, name := range query.metrics {
		number, err := plausibleNumber(values[i])
		if err != nil {
			return fmt.Errorf("invalid %s", name)
		}
		numbers[name] = number
	}
	metrics := Metrics{
		Visitors:  int64(numbers["visitors"] + 0.5),
		Visits:    int64(numbers["visits"] + 0.5),
		Pageviews: int64(numbers["pageviews"] + 0.5),
		Events:    int64(numbers["events"] + 0.5),
	}
	// bounce_rate is a percentage and visit_duration an average in seconds;
	// the import tables hold bounced visits and total seconds.
	metrics.Bounces = ratioCount(metrics.Visits, numbers["bounce_rate"])
	metrics.VisitDurationS = totalSeconds(metrics.Visits, numbers["visit_duration"])
	if query.dim == "" {
		result.addDay(day, metrics)
		return nil
	}
	if len(dimensions) < 2 {
		return fmt.Errorf("missing %s value", query.dim)
	}
	value := plausibleValue(query.dimCode, fmt.Sprint(dimensions[1]))
	if query.event {
		metrics = Metrics{Visitors: metrics.Visitors, Events: metrics.Events}
	}
	result.addDimension(DimensionKey{Day: day, Dim: query.dimCode, Value: value}, metrics)
	return nil
}

func plausibleNumber(value any) (float64, error) {
	switch typed := value.(type) {
	case nil:
		return 0, nil
	case float64:
		if typed < 0 || typed > 1e15 {
			return 0, fmt.Errorf("out of range")
		}
		return typed, nil
	}
	return 0, fmt.Errorf("not a number")
}

func plausibleValue(dim int16, value string) string {
	value = strings.TrimSpace(value)
	switch dim {
	case DimPage, DimEntry, DimExit:
		return normalizePagePath(value)
	case DimSource:
		return normalizeSourceName(value)
	case DimCountry:
		if len(value) == 2 {
			return strings.ToUpper(value)
		}
	}
	switch strings.ToLower(value) {
	case "", "(not set)", "(none)":
		return ""
	}
	return value
}
