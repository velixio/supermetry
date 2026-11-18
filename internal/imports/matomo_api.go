package imports

import (
	"bytes"
	"context"
	"encoding/json"
	"fmt"
	"net/http"
	"net/url"
	"sort"
	"strconv"
	"strings"
	"time"
)

// matomoRowLimit bounds the rows kept per report and day (Matomo's own
// "Others" aggregate row is dropped, so the long tail is not imported).
const (
	matomoRowLimit  = 1000
	matomoMaxGoals  = 50
	matomoOthersRow = "Others"
)

// matomoPuller reads the Matomo Reporting API (module=API over POST with
// token_auth in the body, so the token never lands in a URL or access log).
// Method names and column names follow the public Reporting API reference.
type matomoPuller struct {
	client *pullClient
	base   string
	siteID string
	token  string
}

func (p *matomoPuller) ChunkDays() int { return 14 }

// matomoNum decodes Matomo's mixed number encoding: numbers, numeric strings
// and empty values.
type matomoNum float64

func (n *matomoNum) UnmarshalJSON(data []byte) error {
	text := strings.Trim(strings.TrimSpace(string(data)), `"`)
	if text == "" || text == "null" || text == "false" {
		*n = 0
		return nil
	}
	value, err := strconv.ParseFloat(strings.TrimSuffix(text, "%"), 64)
	if err != nil || value < 0 || value > 1e15 {
		return fmt.Errorf("invalid number %q", text)
	}
	*n = matomoNum(value)
	return nil
}

func (n matomoNum) int() int64 { return int64(float64(n) + 0.5) }

type matomoRow struct {
	Label            string      `json:"label"`
	URL              string      `json:"url"`
	Code             string      `json:"code"`
	Visits           matomoNum   `json:"nb_visits"`
	UniqVisitors     matomoNum   `json:"nb_uniq_visitors"`
	Visitors         matomoNum   `json:"nb_visitors"`
	Actions          matomoNum   `json:"nb_actions"`
	Pageviews        matomoNum   `json:"nb_pageviews"`
	Hits             matomoNum   `json:"nb_hits"`
	Events           matomoNum   `json:"nb_events"`
	Conversions      matomoNum   `json:"nb_conversions"`
	VisitsConverted  matomoNum   `json:"nb_visits_converted"`
	Bounces          matomoNum   `json:"bounce_count"`
	VisitLength      matomoNum   `json:"sum_visit_length"`
	EntryVisits      matomoNum   `json:"entry_nb_visits"`
	EntryBounces     matomoNum   `json:"entry_bounce_count"`
	EntryVisitLength matomoNum   `json:"entry_sum_visit_length"`
	ExitVisits       matomoNum   `json:"exit_nb_visits"`
	IDGoal           json.Number `json:"idgoal"`
	Name             string      `json:"name"`
}

func (r matomoRow) visitors() int64 {
	if r.UniqVisitors > 0 {
		return r.UniqVisitors.int()
	}
	if r.Visitors > 0 {
		return r.Visitors.int()
	}
	return r.Visits.int()
}

// report calls one API method for the chunk and returns the per-day payloads.
func (p *matomoPuller) report(ctx context.Context, method string, extra url.Values, chunk DayRange) (map[time.Time]json.RawMessage, error) {
	form := url.Values{
		"module": {"API"}, "method": {method}, "idSite": {p.siteID}, "period": {"day"},
		"date":   {chunk.From.Format(time.DateOnly) + "," + chunk.To.Format(time.DateOnly)},
		"format": {"JSON"}, "token_auth": {p.token},
	}
	for key, values := range extra {
		form[key] = values
	}
	encoded := []byte(form.Encode())
	status, raw, err := p.client.do(ctx, p.token, func() (*http.Request, error) {
		request, err := http.NewRequest(http.MethodPost, p.base+"/index.php", bytes.NewReader(encoded))
		if err != nil {
			return nil, err
		}
		request.Header.Set("Content-Type", "application/x-www-form-urlencoded")
		request.Header.Set("Accept", "application/json")
		return request, nil
	})
	if err != nil {
		return nil, err
	}
	switch {
	case status == http.StatusUnauthorized || status == http.StatusForbidden:
		return nil, fmt.Errorf("%w: check the Matomo token_auth", errRemoteAuth)
	case status == http.StatusNotFound:
		return nil, fmt.Errorf("no Matomo API at this address (HTTP 404)")
	case status != http.StatusOK:
		return nil, fmt.Errorf("Matomo answered HTTP %d", status)
	}
	var days map[string]json.RawMessage
	if err := json.Unmarshal(raw, &days); err != nil {
		// A list at the top level is an empty result for some reports.
		var list []json.RawMessage
		if json.Unmarshal(raw, &list) == nil && len(list) == 0 {
			return nil, nil
		}
		return nil, fmt.Errorf("Matomo returned an unexpected response: %s", snippet(raw, p.token))
	}
	if result, ok := days["result"]; ok {
		var marker string
		_ = json.Unmarshal(result, &marker)
		if marker == "error" {
			var message string
			_ = json.Unmarshal(days["message"], &message)
			return nil, remoteRejection{fmt.Sprintf("Matomo error in %s: %s", method, redact(truncateLabel(message), p.token))}
		}
	}
	result := make(map[time.Time]json.RawMessage, len(days))
	for key, value := range days {
		day, err := time.Parse(time.DateOnly, key)
		if err != nil || !chunk.Contains(day) {
			continue
		}
		result[day] = value
	}
	return result, nil
}

