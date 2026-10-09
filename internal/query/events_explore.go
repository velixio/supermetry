package query

import (
	"context"
	"encoding/base64"
	"encoding/json"
	"errors"
	"fmt"
	"strconv"
	"strings"
	"time"

	"github.com/jackc/pgx/v5"
	openapi_types "github.com/oapi-codegen/runtime/types"

	"supermetry/internal/api"
	"supermetry/internal/db"
	"supermetry/internal/dict"
	"supermetry/internal/privacy"
	"supermetry/internal/sites"
)

const (
	eventExploreDefaultLimit = 50
	eventExploreMaxLimit     = 100
	eventExploreAggregateCap = 100
	suggestionCap            = 20
)

type normalizedEventExplore struct {
	siteID    int32
	retention db.RetentionClass
	from      time.Time
	to        time.Time
	start     time.Time
	end       time.Time
	name      string
	filters   []api.Filter
	after     *eventCursor
	limit     int
}

type normalizedExploreRange struct {
	siteID    int32
	retention db.RetentionClass
	from      time.Time
	to        time.Time
	start     time.Time
	end       time.Time
}

func (e *Engine) Events(ctx context.Context, req api.EventExploreRequest) (api.EventExploreList, error) {
	if e.trackingMode != privacy.Identified {
		return api.EventExploreList{}, api.ErrAnonymousHistory
	}
	site, err := e.exploreSite(req.SiteID)
	if err != nil {
		return api.EventExploreList{}, err
	}
	normalized, err := normalizeEventExplore(req, site, e.reportNow())
	if err != nil {
		return api.EventExploreList{}, err
	}
	querySQL, args, err := buildEventsExploreSQL(normalized)
	if err != nil {
		return api.EventExploreList{}, err
	}
	response := api.EventExploreList{Data: make([]api.EventExploreItem, 0, normalized.limit)}
	keys := make([]eventCursor, 0, normalized.limit+1)
	err = WithStatementTimeout(ctx, e.pool, queryTimeout, func(tx pgx.Tx) error {
		countSQL, countArgs, countErr := buildEventsExploreCountSQL(normalized)
		if countErr != nil {
			return countErr
		}
		var count int
		if countErr := tx.QueryRow(ctx, countSQL, countArgs...).Scan(&count); countErr != nil {
			return countErr
		}
		response.Total, response.TotalCapped = cappedTotal(count)
		rows, queryErr := tx.Query(ctx, querySQL, args...)
		if queryErr != nil {
			return queryErr
		}
		defer rows.Close()
		for rows.Next() {
			var item api.EventExploreItem
			var kind int16
			var props []byte
			var key eventCursor
			if scanErr := rows.Scan(&item.Ts, &kind, &item.Name, &item.Page, &props, &item.Country, &item.City, &item.Device, &item.Browser, &item.Os, &item.Source, &item.SessionId, &item.HasReplay,
				&key.sessionID, &key.nameID, &key.pathID); scanErr != nil {
				return scanErr
			}
			key.ts, key.kind = item.Ts, kind
			keys = append(keys, key)
			if kind == 0 {
				item.Kind = api.ExplorePageview
			} else {
				item.Kind = api.ExploreEvent
			}
			if len(props) != 0 {
				decoded := make(map[string]string)
				if jsonErr := json.Unmarshal(props, &decoded); jsonErr != nil {
					return jsonErr
				}
				item.Props = &decoded
			}
			response.Data = append(response.Data, item)
		}
		return rows.Err()
	})
	if err != nil {
		return api.EventExploreList{}, fmt.Errorf("query events: %w", err)
	}
	if len(response.Data) > normalized.limit {
		response.Data = response.Data[:normalized.limit]
		next := keys[normalized.limit-1].encode()
		response.NextCursor = &next
	}
	return response, nil
}

