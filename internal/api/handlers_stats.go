package api

import (
	"encoding/json"
	"fmt"
	"net/http"
	"strconv"
	"strings"
	"time"

	openapi_types "github.com/oapi-codegen/runtime/types"

	"panel/internal/dict"
	"panel/internal/health"
	"panel/internal/realtime"
	"panel/internal/session"
	"panel/internal/sites"
)

type statsRange struct {
	from, to time.Time
	fromText string
	toText   string
}

func (h *handlers) statsSummary(w http.ResponseWriter, r *http.Request) {
	h.runStats(w, r, "summary", []Metric{Visitors, Visits, Pageviews, BounceRate, VisitDuration}, nil, "")
}

func (h *handlers) statsTimeseries(w http.ResponseWriter, r *http.Request) {
	interval := r.URL.Query().Get("interval")
	if interval == "" {
		interval = "day"
	}
	if interval != "hour" && interval != "day" && interval != "week" && interval != "month" {
		writeProblem(w, badRequest("interval must be hour, day, week, or month"))
		return
	}
	dimension := "time"
	if interval == "hour" {
		dimension = "hour"
	}
	h.runStats(w, r, "timeseries", []Metric{Visitors, Visits, Pageviews}, []string{dimension}, interval)
}

func (h *handlers) statsBreakdown(w http.ResponseWriter, r *http.Request) {
	dimension := r.URL.Query().Get("dimension")
	if dimension == "" {
		writeProblem(w, badRequest("dimension is required"))
		return
	}
	h.runStats(w, r, "breakdown", []Metric{Visitors, Pageviews}, []string{dimension}, "")
}

func (h *handlers) runStats(w http.ResponseWriter, r *http.Request, endpoint string, defaults []Metric, dimensions []string, interval string) {
	if h.query == nil {
		writeProblem(w, internalProblem())
		return
	}
	request, dateRange, all, problem := h.statsQuery(r, defaults, dimensions)
	if problem != nil {
		writeProblem(w, *problem)
		return
	}
	if interval == "hour" && (dateRange.from.IsZero() || dateRange.to.Sub(dateRange.from) >= 7*24*time.Hour) {
		writeProblem(w, badRequest("hour interval is limited to seven days"))
		return
	}
	if !authorizeRequest(w, r, request.SiteId, "stats:read") {
		return
	}
	filename := csvFilename(csvSite(r, request.SiteId), endpoint, dateRange.fromText, dateRange.toText)
	if all {
		streamQueryCSV(w, r, h.query, request, filename)
		return
	}
	var response QueryResponse
	var err error
	if interval != "" {
		response, err = queryAllPages(r, h.query, request)
	} else {
		response, err = h.query.Query(r.Context(), request)
	}
	if err != nil {
		writeQueryError(w, err)
		return
	}
	if interval == "hour" {
		response = normalizeHourlyTimeseries(response, request.Metrics, dateRange)
	} else if (interval == "week" || interval == "month") && needsExactBuckets(request.Metrics) {
		response, err = exactBucketTimeseries(r, h.query, request, response, dateRange, interval)
		if err != nil {
			writeQueryError(w, err)
			return
		}
	} else if interval != "" {
		response = normalizeTimeseries(response, request.Metrics, dateRange, interval)
	}
	writeQueryResponse(w, r, response, filename)
}

func needsExactBuckets(metrics []Metric) bool {
	for _, metric := range metrics {
		switch metric {
		case ViewsPerVisit, BounceRate, VisitDuration, EngagementTime, ScrollDepth, ConversionRate:
			return true
		}
	}
	return false
}

