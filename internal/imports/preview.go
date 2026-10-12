package imports

import (
	"context"
	"errors"
	"time"

	"github.com/jackc/pgx/v5"
)

// Totals are the headline sums a dry run reports.
type Totals struct {
	Visitors  int64 `json:"visitors"`
	Visits    int64 `json:"visits"`
	Pageviews int64 `json:"pageviews"`
	Events    int64 `json:"events"`
}

// Summary describes what an import would add, without storing anything.
type Summary struct {
	Rows          int64          `json:"rows"`
	BadRows       int64          `json:"bad_rows"`
	DuplicateRows int64          `json:"duplicate_rows"`
	Errors        []RowError     `json:"errors"`
	Days          int            `json:"days"`
	DateFrom      string         `json:"date_from,omitempty"`
	DateTo        string         `json:"date_to,omitempty"`
	Totals        Totals         `json:"totals"`
	Dimensions    map[string]int `json:"dimensions"`
}

var dimensionNames = map[int16]string{
	DimEvent: "event", DimPage: "page", DimSource: "source", DimCountry: "country",
	DimBrowser: "browser", DimOS: "os", DimDevice: "device", DimEntry: "entry_page",
	DimExit: "exit_page", DimRegion: "region", DimCity: "city",
}

// Summarize condenses a parsed aggregate. Totals come from the daily rows;
// when a file has only dimension rows, pageviews and events are summed from
// the page and event rows instead so the preview never shows an empty file.
func Summarize(aggregate Aggregate, stats ParseStats) Summary {
	summary := Summary{
		Rows: stats.Rows, BadRows: stats.BadRows, DuplicateRows: stats.DuplicateRows,
		Errors: append([]RowError{}, stats.Errors...), Dimensions: map[string]int{},
	}
	days := make(map[time.Time]struct{})
	for day, metrics := range aggregate.Days {
		days[day] = struct{}{}
		summary.Totals.Visitors += metrics.Visitors
		summary.Totals.Visits += metrics.Visits
		summary.Totals.Pageviews += metrics.Pageviews
		summary.Totals.Events += metrics.Events
	}
	var pagePageviews, eventCount int64
	values := make(map[int16]map[string]struct{})
	for key, metrics := range aggregate.Dimensions {
		days[key.Day] = struct{}{}
		if values[key.Dim] == nil {
			values[key.Dim] = make(map[string]struct{})
		}
		values[key.Dim][key.Value] = struct{}{}
		switch key.Dim {
		case DimPage:
			pagePageviews += metrics.Pageviews
		case DimEvent:
			eventCount += metrics.Events
		}
	}
	if len(aggregate.Days) == 0 {
		summary.Totals.Pageviews = pagePageviews
	}
	if summary.Totals.Events == 0 {
		summary.Totals.Events = eventCount
	}
	for dim, set := range values {
		summary.Dimensions[dimensionNames[dim]] = len(set)
	}
	summary.Days = len(days)
	if from, to := aggregateRange(aggregate); from != nil {
		summary.DateFrom, summary.DateTo = from.Format(time.DateOnly), to.Format(time.DateOnly)
	}
	return summary
}

// Overlap describes how a previewed file relates to data already stored.
type Overlap struct {
	// ImportedDays counts days a completed import already covers; the newest
	// import wins per day, so confirming replaces those days.
	ImportedDays int `json:"imported_days"`
	// LiveDays counts days that already have live data; live data always wins.
	LiveDays int `json:"live_days"`
	// SameFile is set when this exact file was imported before.
	SameFile *PriorImport `json:"same_file"`
}

type PriorImport struct {
	ImportID   int64     `json:"import_id"`
	ImportedAt time.Time `json:"imported_at"`
}

// Preview checks a parsed aggregate against stored data. It writes nothing.
func (s *Store) Preview(ctx context.Context, siteID int32, fileHash [32]byte, aggregate Aggregate) (Overlap, error) {
	var result Overlap
	if fileHash != ([32]byte{}) {
		var prior PriorImport
		err := s.pool.QueryRow(ctx, `
			select id,created_at from imports
			where site_id=$1 and file_hash=$2 and status <> 'failed'
			order by created_at desc,id desc limit 1`, siteID, fileHash[:]).Scan(&prior.ImportID, &prior.ImportedAt)
		if err == nil {
			result.SameFile = &prior
		} else if !errors.Is(err, pgx.ErrNoRows) {
			return result, err
		}
	}
	days := make(map[time.Time]struct{})
	for day := range aggregate.Days {
		days[day] = struct{}{}
	}
	for key := range aggregate.Dimensions {
		days[key.Day] = struct{}{}
	}
	if len(days) == 0 {
		return result, nil
	}
	list := make([]time.Time, 0, len(days))
	for day := range days {
		list = append(list, day)
	}
	if err := s.pool.QueryRow(ctx, `
		select count(*) from (
			select d.day from imported_day d join imports i on i.id=d.import_id
			where d.site_id=$1 and i.status='completed' and d.day=any($2)
			union
			select d.day from imported_day_dim d join imports i on i.id=d.import_id
			where d.site_id=$1 and i.status='completed' and d.day=any($2)
		) covered`, siteID, list).Scan(&result.ImportedDays); err != nil {
		return result, err
	}
	err := s.pool.QueryRow(ctx, `
		select count(distinct day) from rollup_day where site_id=$1 and day=any($2)`, siteID, list).Scan(&result.LiveDays)
	return result, err
}