// rows decodes a day's table; an empty day is [] (or an object for summaries).
func matomoRows(raw json.RawMessage) ([]matomoRow, error) {
	var rows []matomoRow
	if err := json.Unmarshal(raw, &rows); err != nil {
		return nil, err
	}
	return rows, nil
}

func matomoRowOf(raw json.RawMessage) (matomoRow, bool, error) {
	var row matomoRow
	if len(raw) == 0 || raw[0] == '[' {
		return row, false, nil
	}
	if err := json.Unmarshal(raw, &row); err != nil {
		return row, false, err
	}
	return row, true, nil
}

func sortedDayKeys(values map[time.Time]json.RawMessage) []time.Time {
	days := make([]time.Time, 0, len(values))
	for day := range values {
		days = append(days, day)
	}
	sort.Slice(days, func(i, j int) bool { return days[i].Before(days[j]) })
	return days
}

type matomoTable struct {
	method string
	extra  url.Values
	dim    int16
	// optional reports are skipped when the server rejects them (plugin off).
	optional bool
}

var matomoTables = []matomoTable{
	{method: "Actions.getPageUrls", extra: url.Values{"flat": {"1"}}, dim: DimPage},
	{method: "Referrers.getAll", dim: DimSource},
	{method: "UserCountry.getCountry", dim: DimCountry},
	{method: "DevicesDetection.getType", dim: DimDevice, optional: true},
	{method: "DevicesDetection.getBrowsers", dim: DimBrowser, optional: true},
	{method: "DevicesDetection.getOsFamilies", dim: DimOS, optional: true},
	{method: "Events.getAction", dim: DimEvent, optional: true},
}

func (p *matomoPuller) Fetch(ctx context.Context, chunk DayRange) (Aggregate, ParseStats, error) {
	result := newAggregate()
	var stats ParseStats
	if err := p.daily(ctx, chunk, result, &stats); err != nil {
		return result, stats, err
	}
	for _, table := range matomoTables {
		extra := url.Values{"filter_limit": {strconv.Itoa(matomoRowLimit)}}
		for key, values := range table.extra {
			extra[key] = values
		}
		days, err := p.report(ctx, table.method, extra, chunk)
		if err != nil {
			if table.optional && isRemoteRejection(err) {
				stats.badRow(0, fmt.Errorf("skipped %s: not available on this Matomo", table.method))
				continue
			}
			return result, stats, err
		}
		for _, day := range sortedDayKeys(days) {
			rows, err := matomoRows(days[day])
			if err != nil {
				stats.badRow(0, fmt.Errorf("%s %s: unexpected table shape", table.method, day.Format(time.DateOnly)))
				continue
			}
			for _, row := range rows {
				if row.Label == matomoOthersRow {
					continue
				}
				p.addRow(table, day, row, result)
				stats.Rows++
			}
		}
		if err := result.checkLimit(); err != nil {
			return result, stats, err
		}
	}
	if err := p.goals(ctx, chunk, result, &stats); err != nil {
		return result, stats, err
	}
	return result, stats, nil
}

// daily maps VisitsSummary.get (visits, visitors, bounces, time on site) and
// Actions.get (pageviews) onto the per-day totals.
func (p *matomoPuller) daily(ctx context.Context, chunk DayRange, result Aggregate, stats *ParseStats) error {
	summary, err := p.report(ctx, "VisitsSummary.get", nil, chunk)
	if err != nil {
		return err
	}
	for _, day := range sortedDayKeys(summary) {
		row, ok, err := matomoRowOf(summary[day])
		if err != nil {
			stats.badRow(0, fmt.Errorf("VisitsSummary %s: unexpected shape", day.Format(time.DateOnly)))
			continue
		}
		if !ok {
			continue
		}
		result.addDay(day, Metrics{
			Visitors: row.visitors(), Visits: row.Visits.int(),
			Bounces: row.Bounces.int(), VisitDurationS: row.VisitLength.int(),
		})
		stats.Rows++
	}
	actions, err := p.report(ctx, "Actions.get", nil, chunk)
	if err != nil {
		return err
	}
	for _, day := range sortedDayKeys(actions) {
		row, ok, err := matomoRowOf(actions[day])
		if err != nil || !ok {
			continue
		}
		if views := row.Pageviews.int(); views > 0 {
			result.addDay(day, Metrics{Pageviews: views})
		}
	}
	return nil
}