func (e *Engine) Properties(ctx context.Context, req api.PropertyExploreRequest) ([]api.PropertyKeyCount, error) {
	if e.trackingMode != privacy.Identified {
		return nil, api.ErrAnonymousHistory
	}
	rangeValue, err := e.normalizeExploreRange(req.SiteID, req.From, req.To)
	if err != nil {
		return nil, err
	}
	builder := &sqlBuilder{}
	where, joins := exploreBaseSQL(builder, rangeValue, req.Event)
	querySQL := `select property.key,count(*)::int8
from events e ` + joins + ` cross join lateral jsonb_object_keys(e.props) property(key)
where ` + where + ` and e.props is not null
group by property.key order by count(*) desc,property.key limit ` + builder.arg(eventExploreAggregateCap)
	result := make([]api.PropertyKeyCount, 0)
	err = WithStatementTimeout(ctx, e.pool, queryTimeout, func(tx pgx.Tx) error {
		rows, queryErr := tx.Query(ctx, querySQL, builder.args...)
		if queryErr != nil {
			return queryErr
		}
		defer rows.Close()
		for rows.Next() {
			var item api.PropertyKeyCount
			if scanErr := rows.Scan(&item.Key, &item.Events); scanErr != nil {
				return scanErr
			}
			result = append(result, item)
		}
		return rows.Err()
	})
	if err != nil {
		return nil, fmt.Errorf("query property keys: %w", err)
	}
	return result, nil
}

func (e *Engine) PropertyValues(ctx context.Context, req api.PropertyValueExploreRequest) ([]api.PropertyValueCount, error) {
	if e.trackingMode != privacy.Identified {
		return nil, api.ErrAnonymousHistory
	}
	if strings.TrimSpace(req.Key) == "" || len(req.Key) > 2000 {
		return nil, errors.New("property key must be between 1 and 2000 characters")
	}
	rangeValue, err := e.normalizeExploreRange(req.SiteID, req.From, req.To)
	if err != nil {
		return nil, err
	}
	builder := &sqlBuilder{}
	where, joins := exploreBaseSQL(builder, rangeValue, req.Event)
	keyArg := builder.arg(req.Key)
	querySQL := `select e.props->>` + keyArg + ` as value,count(*)::int8
from events e ` + joins + `
where ` + where + ` and e.props ? ` + keyArg + `
group by 1 order by count(*) desc,value limit ` + builder.arg(eventExploreAggregateCap)
	result := make([]api.PropertyValueCount, 0)
	err = WithStatementTimeout(ctx, e.pool, queryTimeout, func(tx pgx.Tx) error {
		rows, queryErr := tx.Query(ctx, querySQL, builder.args...)
		if queryErr != nil {
			return queryErr
		}
		defer rows.Close()
		for rows.Next() {
			var item api.PropertyValueCount
			if scanErr := rows.Scan(&item.Value, &item.Events); scanErr != nil {
				return scanErr
			}
			result = append(result, item)
		}
		return rows.Err()
	})
	if err != nil {
		return nil, fmt.Errorf("query property values: %w", err)
	}
	return result, nil
}

func (e *Engine) Suggestions(ctx context.Context, req api.SuggestionExploreRequest) ([]api.Suggestion, error) {
	if e.trackingMode != privacy.Identified {
		return nil, api.ErrAnonymousHistory
	}
	rangeValue, err := e.normalizeExploreRange(req.SiteID, req.From, req.To)
	if err != nil {
		return nil, err
	}
	if len(req.Query) > 2000 {
		return nil, errors.New("suggestion query must not exceed 2000 characters")
	}
	builder := &sqlBuilder{}
	where, _ := exploreBaseSQL(builder, rangeValue, "")
	valueSQL, joinSQL, extraWhere, err := suggestionValueSQL(builder, req.Dimension)
	if err != nil {
		return nil, err
	}
	queryArg := builder.arg(strings.ToLower(req.Query))
	querySQL := `select ` + valueSQL + ` as value,count(*)::int8
from events e ` + joinSQL + `
where ` + where + extraWhere + ` and (` + queryArg + `='' or position(` + queryArg + ` in lower(` + valueSQL + `))>0)
group by value
order by (position(` + queryArg + ` in lower(` + valueSQL + `))=1) desc,count(*) desc,value
limit ` + builder.arg(suggestionCap)
	result := make([]api.Suggestion, 0, suggestionCap)
	err = WithStatementTimeout(ctx, e.pool, queryTimeout, func(tx pgx.Tx) error {
		rows, queryErr := tx.Query(ctx, querySQL, builder.args...)
		if queryErr != nil {
			return queryErr
		}
		defer rows.Close()
		for rows.Next() {
			var item api.Suggestion
			if scanErr := rows.Scan(&item.Value, &item.Count); scanErr != nil {
				return scanErr
			}
			result = append(result, item)
		}
		return rows.Err()
	})
	if err != nil {
		return nil, fmt.Errorf("query suggestions: %w", err)
	}
	return result, nil
}