func exactBucketTimeseries(r *http.Request, service queryService, request QueryRequest, response QueryResponse, dateRange statsRange, interval string) (QueryResponse, error) {
	rows, err := queryMetricBuckets(r, service, request, dateRange, interval)
	if err != nil {
		return QueryResponse{}, err
	}
	response.Rows = rows
	if response.Comparison != nil {
		comparisonRange := statsRange{
			from: response.Comparison.DateRange.From.Time,
			to:   response.Comparison.DateRange.To.Time,
		}
		comparisonRows, queryErr := queryMetricBuckets(r, service, request, comparisonRange, interval)
		if queryErr != nil {
			return QueryResponse{}, queryErr
		}
		for index := range comparisonRows {
			if index < len(rows) {
				comparisonRows[index]["time"] = rows[index]["time"]
			}
		}
		response.Comparison.Rows = comparisonRows
	}
	return response, nil
}

func queryMetricBuckets(r *http.Request, service queryService, request QueryRequest, dateRange statsRange, interval string) ([]map[string]any, error) {
	emptyDimensions := []string{}
	request.Dimensions = &emptyDimensions
	request.Period = nil
	request.Compare = nil
	request.Cursor = nil
	request.Limit = nil
	rows := make([]map[string]any, 0)
	for bucket := bucketDay(dateRange.from, interval); !bucket.After(dateRange.to); bucket = nextBucket(bucket, interval) {
		from := bucket
		if from.Before(dateRange.from) {
			from = dateRange.from
		}
		to := nextBucket(bucket, interval).AddDate(0, 0, -1)
		if to.After(dateRange.to) {
			to = dateRange.to
		}
		request.DateRange = &DateRange{
			From: openapi_types.Date{Time: from},
			To:   openapi_types.Date{Time: to},
		}
		result, err := service.Query(r.Context(), request)
		if err != nil {
			return nil, err
		}
		row := make(map[string]any, len(request.Metrics)+1)
		row["time"] = bucketLabel(bucket, interval)
		for _, metric := range request.Metrics {
			row[string(metric)] = result.Totals[string(metric)]
		}
		rows = append(rows, row)
	}
	return rows, nil
}

func queryAllPages(r *http.Request, service queryService, request QueryRequest) (QueryResponse, error) {
	limit := csvPageSize
	request.Limit = &limit
	var combined QueryResponse
	first := true
	for len(combined.Rows) < csvMaxRows {
		response, err := service.Query(r.Context(), request)
		if err != nil {
			return QueryResponse{}, err
		}
		if first {
			combined = response
			combined.Rows = nil
			combined.NextCursor = nil
		}
		remaining := csvMaxRows - len(combined.Rows)
		if len(response.Rows) > remaining {
			response.Rows = response.Rows[:remaining]
		}
		combined.Rows = append(combined.Rows, response.Rows...)
		if !first && combined.Comparison != nil && response.Comparison != nil {
			combined.Comparison.Rows = append(combined.Comparison.Rows, response.Comparison.Rows...)
		}
		first = false
		if response.NextCursor == nil || len(response.Rows) == 0 {
			return combined, nil
		}
		request.Cursor = response.NextCursor
	}
	return combined, nil
}

