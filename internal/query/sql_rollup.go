package query

import (
	"context"
	"fmt"
	"strconv"
	"strings"
	"time"

	"github.com/jackc/pgx/v5"

	"panel/internal/api"
	"panel/internal/dict"
)

var rollupDimensionCodes = map[string]int16{
	"page": 1, "path": 1, "source": 2, "country": 3, "browser": 4,
	"os": 5, "device": 6, "entry": 7, "exit": 8, "region": 9, "city": 10,
}

func (e *Engine) queryRollup(ctx context.Context, req Normalized, part Part) ([]resultRow, error) {
	if len(req.Filters) != 0 || len(req.Dimensions) > 1 {
		return nil, fmt.Errorf("rollups cannot answer this query shape exactly")
	}
	builder := &sqlBuilder{}
	siteArg := builder.arg(req.SiteID)
	baseSums := []string{
		"coalesce(sum(visitors),0)::int8", "coalesce(sum(visits),0)::int8",
		"coalesce(sum(pageviews),0)::int8", "coalesce(sum(bounces),0)::int8",
		"coalesce(sum(duration_ms),0)::int8", "coalesce(sum(engagement_ms),0)::int8",
		"coalesce(sum(exits),0)::int8", "coalesce(sum(engaged_n),0)::int8",
		"coalesce(sum(scroll_sum),0)::int8", "coalesce(sum(scroll_n),0)::int8", "0::int8", "0::int8", "0::int8", "0::int8",
	}
	if len(req.Dimensions) == 0 {
		fromArg := builder.arg(part.From)
		toArg := builder.arg(part.To)
		totalSums := append([]string(nil), baseSums...)
		totalSums[6], totalSums[7] = "0::int8", "0::int8"
		querySQL := aggregateSQL(nil, totalSums, "rollup_day", "site_id="+siteArg+" and day between "+fromArg+"::date and "+toArg+"::date")
		return e.executeAggregate(ctx, querySQL, builder.args, nil)
	}
	if req.Dimensions[0] == "time" {
		fromArg := builder.arg(part.From)
		toArg := builder.arg(part.To)
		timeSums := append([]string(nil), baseSums...)
		timeSums[6], timeSums[7] = "0::int8", "0::int8"
		querySQL := aggregateSQL([]string{"to_char(day, 'YYYY-MM-DD')"}, timeSums, "rollup_day", "site_id="+siteArg+" and day between "+fromArg+"::date and "+toArg+"::date")
		return e.executeAggregate(ctx, querySQL, builder.args, req.Dimensions)
	}
	code, ok := rollupDimensionCodes[req.Dimensions[0]]
	if !ok {
		return nil, fmt.Errorf("dimension %q is unavailable from rollups", req.Dimensions[0])
	}
	done, err := e.completedDayBlocks(ctx, req.SiteID)
	if err != nil {
		return nil, err
	}
	blocks := availableDayBlocks(part.From, part.To, done)
	var daily, starts []time.Time
	var levels []int16
	for _, block := range blocks {
		if block.level == 0 {
			daily = append(daily, block.start)
			continue
		}
		levels = append(levels, block.level)
		starts = append(starts, block.start)
	}
	dimArg := builder.arg(code)
	const columns = "value_id, visitors, visits, pageviews, bounces, duration_ms, engagement_ms, engaged_n, scroll_sum, scroll_n"
	var inputs []string
	dimension := req.Dimensions[0]
	if len(daily) > 0 {
		daysArg := builder.arg(daily)
		switch dimension {
		case "page", "path", "entry":
			inputs = append(inputs, `select p.`+strings.ReplaceAll(columns, ", ", ", p.")+`, coalesce(x.visits,0)::int8 as exits
from rollup_day_dim p
left join rollup_day_dim x on x.site_id=p.site_id and x.day=p.day and x.dim=8 and x.value_id=p.value_id
where p.site_id=`+siteArg+` and p.dim=`+dimArg+` and p.day=any(`+daysArg+`::date[])`)
		case "exit":
			inputs = append(inputs, "select "+columns+", visits::int8 as exits from rollup_day_dim where site_id="+siteArg+" and dim="+dimArg+" and day=any("+daysArg+"::date[])")
		default:
			inputs = append(inputs, "select "+columns+", 0::int8 as exits from rollup_day_dim where site_id="+siteArg+" and dim="+dimArg+" and day=any("+daysArg+"::date[])")
		}
	}
	if len(starts) > 0 {
		levelArg, startArg := builder.arg(levels), builder.arg(starts)
		switch dimension {
		case "page", "path", "entry":
			inputs = append(inputs, `select p.`+strings.ReplaceAll(columns, ", ", ", p.")+`, coalesce(x.visits,0)::int8 as exits
from rollup_block_dim p
join unnest(`+levelArg+`::int2[], `+startArg+`::date[]) wanted(k,start_day)
	  on p.k=wanted.k and p.start_day=wanted.start_day
left join rollup_block_dim x on x.site_id=p.site_id and x.k=p.k and x.start_day=p.start_day and x.dim=8 and x.value_id=p.value_id
where p.site_id=`+siteArg+` and p.dim=`+dimArg)
		case "exit":
			inputs = append(inputs, `select `+columns+`, visits::int8 as exits from rollup_block_dim r
join unnest(`+levelArg+`::int2[], `+startArg+`::date[]) wanted(k,start_day)
  on r.k=wanted.k and r.start_day=wanted.start_day
where r.site_id=`+siteArg+` and r.dim=`+dimArg)
		default:
			inputs = append(inputs, `select `+columns+`, 0::int8 as exits from rollup_block_dim r
join unnest(`+levelArg+`::int2[], `+startArg+`::date[]) wanted(k,start_day)
  on r.k=wanted.k and r.start_day=wanted.start_day
where r.site_id=`+siteArg+` and r.dim=`+dimArg)
		}
	}
	if len(inputs) == 0 {
		inputs = append(inputs, "select "+columns+", 0::int8 as exits from rollup_day_dim where false")
	}
	dimensionText := rollupValueText("value_id", req.Dimensions[0])
	where := "true"
	if req.Dimensions[0] == "region" || req.Dimensions[0] == "city" {
		where = "value_id<>0"
	}
	querySQL := aggregateSQL([]string{dimensionText}, baseSums, "("+strings.Join(inputs, " union all ")+") r", where)
	return e.executeAggregate(ctx, querySQL, builder.args, req.Dimensions)
}