func (e *Engine) exploreSite(siteID int32) (sites.Site, error) {
	if e == nil || e.pool == nil || e.registry == nil {
		return sites.Site{}, errors.New("query engine is not configured")
	}
	site, ok := e.registry.Site(siteID)
	if !ok {
		return sites.Site{}, fmt.Errorf("unknown site %d", siteID)
	}
	return site, nil
}

func (e *Engine) normalizeExploreRange(siteID int32, from, to time.Time) (normalizedExploreRange, error) {
	site, err := e.exploreSite(siteID)
	if err != nil {
		return normalizedExploreRange{}, err
	}
	request := api.EventExploreRequest{SiteID: siteID, From: from, To: to, Limit: eventExploreDefaultLimit}
	normalized, err := normalizeEventExplore(request, site, e.reportNow())
	if err != nil {
		return normalizedExploreRange{}, err
	}
	return normalizedExploreRange{
		siteID: normalized.siteID, retention: normalized.retention, from: normalized.from, to: normalized.to,
		start: normalized.start, end: normalized.end,
	}, nil
}

func normalizeEventExplore(req api.EventExploreRequest, site sites.Site, now time.Time) (normalizedEventExplore, error) {
	dateRange := api.DateRange{From: openapi_types.Date{Time: req.From}, To: openapi_types.Date{Time: req.To}}
	if err := validateRawReportRange(req.SiteID, dateRange, nil, site, now); err != nil {
		return normalizedEventExplore{}, err
	}
	if len(req.Filters) > maxFilters {
		return normalizedEventExplore{}, errors.New("at most 10 filters are allowed")
	}
	for _, filter := range req.Filters {
		if !filter.Operator.Valid() || len(filter.Values) == 0 {
			return normalizedEventExplore{}, fmt.Errorf("invalid filter %q", filter.Dimension)
		}
		if strings.HasPrefix(filter.Dimension, "prop:") {
			if strings.TrimPrefix(filter.Dimension, "prop:") == "" {
				return normalizedEventExplore{}, errors.New("property filter requires a key")
			}
			continue
		}
		if _, _, err := eventExploreResolver("e")(filter.Dimension); err != nil {
			return normalizedEventExplore{}, err
		}
	}
	if len(req.Name) > 2000 {
		return normalizedEventExplore{}, errors.New("name must not exceed 2000 characters")
	}
	limit := req.Limit
	if limit == 0 {
		limit = eventExploreDefaultLimit
	}
	if limit < 1 || limit > eventExploreMaxLimit {
		return normalizedEventExplore{}, errors.New("limit must be between 1 and 100")
	}
	after, err := decodeEventCursor(req.Cursor)
	if err != nil {
		return normalizedEventExplore{}, err
	}
	location := site.TZ
	if location == nil {
		location = time.UTC
	}
	from, to := civilDate(req.From), civilDate(req.To)
	start := time.Date(from.Year(), from.Month(), from.Day(), 0, 0, 0, 0, location)
	end := time.Date(to.Year(), to.Month(), to.Day(), 0, 0, 0, 0, location).AddDate(0, 0, 1)
	return normalizedEventExplore{
		siteID: req.SiteID, retention: site.Retention, from: from, to: to, start: start, end: end,
		name: strings.TrimSpace(req.Name), filters: append([]api.Filter(nil), req.Filters...), after: after, limit: limit,
	}, nil
}