func (h *handlers) statsQuery(r *http.Request, defaults []Metric, dimensions []string) (QueryRequest, statsRange, bool, *Problem) {
	if problem := validateFormat(r); problem != nil {
		return QueryRequest{}, statsRange{}, false, problem
	}
	siteID, problem := siteIDParam(r)
	if problem != nil {
		return QueryRequest{}, statsRange{}, false, problem
	}
	request := QueryRequest{SiteId: siteID, Metrics: defaults}
	dateRange, problem := h.parseStatsRange(r, siteID, &request)
	if problem != nil {
		return QueryRequest{}, statsRange{}, false, problem
	}
	if raw := r.URL.Query().Get("metrics"); raw != "" {
		parts := strings.Split(raw, ",")
		metrics := make([]Metric, len(parts))
		for index, part := range parts {
			metrics[index] = Metric(part)
			if part == "" || !metrics[index].Valid() {
				problem := badRequest(fmt.Sprintf("unknown metric %q", part))
				return QueryRequest{}, statsRange{}, false, &problem
			}
		}
		request.Metrics = metrics
	}
	if dimensions != nil {
		request.Dimensions = &dimensions
	}
	filters, filterProblem := parseStatsFilters(r.URL.Query()["filter"])
	if filterProblem != nil {
		return QueryRequest{}, statsRange{}, false, filterProblem
	}
	if len(filters) > 0 {
		request.Filters = &filters
	}
	if raw := r.URL.Query().Get("compare"); raw != "" {
		comparison := QueryRequestCompare(raw)
		if comparison != PreviousPeriod {
			problem := badRequest("compare must be previous_period")
			return QueryRequest{}, statsRange{}, false, &problem
		}
		request.Compare = &comparison
	}
	all := false
	if raw := r.URL.Query().Get("limit"); raw != "" {
		if raw == "all" {
			if !wantsCSV(r) {
				problem := badRequest("limit=all requires a CSV response")
				return QueryRequest{}, statsRange{}, false, &problem
			}
			all = true
		} else {
			limit, err := strconv.Atoi(raw)
			if err != nil || limit < 1 || limit > 1000 {
				problem := badRequest("limit must be between 1 and 1000, or all for CSV")
				return QueryRequest{}, statsRange{}, false, &problem
			}
			request.Limit = &limit
		}
	}
	if cursor := r.URL.Query().Get("cursor"); cursor != "" {
		request.Cursor = &cursor
	}
	return request, dateRange, all, nil
}

func (h *handlers) parseStatsRange(r *http.Request, siteID int32, request *QueryRequest) (statsRange, *Problem) {
	period := r.URL.Query().Get("period")
	if period == "" {
		problem := badRequest("period is required")
		return statsRange{}, &problem
	}
	timezone := r.URL.Query().Get("timezone")
	location := siteLocation(r, siteID)
	if timezone != "" {
		loaded, err := time.LoadLocation(timezone)
		if err != nil {
			problem := badRequest(fmt.Sprintf("invalid timezone %q", timezone))
			return statsRange{}, &problem
		}
		location = loaded
		request.Timezone = &timezone
	}
	now := h.currentTime().In(location)
	today := civilDay(now)
	fromText, toText := r.URL.Query().Get("date_from"), r.URL.Query().Get("date_to")
	if period == "custom" {
		from, errFrom := time.Parse("2006-01-02", fromText)
		to, errTo := time.Parse("2006-01-02", toText)
		if errFrom != nil || errTo != nil || fromText == "" || toText == "" || from.After(to) {
			problem := badRequest("custom period requires valid date_from and date_to dates")
			return statsRange{}, &problem
		}
		request.DateRange = &DateRange{From: openapi_types.Date{Time: from}, To: openapi_types.Date{Time: to}}
		return statsRange{from: from, to: to, fromText: fromText, toText: toText}, nil
	}
	if fromText != "" || toText != "" {
		problem := badRequest("date_from and date_to require period=custom")
		return statsRange{}, &problem
	}
	var from time.Time
	enginePeriod := Period(period)
	switch period {
	case "day", "today":
		enginePeriod = Today
		from = today
	case "yesterday":
		enginePeriod = Yesterday
		from = today.AddDate(0, 0, -1)
		today = from
	case "7d":
		from = today.AddDate(0, 0, -6)
	case "30d":
		from = today.AddDate(0, 0, -29)
	case "month":
		from = time.Date(today.Year(), today.Month(), 1, 0, 0, 0, 0, time.UTC)
	case "last_month":
		enginePeriod = LastMonth
		thisMonth := time.Date(today.Year(), today.Month(), 1, 0, 0, 0, 0, time.UTC)
		from = thisMonth.AddDate(0, -1, 0)
		today = thisMonth.AddDate(0, 0, -1)
	case "6mo":
		from = today.AddDate(0, -6, 1)
		request.DateRange = &DateRange{From: openapi_types.Date{Time: from}, To: openapi_types.Date{Time: today}}
	case "12mo":
		from = today.AddDate(-1, 0, 1)
	case "year":
		from = time.Date(today.Year(), time.January, 1, 0, 0, 0, 0, time.UTC)
	case "all":
		enginePeriod = Period("all")
		request.Period = &enginePeriod
		from = time.Date(1970, time.January, 1, 0, 0, 0, 0, time.UTC)
		return statsRange{from: from, to: today, fromText: from.Format("2006-01-02"), toText: today.Format("2006-01-02")}, nil
	default:
		problem := badRequest(fmt.Sprintf("unknown period %q", period))
		return statsRange{}, &problem
	}
	if request.DateRange == nil {
		request.Period = &enginePeriod
	}
	return statsRange{from: from, to: today, fromText: from.Format("2006-01-02"), toText: today.Format("2006-01-02")}, nil
}

