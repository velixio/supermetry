package imports

import (
	"bytes"
	"context"
	"encoding/csv"
	"encoding/json"
	"fmt"
	"net/http"
	"regexp"
	"strconv"
	"strings"
	"sync"
	"time"
)

// GA4Config configures the Google Analytics Data API pull. It is read from
// SUPERMETRY_GA4_OAUTH_CLIENT_ID / _SECRET; while either is empty the GA4
// endpoints answer "not configured". The URL fields exist for tests.
type GA4Config struct {
	ClientID     string
	ClientSecret string
	// BaseURL is the public origin of this installation; the redirect URI
	// is derived from it and is the only one ever sent to Google.
	BaseURL   string
	AuthURL   string
	TokenURL  string
	RevokeURL string
	DataBase  string
	AdminBase string
	// PageSize is the runReport page size (Google allows up to 250000).
	PageSize int
}

const (
	ga4Scope     = "https://www.googleapis.com/auth/analytics.readonly"
	ga4PageSize  = 100000
	ga4Redirect  = "/api/private/imports/ga4/callback"
	ga4TokenSkew = time.Minute
)

// Configured reports whether OAuth client credentials and a base URL exist.
func (c GA4Config) Configured() bool {
	return c.ClientID != "" && c.ClientSecret != "" && c.BaseURL != ""
}

// RedirectURI is the strict OAuth callback address.
func (c GA4Config) RedirectURI() string { return strings.TrimRight(c.BaseURL, "/") + ga4Redirect }

func (c GA4Config) withDefaults() GA4Config {
	if c.AuthURL == "" {
		c.AuthURL = "https://accounts.google.com/o/oauth2/v2/auth"
	}
	if c.TokenURL == "" {
		c.TokenURL = "https://oauth2.googleapis.com/token"
	}
	if c.RevokeURL == "" {
		c.RevokeURL = "https://oauth2.googleapis.com/revoke"
	}
	if c.DataBase == "" {
		c.DataBase = "https://analyticsdata.googleapis.com"
	}
	if c.AdminBase == "" {
		c.AdminBase = "https://analyticsadmin.googleapis.com"
	}
	if c.PageSize <= 0 {
		c.PageSize = ga4PageSize
	}
	return c
}

var ga4PropertyPattern = regexp.MustCompile(`^properties/[0-9]{1,20}$`)

// ga4AccessToken hands out a valid access token, refreshing it from the
// stored refresh token when it is missing or about to expire.
type ga4AccessToken struct {
	mu      sync.Mutex
	refresh func(context.Context) (string, time.Duration, error)
	token   string
	expiry  time.Time
	now     func() time.Time
}

func (t *ga4AccessToken) get(ctx context.Context, force bool) (string, error) {
	t.mu.Lock()
	defer t.mu.Unlock()
	if !force && t.token != "" && t.now().Add(ga4TokenSkew).Before(t.expiry) {
		return t.token, nil
	}
	token, lifetime, err := t.refresh(ctx)
	if err != nil {
		return "", err
	}
	t.token, t.expiry = token, t.now().Add(lifetime)
	return token, nil
}

// ga4Report is one runReport shape. Each report fills one family of tables,
// mirroring the CSV reports the GA4 file import accepts, so the parsing and
// mapping rules (session scoping, direct traffic, country codes...) are the
// ones ParseGA4 already applies.
type ga4Report struct {
	dimension string
	metrics   []string
	totals    bool // the daily totals report feeds Days; others feed breakdowns
}

var ga4Labels = map[string]string{
	"date": "Date", "pagePathPlusQueryString": "Page path + query string",
	"landingPagePlusQueryString": "Landing page + query string", "sessionSourceMedium": "Session source / medium",
	"country": "Country", "region": "Region", "city": "City", "deviceCategory": "Device category",
	"browser": "Browser", "operatingSystem": "Operating system", "eventName": "Event name",
	"activeUsers": "Active users", "sessions": "Sessions", "screenPageViews": "Views",
	"bounceRate": "Bounce rate", "averageSessionDuration": "Average session duration", "eventCount": "Event count",
}

var ga4SessionMetrics = []string{"activeUsers", "sessions", "screenPageViews", "bounceRate", "averageSessionDuration"}

var ga4Reports = []ga4Report{
	{dimension: "", metrics: ga4SessionMetrics, totals: true},
	{dimension: "pagePathPlusQueryString", metrics: ga4SessionMetrics},
	{dimension: "landingPagePlusQueryString", metrics: ga4SessionMetrics},
	{dimension: "sessionSourceMedium", metrics: ga4SessionMetrics},
	{dimension: "country", metrics: ga4SessionMetrics},
	{dimension: "region", metrics: ga4SessionMetrics},
	{dimension: "city", metrics: ga4SessionMetrics},
	{dimension: "deviceCategory", metrics: ga4SessionMetrics},
	{dimension: "browser", metrics: ga4SessionMetrics},
	{dimension: "operatingSystem", metrics: ga4SessionMetrics},
	{dimension: "eventName", metrics: []string{"activeUsers", "eventCount"}},
}

type ga4Puller struct {
	client   *pullClient
	cfg      GA4Config
	property string
	token    *ga4AccessToken
}

func (p *ga4Puller) ChunkDays() int { return 31 }

type ga4Response struct {
	Rows []struct {
		DimensionValues []struct {
			Value string `json:"value"`
		} `json:"dimensionValues"`
		MetricValues []struct {
			Value string `json:"value"`
		} `json:"metricValues"`
	} `json:"rows"`
	RowCount int `json:"rowCount"`
	Error    struct {
		Message string `json:"message"`
	} `json:"error"`
}