func buildEventsExploreSQL(req normalizedEventExplore) (string, []any, error) {
	builder, fromSQL, where, err := buildEventsExploreBaseSQL(req, true)
	if err != nil {
		return "", nil, err
	}
	querySQL := `select e.ts,e.kind,case when e.kind=0 then 'pageview' else coalesce(event_name.value,'') end,
	       coalesce(path.value,''),e.props,trim(e.country),coalesce(city.value,''),
	       case e.device when 0 then 'desktop' when 1 then 'mobile' when 2 then 'tablet' else 'unknown' end,
	       coalesce(browser.value,''),coalesce(os.value,''),coalesce(source.value,''),e.session_id::text,
       exists (select 1 from replay_sessions rr where rr.site_id=e.site_id and rr.session_id=e.session_id),
       e.session_id,e.name_id,e.path_id
` + fromSQL + `
left join dict path on path.kind=` + strconv.Itoa(int(dict.Path)) + ` and path.id=e.path_id
left join dict browser on browser.kind=` + strconv.Itoa(int(dict.Browser)) + ` and browser.id=e.browser_id
left join dict os on os.kind=` + strconv.Itoa(int(dict.OS)) + ` and os.id=e.os_id
left join dict city on city.kind=` + strconv.Itoa(int(dict.City)) + ` and city.id=e.city_id
left join dict source on source.kind=` + strconv.Itoa(int(dict.Source)) + ` and source.id=e.source_id
where ` + where + `
order by e.ts desc,e.session_id desc,e.kind desc,e.name_id desc,e.path_id desc
limit ` + builder.arg(req.limit+1)
	return querySQL, builder.args, nil
}

func buildEventsExploreBaseSQL(req normalizedEventExplore, includeCursor bool) (*sqlBuilder, string, string, error) {
	builder := &sqlBuilder{}
	rangeValue := normalizedExploreRange{siteID: req.siteID, retention: req.retention, from: req.from, to: req.to, start: req.start, end: req.end}
	where, joins := exploreBaseSQL(builder, rangeValue, req.name)
	conditions, err := eventExploreFilterConditions(builder, req.filters)
	if err != nil {
		return nil, "", "", err
	}
	where += andSQL(conditions)
	if includeCursor && req.after != nil {
		// Keyset pagination: newer rows that arrive between requests cannot
		// shift later pages, and deep pages cost no more than the first one.
		where += " and (e.ts,e.session_id,e.kind,e.name_id,e.path_id)<(" + builder.arg(req.after.ts) + "::timestamptz," +
			builder.arg(req.after.sessionID) + "::int8," + builder.arg(req.after.kind) + "::int2," +
			builder.arg(req.after.nameID) + "::int4," + builder.arg(req.after.pathID) + "::int4)"
	}
	return builder, "from events e " + joins, where, nil
}

func buildEventsExploreCountSQL(req normalizedEventExplore) (string, []any, error) {
	builder, fromSQL, where, err := buildEventsExploreBaseSQL(req, false)
	if err != nil {
		return "", nil, err
	}
	querySQL := `select count(*) from (
  select 1 ` + fromSQL + ` where ` + where + ` limit ` + builder.arg(listTotalCap+1) + `
) t`
	return querySQL, builder.args, nil
}

func exploreBaseSQL(builder *sqlBuilder, req normalizedExploreRange, event string) (string, string) {
	where := "e.site_id=" + builder.arg(req.siteID) +
		" and e.retention_class=" + builder.arg(req.retention) +
		" and e.local_day between " + builder.arg(dateParam(req.from)) + "::date and " + builder.arg(dateParam(req.to)) + "::date" +
		" and e.ts>=" + builder.arg(req.start) + " and e.ts<" + builder.arg(req.end)
	joins := "left join dict event_name on event_name.kind=" + strconv.Itoa(int(dict.EventName)) + " and event_name.id=e.name_id"
	if event == "pageview" {
		where += " and e.kind=0"
	} else if event != "" {
		where += " and e.kind=1 and event_name.value=" + builder.arg(event)
	}
	return where, joins
}

func eventExploreFilterConditions(builder *sqlBuilder, filters []api.Filter) ([]string, error) {
	conditions := make([]string, 0, len(filters))
	for _, filter := range filters {
		if strings.HasPrefix(filter.Dimension, "prop:") {
			key := strings.TrimPrefix(filter.Dimension, "prop:")
			if key == "" {
				return nil, errors.New("property filter requires a key")
			}
			match, err := textMatch(builder, "e.props->>"+builder.arg(key), filter)
			if err != nil {
				return nil, err
			}
			conditions = append(conditions, "e.props ? "+builder.arg(key)+" and "+match)
			continue
		}
		matches, err := filterConditions(builder, []api.Filter{filter}, eventExploreResolver("e"))
		if err != nil {
			return nil, err
		}
		conditions = append(conditions, matches[0])
	}
	return conditions, nil
}