func parseStatsFilters(values []string) ([]Filter, *Problem) {
	if len(values) > 10 {
		problem := badRequest("at most 10 filters are allowed")
		return nil, &problem
	}
	filters := make([]Filter, 0, len(values))
	operators := []FilterOperator{StartsWith, Contains, IsNot, Is}
	for _, raw := range values {
		var filter Filter
		for _, operator := range operators {
			marker := ":" + string(operator) + ":"
			if index := strings.Index(raw, marker); index > 0 {
				filter.Dimension = raw[:index]
				filter.Operator = operator
				filter.Values = strings.Split(raw[index+len(marker):], ",")
				break
			}
		}
		if filter.Dimension == "" || len(filter.Values) == 0 {
			problem := badRequest("filter must be dimension:operator:value[,value]")
			return nil, &problem
		}
		for _, value := range filter.Values {
			if value == "" {
				problem := badRequest("filter values must not be empty")
				return nil, &problem
			}
		}
		filters = append(filters, filter)
	}
	return filters, nil
}

func normalizeTimeseries(response QueryResponse, metrics []Metric, dateRange statsRange, interval string) QueryResponse {
	if dateRange.from.IsZero() || dateRange.to.IsZero() {
		return response
	}
	rows := make(map[string]map[string]any, len(response.Rows))
	for _, row := range response.Rows {
		text, ok := row["time"].(string)
		if !ok {
			continue
		}
		day, err := time.Parse("2006-01-02", text)
		if err != nil {
			continue
		}
		key := bucketLabel(day, interval)
		target := rows[key]
		if target == nil {
			target = map[string]any{"time": key}
			rows[key] = target
		}
		for _, metric := range metrics {
			target[string(metric)] = number(target[string(metric)]) + number(row[string(metric)])
		}
	}
	start := bucketDay(dateRange.from, interval)
	result := make([]map[string]any, 0)
	for day := start; !day.After(dateRange.to); day = nextBucket(day, interval) {
		key := bucketLabel(day, interval)
		row := rows[key]
		if row == nil {
			row = map[string]any{"time": key}
			for _, metric := range metrics {
				row[string(metric)] = float64(0)
			}
		}
		result = append(result, row)
	}
	response.Rows = result
	if response.Comparison != nil {
		comparisonRange := statsRange{from: response.Comparison.DateRange.From.Time, to: response.Comparison.DateRange.To.Time}
		comparison := normalizeTimeseries(QueryResponse{Rows: response.Comparison.Rows}, metrics, comparisonRange, interval)
		response.Comparison.Rows = alignComparisonRows(result, comparison.Rows)
	}
	return response
}