func rollupValueText(column, dimension string) string {
	switch dimension {
	case "country":
		return "case when " + column + "=0 then '' else chr(" + column + " / 256) || chr(" + column + " % 256) end"
	case "device":
		return dimensionText(column, nil, "device")
	default:
		kind := dict.Path
		switch dimension {
		case "source":
			kind = dict.Source
		case "browser":
			kind = dict.Browser
		case "os":
			kind = dict.OS
		case "region":
			kind = dict.Region
		case "city":
			kind = dict.City
		}
		return "coalesce((select value from dict where kind=" + strconv.Itoa(int(kind)) + " and id=" + column + "), '')"
	}
}

func (e *Engine) queryVisitorDay(ctx context.Context, req Normalized, part Part) ([]resultRow, error) {
	return e.queryClosedChunks(ctx, req, part, buildVisitorDaySQL, "visitor_day")
}

func buildVisitorDaySQL(req Normalized, part Part) (string, []any, error) {
	builder := &sqlBuilder{}
	resolver := compactResolver("v", map[string]string{
		"country": "country", "browser": "browser_id", "os": "os_id", "device": "device", "time": "day",
	})
	dimensions, err := dimensionExpressions(req.Dimensions, resolver)
	if err != nil {
		return "", nil, err
	}
	conditions, err := filterConditions(builder, req.Filters, resolver)
	if err != nil {
		return "", nil, err
	}
	where := "v.site_id=" + builder.arg(req.SiteID) + " and v.day between " + builder.arg(part.From) + "::date and " + builder.arg(part.To) + "::date" + andSQL(conditions)
	sums := additiveColumnSums("v", false)
	return aggregateSQL(dimensions, sums, "visitor_day v", where), builder.args, nil
}

func (e *Engine) queryCube(ctx context.Context, req Normalized, part Part) ([]resultRow, error) {
	return e.queryClosedChunks(ctx, req, part, buildCubeSQL, "cube_day")
}

func buildCubeSQL(req Normalized, part Part) (string, []any, error) {
	builder := &sqlBuilder{}
	resolver := compactResolver("c", map[string]string{
		"source": "source_id", "country": "country", "browser": "browser_id", "os": "os_id", "device": "device", "time": "day",
	})
	dimensions, err := dimensionExpressions(req.Dimensions, resolver)
	if err != nil {
		return "", nil, err
	}
	conditions, err := filterConditions(builder, req.Filters, resolver)
	if err != nil {
		return "", nil, err
	}
	where := "c.site_id=" + builder.arg(req.SiteID) + " and c.day between " + builder.arg(part.From) + "::date and " + builder.arg(part.To) + "::date" + andSQL(conditions)
	visitorColumn := "v_vd"
	if containsAny(req.Dimensions, "source") || filterHasDimension(req.Filters, "source") {
		visitorColumn = "v_src"
	}
	sums := additiveColumnSums("c", true)
	sums[0] = "coalesce(sum(c." + visitorColumn + "),0)::int8"
	return aggregateSQL(dimensions, sums, "cube_day c", where), builder.args, nil
}