func (p *ga4Puller) Fetch(ctx context.Context, chunk DayRange) (Aggregate, ParseStats, error) {
	result := newAggregate()
	var stats ParseStats
	for _, report := range ga4Reports {
		table, rows, err := p.runReport(ctx, chunk, report)
		if err != nil {
			return result, stats, err
		}
		if rows == 0 {
			continue
		}
		parsed, parseStats, err := parseGA4(ctx, bytes.NewReader(table))
		if err != nil {
			return result, stats, fmt.Errorf("map GA4 %s report: %w", report.dimension, err)
		}
		stats.Rows += parseStats.Rows
		stats.BadRows += parseStats.BadRows
		stats.DuplicateRows += parseStats.DuplicateRows
		for _, rowError := range parseStats.Errors {
			if len(stats.Errors) < maxRecordedErrors {
				stats.Errors = append(stats.Errors, rowError)
			}
		}
		if report.totals {
			mergeInto(result, parsed)
		} else {
			// Breakdown reports repeat each day's pageviews in their day rows;
			// only the totals report feeds Days so nothing is counted twice.
			mergeInto(result, Aggregate{Days: map[time.Time]Metrics{}, Dimensions: parsed.Dimensions})
		}
		if err := result.checkLimit(); err != nil {
			return result, stats, err
		}
	}
	return result, stats, nil
}

// runReport pages through one report and renders it as the CSV table
// ParseGA4 reads. It returns the table and the number of data rows.
func (p *ga4Puller) runReport(ctx context.Context, chunk DayRange, report ga4Report) ([]byte, int, error) {
	dimensions := []map[string]string{{"name": "date"}}
	names := []string{"date"}
	if report.dimension != "" {
		dimensions = append(dimensions, map[string]string{"name": report.dimension})
		names = append(names, report.dimension)
	}
	metrics := make([]map[string]string, len(report.metrics))
	for i, name := range report.metrics {
		metrics[i] = map[string]string{"name": name}
	}
	var buffer bytes.Buffer
	writer := csv.NewWriter(&buffer)
	header := make([]string, 0, len(names)+len(report.metrics))
	for _, name := range append(append([]string{}, names...), report.metrics...) {
		header = append(header, ga4Labels[name])
	}
	_ = writer.Write(header)
	total := 0
	for offset := 0; ; {
		body, err := json.Marshal(map[string]any{
			"dateRanges": []map[string]string{{"startDate": chunk.From.Format(time.DateOnly), "endDate": chunk.To.Format(time.DateOnly)}},
			"dimensions": dimensions, "metrics": metrics,
			"limit": strconv.Itoa(p.cfg.PageSize), "offset": strconv.Itoa(offset),
			"keepEmptyRows": false,
		})
		if err != nil {
			return nil, 0, err
		}
		page, err := p.post(ctx, body)
		if err != nil {
			return nil, 0, err
		}
		for _, row := range page.Rows {
			if len(row.DimensionValues) != len(names) || len(row.MetricValues) != len(report.metrics) {
				return nil, 0, fmt.Errorf("GA4 returned a row with an unexpected shape")
			}
			record := make([]string, 0, len(header))
			for _, value := range row.DimensionValues {
				record = append(record, value.Value)
			}
			for _, value := range row.MetricValues {
				record = append(record, value.Value)
			}
			_ = writer.Write(record)
		}
		total += len(page.Rows)
		offset += len(page.Rows)
		if total > maxGA4Rows {
			return nil, 0, fatalError{fmt.Errorf("GA4 report has more than %d rows", maxGA4Rows)}
		}
		if len(page.Rows) == 0 || offset >= page.RowCount {
			break
		}
	}
	writer.Flush()
	return buffer.Bytes(), total, writer.Error()
}

func (p *ga4Puller) post(ctx context.Context, body []byte) (ga4Response, error) {
	endpoint := strings.TrimRight(p.cfg.DataBase, "/") + "/v1beta/" + p.property + ":runReport"
	refreshed := false
	for {
		access, err := p.token.get(ctx, false)
		if err != nil {
			return ga4Response{}, err
		}
		status, raw, err := p.client.do(ctx, access, func() (*http.Request, error) {
			request, err := http.NewRequest(http.MethodPost, endpoint, bytes.NewReader(body))
			if err != nil {
				return nil, err
			}
			request.Header.Set("Authorization", "Bearer "+access)
			request.Header.Set("Content-Type", "application/json")
			return request, nil
		})
		if err != nil {
			return ga4Response{}, err
		}
		var parsed ga4Response
		_ = json.Unmarshal(raw, &parsed)
		switch {
		case status == http.StatusUnauthorized && !refreshed:
			refreshed = true
			if _, err := p.token.get(ctx, true); err != nil {
				return ga4Response{}, err
			}
			continue
		case status == http.StatusUnauthorized || status == http.StatusForbidden:
			return ga4Response{}, fmt.Errorf("%w: the Google account cannot read this property; reconnect Google Analytics", errRemoteAuth)
		case status == http.StatusBadRequest || status == http.StatusNotFound:
			return ga4Response{}, remoteRejection{fmt.Sprintf("Google Analytics rejected the report (HTTP %d): %s", status, truncateLabel(redact(parsed.Error.Message, access)))}
		case status != http.StatusOK:
			return ga4Response{}, fmt.Errorf("Google Analytics answered HTTP %d", status)
		}
		return parsed, nil
	}
}