func normalizeHourlyTimeseries(response QueryResponse, metrics []Metric, dateRange statsRange) QueryResponse {
	if dateRange.from.IsZero() || dateRange.to.IsZero() {
		return response
	}
	if len(response.Columns) > 0 && response.Columns[0] == "hour" {
		response.Columns[0] = "time"
	}
	rows := make(map[string]map[string]any, len(response.Rows))
	for _, row := range response.Rows {
		key, _ := row["hour"].(string)
		if key == "" {
			key, _ = row["time"].(string)
		}
		if key == "" {
			continue
		}
		row["time"] = key
		delete(row, "hour")
		rows[key] = row
	}
	start := time.Date(dateRange.from.Year(), dateRange.from.Month(), dateRange.from.Day(), 0, 0, 0, 0, time.UTC)
	end := time.Date(dateRange.to.Year(), dateRange.to.Month(), dateRange.to.Day(), 23, 0, 0, 0, time.UTC)
	result := make([]map[string]any, 0, int(end.Sub(start)/time.Hour)+1)
	for hour := start; !hour.After(end); hour = hour.Add(time.Hour) {
		key := hour.Format("2006-01-02T15:00:00")
		row := rows[key]
		if row == nil {
			row = map[string]any{"time": key}
			for _, metric := range metrics {
				row[string(metric)] = float64(0)
			}
		}
		result = append(result, row)
	}
	response.Rows = result
	if response.Comparison != nil {
		comparisonRange := statsRange{from: response.Comparison.DateRange.From.Time, to: response.Comparison.DateRange.To.Time}
		comparison := normalizeHourlyTimeseries(QueryResponse{Rows: response.Comparison.Rows}, metrics, comparisonRange)
		response.Comparison.Rows = alignComparisonRows(result, comparison.Rows)
	}
	return response
}

func alignComparisonRows(current, comparison []map[string]any) []map[string]any {
	for index := range comparison {
		if index < len(current) {
			comparison[index]["time"] = current[index]["time"]
		}
	}
	return comparison
}

func bucketDay(day time.Time, interval string) time.Time {
	if interval == "week" {
		return day.AddDate(0, 0, -((int(day.Weekday()) + 6) % 7))
	}
	if interval == "month" {
		return time.Date(day.Year(), day.Month(), 1, 0, 0, 0, 0, time.UTC)
	}
	return day
}

func bucketLabel(day time.Time, interval string) string {
	return bucketDay(day, interval).Format("2006-01-02")
}

func nextBucket(day time.Time, interval string) time.Time {
	if interval == "week" {
		return day.AddDate(0, 0, 7)
	}
	if interval == "month" {
		return day.AddDate(0, 1, 0)
	}
	return day.AddDate(0, 0, 1)
}

func number(value any) float64 {
	switch typed := value.(type) {
	case float64:
		return typed
	case float32:
		return float64(typed)
	case int:
		return float64(typed)
	case int64:
		return float64(typed)
	default:
		return 0
	}
}

func siteLocation(r *http.Request, siteID int32) *time.Location {
	registry, _ := r.Context().Value(registryContextKey{}).(sites.Registry)
	if registry != nil {
		if site, ok := registry.Site(siteID); ok && site.TZ != nil {
			return site.TZ
		}
	}
	return time.UTC
}

func csvSite(r *http.Request, siteID int32) string {
	registry, _ := r.Context().Value(registryContextKey{}).(sites.Registry)
	if registry != nil {
		if site, ok := registry.Site(siteID); ok && site.PublicID != "" {
			return site.PublicID
		}
	}
	return strconv.FormatInt(int64(siteID), 10)
}

func civilDay(value time.Time) time.Time {
	return time.Date(value.Year(), value.Month(), value.Day(), 0, 0, 0, 0, time.UTC)
}

func queryRangeLabels(r *http.Request, request QueryRequest, now time.Time) (string, string) {
	if request.DateRange != nil {
		return request.DateRange.From.Format("2006-01-02"), request.DateRange.To.Format("2006-01-02")
	}
	if request.Period == nil {
		return "custom", "custom"
	}
	today := civilDay(now.In(siteLocation(r, request.SiteId)))
	period := string(*request.Period)
	if period == "all" {
		return "1970-01-01", today.Format("2006-01-02")
	}
	from := today
	switch period {
	case "today", "day":
	case "yesterday":
		from, today = today.AddDate(0, 0, -1), today.AddDate(0, 0, -1)
	case "7d":
		from = today.AddDate(0, 0, -6)
	case "30d":
		from = today.AddDate(0, 0, -29)
	case "month":
		from = time.Date(today.Year(), today.Month(), 1, 0, 0, 0, 0, time.UTC)
	case "12mo":
		from = today.AddDate(-1, 0, 1)
	case "year":
		from = time.Date(today.Year(), time.January, 1, 0, 0, 0, 0, time.UTC)
	default:
		return period, period
	}
	return from.Format("2006-01-02"), today.Format("2006-01-02")
}