func compactResolver(alias string, columns map[string]string) dimensionResolver {
	return func(dimension string) (string, *dict.Kind, error) {
		column, ok := columns[dimension]
		if !ok {
			return "", nil, fmt.Errorf("dimension %q is unavailable from compacted cube", dimension)
		}
		var kind *dict.Kind
		switch dimension {
		case "source":
			kind = dictionaryKind(dict.Source)
		case "browser":
			kind = dictionaryKind(dict.Browser)
		case "os":
			kind = dictionaryKind(dict.OS)
		}
		return alias + "." + column, kind, nil
	}
}

func additiveColumnSums(alias string, hasScroll bool) []string {
	prefix := alias + "."
	scrollSum, scrollN := "0::int8", "0::int8"
	if hasScroll {
		scrollSum = "coalesce(sum(" + prefix + "scroll_sum),0)::int8"
		scrollN = "coalesce(sum(" + prefix + "scroll_n),0)::int8"
	}
	return []string{
		"coalesce(sum(" + prefix + "visitors),0)::int8",
		"coalesce(sum(" + prefix + "visits),0)::int8",
		"coalesce(sum(" + prefix + "pageviews),0)::int8",
		"coalesce(sum(" + prefix + "bounces),0)::int8",
		"coalesce(sum(" + prefix + "duration_ms),0)::int8",
		"coalesce(sum(" + prefix + "engagement_ms),0)::int8",
		"0::int8", "0::int8", scrollSum, scrollN, "0::int8", "0::int8", "0::int8", "0::int8",
	}
}

func filterHasDimension(filters []api.Filter, dimension string) bool {
	for _, filter := range filters {
		if filter.Dimension == dimension {
			return true
		}
	}
	return false
}

type dayBlock struct {
	start time.Time
	level int16
}

func (b dayBlock) end() time.Time {
	return b.start.AddDate(0, 0, (1<<b.level)-1)
}

func (e *Engine) completedDayBlocks(ctx context.Context, siteID int32) (map[dayBlock]bool, error) {
	done := make(map[dayBlock]bool)
	err := WithStatementTimeout(ctx, e.pool, queryTimeout, func(tx pgx.Tx) error {
		rows, queryErr := tx.Query(ctx, `select k, start_day from rollup_block_done where site_id=$1`, siteID)
		if queryErr != nil {
			return queryErr
		}
		defer rows.Close()
		for rows.Next() {
			var block dayBlock
			if scanErr := rows.Scan(&block.level, &block.start); scanErr != nil {
				return scanErr
			}
			block.start = civilDate(block.start)
			done[block] = true
		}
		return rows.Err()
	})
	if err != nil {
		return nil, fmt.Errorf("query completed rollup blocks: %w", err)
	}
	return done, nil
}

func availableDayBlocks(from, to time.Time, done map[dayBlock]bool) []dayBlock {
	var result []dayBlock
	var add func(dayBlock)
	add = func(block dayBlock) {
		if done[block] {
			result = append(result, block)
			return
		}
		if block.level == 0 {
			return
		}
		left := dayBlock{start: block.start, level: block.level - 1}
		add(left)
		add(dayBlock{start: left.end().AddDate(0, 0, 1), level: left.level})
	}
	for _, block := range decomposeDayRange(from, to) {
		add(block)
	}
	return result
}

func decomposeDayRange(from, to time.Time) []dayBlock {
	from, to = civilDate(from), civilDate(to)
	var result []dayBlock
	for day := from; !day.After(to); {
		epochDay := day.Unix() / int64((24 * time.Hour).Seconds())
		level := int16(0)
		for candidate := int16(1); candidate <= 9; candidate++ {
			size := int64(1) << candidate
			end := day.AddDate(0, 0, int(size)-1)
			if epochDay%size != 0 || end.After(to) {
				break
			}
			level = candidate
		}
		block := dayBlock{start: day, level: level}
		result = append(result, block)
		day = block.end().AddDate(0, 0, 1)
	}
	return result
}