func (p *matomoPuller) addRow(table matomoTable, day time.Time, row matomoRow, result Aggregate) {
	metrics := Metrics{
		Visitors: row.visitors(), Visits: row.Visits.int(), Pageviews: row.Actions.int(),
		Bounces: row.Bounces.int(), VisitDurationS: row.VisitLength.int(),
	}
	switch table.dim {
	case DimPage:
		page := normalizePagePath(firstNonempty(row.URL, row.Label))
		// Pageviews are page hits; Matomo reports no per-page unique
		// visitors, so nb_visits stands in for them.
		result.addDimension(DimensionKey{day, DimPage, page}, Metrics{Visitors: row.Visits.int(), Pageviews: row.Hits.int()})
		if entries := row.EntryVisits.int(); entries > 0 {
			result.addDimension(DimensionKey{day, DimEntry, page}, Metrics{
				Visitors: entries, Visits: entries, Bounces: row.EntryBounces.int(), VisitDurationS: row.EntryVisitLength.int(),
			})
		}
		if exits := row.ExitVisits.int(); exits > 0 {
			result.addDimension(DimensionKey{day, DimExit, page}, Metrics{Visitors: exits, Visits: exits})
		}
	case DimSource:
		result.addDimension(DimensionKey{day, DimSource, normalizeSourceName(row.Label)}, metrics)
	case DimCountry:
		code := strings.ToUpper(strings.TrimSpace(row.Code))
		if len(code) != 2 {
			code = normalizeGA4Value(DimCountry, row.Label)
		}
		if len(code) == 2 {
			result.addDimension(DimensionKey{day, DimCountry, code}, metrics)
		}
	case DimEvent:
		name := strings.TrimSpace(row.Label)
		result.addDimension(DimensionKey{day, DimEvent, name}, Metrics{Visitors: row.Visits.int(), Events: row.Events.int()})
	default:
		result.addDimension(DimensionKey{day, table.dim, normalizeGA4Value(table.dim, row.Label)}, metrics)
	}
}

// goals lists the site's goals once per chunk and reads each goal's daily
// conversions, mapping them to custom-event rows named after the goal.
func (p *matomoPuller) goals(ctx context.Context, chunk DayRange, result Aggregate, stats *ParseStats) error {
	listing, err := p.listGoals(ctx)
	if err != nil {
		if isRemoteRejection(err) {
			stats.badRow(0, fmt.Errorf("skipped goals: not available on this Matomo"))
			return nil
		}
		return err
	}
	for _, goal := range listing {
		days, err := p.report(ctx, "Goals.get", url.Values{"idGoal": {goal.id}}, chunk)
		if err != nil {
			if isRemoteRejection(err) {
				continue
			}
			return err
		}
		for _, day := range sortedDayKeys(days) {
			row, ok, err := matomoRowOf(days[day])
			if err != nil || !ok || row.Conversions == 0 {
				continue
			}
			result.addDimension(DimensionKey{day, DimEvent, goal.name}, Metrics{Visitors: row.VisitsConverted.int(), Events: row.Conversions.int()})
			stats.Rows++
		}
	}
	return nil
}

type matomoGoal struct{ id, name string }

func (p *matomoPuller) listGoals(ctx context.Context) ([]matomoGoal, error) {
	form := url.Values{
		"module": {"API"}, "method": {"Goals.getGoals"}, "idSite": {p.siteID},
		"format": {"JSON"}, "token_auth": {p.token},
	}
	encoded := []byte(form.Encode())
	status, raw, err := p.client.do(ctx, p.token, func() (*http.Request, error) {
		request, err := http.NewRequest(http.MethodPost, p.base+"/index.php", bytes.NewReader(encoded))
		if err != nil {
			return nil, err
		}
		request.Header.Set("Content-Type", "application/x-www-form-urlencoded")
		return request, nil
	})
	if err != nil {
		return nil, err
	}
	if status != http.StatusOK {
		return nil, remoteRejection{fmt.Sprintf("Matomo answered HTTP %d for the goal list", status)}
	}
	var rows []matomoRow
	if err := json.Unmarshal(raw, &rows); err != nil {
		var asMap map[string]matomoRow
		if json.Unmarshal(raw, &asMap) != nil {
			return nil, remoteRejection{"Matomo returned an unexpected goal list"}
		}
		for _, row := range asMap {
			rows = append(rows, row)
		}
	}
	goals := make([]matomoGoal, 0, len(rows))
	for _, row := range rows {
		if row.IDGoal.String() == "" || strings.TrimSpace(row.Name) == "" {
			continue
		}
		goals = append(goals, matomoGoal{id: row.IDGoal.String(), name: strings.TrimSpace(row.Name)})
	}
	sort.Slice(goals, func(i, j int) bool { return goals[i].id < goals[j].id })
	if len(goals) > matomoMaxGoals {
		goals = goals[:matomoMaxGoals]
	}
	return goals, nil
}