func realtimeHandler(store session.Store, now func() time.Time, dictionary dictionaryResolver) http.HandlerFunc {
	return func(w http.ResponseWriter, r *http.Request) {
		siteID, problem := siteIDParam(r)
		if problem != nil {
			writeProblem(w, *problem)
			return
		}
		if !authorizeRequest(w, r, siteID, "stats:read") {
			return
		}
		current := time.Now()
		if now != nil {
			current = now()
		}
		writeJSON(w, http.StatusOK, realtimeResponse(store, dictionary, siteID, current))
	}
}

// realtimeResponse builds the realtime payload shared by the HTTP API and MCP.
func realtimeResponse(store session.Store, dictionary dictionaryResolver, siteID int32, now time.Time) RealtimeResponse {
	snapshot := realtime.RichSnapshot(store, siteID, now)
	response := RealtimeResponse{
		ActiveVisitors: int64(snapshot.ActiveVisitors), PageviewsLast30m: int64(snapshot.Pageviews),
		TopPages: make([]TopPage, len(snapshot.Pages)), TopSources: make([]TopSource, len(snapshot.Sources)),
		TopEntryPages: make([]TopPage, len(snapshot.EntryPages)), Countries: make([]RealtimeCountry, len(snapshot.Countries)),
		Regions: make([]RealtimeLocation, len(snapshot.Regions)), Cities: make([]RealtimeLocation, len(snapshot.Cities)),
		Devices: make([]RealtimeLabel, len(snapshot.Devices)), Browsers: make([]RealtimeLabel, len(snapshot.Browsers)),
		OperatingSystems: make([]RealtimeLabel, len(snapshot.OperatingSystems)), Events: make([]RealtimeEvent, len(snapshot.Events)),
		RecentActivity: make([]RecentActivity, len(snapshot.RecentActivity)), VisitorsPerMinute: make([]RealtimeMinute, len(snapshot.VisitorsPerMinute)),
	}
	for index, page := range snapshot.Pages {
		response.TopPages[index] = TopPage{Page: dictLabel(dictionary, dict.Path, page.ID), Visitors: int64(page.Visitors)}
	}
	for index, page := range snapshot.EntryPages {
		response.TopEntryPages[index] = TopPage{Page: dictLabel(dictionary, dict.Path, page.ID), Visitors: int64(page.Visitors)}
	}
	for index, source := range snapshot.Sources {
		response.TopSources[index] = TopSource{Source: dictLabel(dictionary, dict.Source, source.ID), Visitors: int64(source.Visitors)}
	}
	for index, value := range snapshot.Countries {
		response.Countries[index] = RealtimeCountry{CountryCode: value.CountryCode, Visitors: int64(value.Visitors)}
	}
	for index, value := range snapshot.Regions {
		response.Regions[index] = RealtimeLocation{Label: dictLabel(dictionary, dict.Region, value.ID), CountryCode: value.CountryCode, Visitors: int64(value.Visitors)}
	}
	for index, value := range snapshot.Cities {
		response.Cities[index] = RealtimeLocation{Label: dictLabel(dictionary, dict.City, value.ID), CountryCode: value.CountryCode, Visitors: int64(value.Visitors)}
	}
	for index, value := range snapshot.Devices {
		response.Devices[index] = RealtimeLabel{Label: deviceLabel(int16(value.ID)), Visitors: int64(value.Visitors)}
	}
	for index, value := range snapshot.Browsers {
		response.Browsers[index] = RealtimeLabel{Label: dictLabel(dictionary, dict.Browser, value.ID), Visitors: int64(value.Visitors)}
	}
	for index, value := range snapshot.OperatingSystems {
		response.OperatingSystems[index] = RealtimeLabel{Label: dictLabel(dictionary, dict.OS, value.ID), Visitors: int64(value.Visitors)}
	}
	for index, value := range snapshot.Events {
		response.Events[index] = RealtimeEvent{Name: dictLabel(dictionary, dict.EventName, value.NameID), Count: int64(value.Count)}
	}
	for index, value := range snapshot.VisitorsPerMinute {
		response.VisitorsPerMinute[index] = RealtimeMinute{Minute: value.Minute, Visitors: int64(value.Visitors)}
	}
	for index, value := range snapshot.RecentActivity {
		kind := "custom"
		if value.Kind == 0 {
			kind = "pageview"
		}
		response.RecentActivity[index] = RecentActivity{
			Kind: kind, Page: dictLabel(dictionary, dict.Path, value.PathID), EventName: dictLabel(dictionary, dict.EventName, value.NameID),
			CountryCode: value.CountryCode, City: dictLabel(dictionary, dict.City, value.CityID), Device: deviceLabel(value.Device),
			Browser: dictLabel(dictionary, dict.Browser, value.BrowserID), Source: dictLabel(dictionary, dict.Source, value.SourceID), Timestamp: value.Timestamp,
		}
	}
	return response
}

