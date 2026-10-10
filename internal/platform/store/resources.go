package store

import (
	"context"
	"encoding/json"
	"time"

	"github.com/jackc/pgx/v5"
)

type resourceQuerier interface {
	QueryRow(context.Context, string, ...any) pgx.Row
}

// GoalRow contains the editable columns stored for a goal.
type GoalRow struct {
	Kind            string
	Match           string
	Value           string
	PropKey         string
	PropValue       string
	Name            string
	Currency        string
	ScrollThreshold *int16
}

func InsertGoal(ctx context.Context, q resourceQuerier, siteID int32, goal GoalRow) (int64, error) {
	var id int64
	err := q.QueryRow(ctx, `
		insert into goals(site_id,kind,match,value,prop_key,prop_value,name,currency,scroll_threshold)
		values($1,$2,$3,$4,nullif($5,''),nullif($6,''),$7,nullif($8,''),$9)
		returning id`, siteID, goal.Kind, goal.Match, goal.Value, goal.PropKey, goal.PropValue, goal.Name, goal.Currency, goal.ScrollThreshold).Scan(&id)
	return id, err
}

func InsertFunnel(ctx context.Context, q resourceQuerier, siteID int32, name string, steps json.RawMessage) (int64, error) {
	var id int64
	err := q.QueryRow(ctx, `insert into funnels(site_id,name,steps) values($1,$2,$3) returning id`, siteID, name, steps).Scan(&id)
	return id, err
}

func InsertAnnotation(ctx context.Context, q resourceQuerier, siteID int32, day time.Time, text string, authorID int64) (int64, error) {
	var id int64
	err := q.QueryRow(ctx, `
		insert into annotations(site_id,day,text,created_by)
		values($1,$2,$3,nullif($4,0)) returning id`, siteID, day, text, authorID).Scan(&id)
	return id, err
}

func InsertSegment(ctx context.Context, q resourceQuerier, siteID int32, name string, filters json.RawMessage, authorID int64) (int64, error) {
	var id int64
	err := q.QueryRow(ctx, `
		insert into segments(site_id,name,filters,created_by)
		values($1,$2,$3,nullif($4,0)) returning id`, siteID, name, filters, authorID).Scan(&id)
	return id, err
}