func eventExploreResolver(alias string) dimensionResolver {
	prefix := alias + "."
	return func(dimension string) (string, *dict.Kind, error) {
		switch dimension {
		case "page", "path":
			return prefix + "path_id", dictionaryKind(dict.Path), nil
		case "source":
			return prefix + "source_id", dictionaryKind(dict.Source), nil
		case "channel":
			return channelSQL(prefix+"source_id", prefix+"medium_id"), nil, nil
		case "utm_source":
			return prefix + "utm_source_id", dictionaryKind(dict.Source), nil
		case "utm_medium":
			return prefix + "medium_id", dictionaryKind(dict.Medium), nil
		case "utm_campaign":
			return prefix + "campaign_id", dictionaryKind(dict.Campaign), nil
		case "utm_content":
			return prefix + "utm_content_id", dictionaryKind(dict.UTMContent), nil
		case "utm_term":
			return prefix + "utm_term_id", dictionaryKind(dict.UTMTerm), nil
		case "event":
			return prefix + "name_id", dictionaryKind(dict.EventName), nil
		case "country":
			return prefix + "country", nil, nil
		case "region":
			return prefix + "region_id", dictionaryKind(dict.Region), nil
		case "city":
			return prefix + "city_id", dictionaryKind(dict.City), nil
		case "browser":
			return prefix + "browser_id", dictionaryKind(dict.Browser), nil
		case "os":
			return prefix + "os_id", dictionaryKind(dict.OS), nil
		case "device":
			return prefix + "device", nil, nil
		default:
			return "", nil, fmt.Errorf("filter dimension %q is not available for events", dimension)
		}
	}
}

func suggestionValueSQL(builder *sqlBuilder, dimension string) (value, joins, where string, err error) {
	if strings.HasPrefix(dimension, "prop:") {
		key := strings.TrimPrefix(dimension, "prop:")
		if key == "" {
			return "", "", "", errors.New("property suggestion requires a key")
		}
		keyArg := builder.arg(key)
		return "e.props->>" + keyArg, "", " and e.props ? " + keyArg, nil
	}
	column, kind, resolveErr := eventExploreResolver("e")(dimension)
	if resolveErr != nil {
		return "", "", "", resolveErr
	}
	if kind == nil {
		return dimensionText(column, nil, dimension), "", "", nil
	}
	return "suggestion_dict.value", "join dict suggestion_dict on suggestion_dict.kind=" + strconv.Itoa(int(*kind)) + " and suggestion_dict.id=" + column, "", nil
}

// eventCursor is the sort key of the last row on a page. It contains an
// internal session ID, so it is sealed in an opaque cursor and never returned
// as a field.
type eventCursor struct {
	ts        time.Time
	sessionID int64
	kind      int16
	nameID    int32
	pathID    int32
}

func (c eventCursor) encode() string {
	raw := fmt.Sprintf("%d.%d.%d.%d.%d", c.ts.UnixMicro(), c.sessionID, c.kind, c.nameID, c.pathID)
	return base64.RawURLEncoding.EncodeToString([]byte(raw))
}

func decodeEventCursor(cursor *string) (*eventCursor, error) {
	if cursor == nil || *cursor == "" {
		return nil, nil
	}
	invalid := errors.New("invalid cursor")
	decoded, err := base64.RawURLEncoding.Strict().DecodeString(*cursor)
	if err != nil || len(decoded) > 128 {
		return nil, invalid
	}
	parts := strings.Split(string(decoded), ".")
	if len(parts) != 5 {
		return nil, invalid
	}
	var numbers [5]int64
	for i, part := range parts {
		value, parseErr := strconv.ParseInt(part, 10, 64)
		if parseErr != nil {
			return nil, invalid
		}
		numbers[i] = value
	}
	if numbers[2] < 0 || numbers[2] > 32767 || numbers[3] < -1<<31 || numbers[3] > 1<<31-1 || numbers[4] < -1<<31 || numbers[4] > 1<<31-1 {
		return nil, invalid
	}
	return &eventCursor{
		ts: time.UnixMicro(numbers[0]).UTC(), sessionID: numbers[1], kind: int16(numbers[2]),
		nameID: int32(numbers[3]), pathID: int32(numbers[4]),
	}, nil
}