func dictLabel(dictionary dictionaryResolver, kind dict.Kind, id int32) string {
	if id == 0 {
		return ""
	}
	return realtimeLabel(dictionary, kind, strconv.FormatInt(int64(id), 10))
}

func deviceLabel(device int16) string {
	switch device {
	case 0:
		return "Desktop"
	case 1:
		return "Mobile"
	case 2:
		return "Tablet"
	default:
		return "Unknown"
	}
}

func realtimeLabel(dictionary dictionaryResolver, kind dict.Kind, encoded string) string {
	id, err := strconv.ParseInt(encoded, 10, 32)
	if err == nil && dictionary != nil {
		if value, ok := dictionary.Value(kind, int32(id)); ok {
			return value
		}
	}
	return encoded
}

func dataHealthHandler(w http.ResponseWriter, r *http.Request) {
	if !authorizeRequest(w, r, 0, "admin:read") {
		return
	}
	snapshot := health.Snapshot()
	bots := int64(0)
	for key, value := range snapshot {
		if strings.HasPrefix(key, "bots_filtered_total{") {
			bots += int64(value)
		}
	}
	writeJSON(w, http.StatusOK, DataHealthResponse{BotsFiltered: bots, CloseLagSeconds: snapshot["close_lag_seconds"], Drops503: int64(snapshot["ingest_dropped_total"]), IngestRate: snapshot["ingest_accepted_total"], QueueDepth: int64(snapshot["writer_queue_depth"])})
}

type DataHealthResponse struct {
	BotsFiltered    int64   `json:"bots_filtered"`
	CloseLagSeconds float64 `json:"close_lag_seconds"`
	Drops503        int64   `json:"drops_503"`
	IngestRate      float64 `json:"ingest_rate"`
	QueueDepth      int64   `json:"queue_depth"`
}

func siteIDParam(r *http.Request) (int32, *Problem) {
	value, err := strconv.ParseInt(r.URL.Query().Get("site_id"), 10, 32)
	if err != nil || value < 1 {
		problem := badRequest("site_id must be a positive integer")
		return 0, &problem
	}
	return int32(value), nil
}

func writeJSON(w http.ResponseWriter, status int, value any) {
	w.Header().Set("Content-Type", "application/json")
	w.WriteHeader(status)
	_ = json.NewEncoder(w).Encode(value)
}
