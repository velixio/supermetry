package store

import (
	"context"
	"fmt"
)

type Goal struct {
	ID              int64
	SiteID          int32
	Kind            string
	Match           string
	Value           string
	PropKey         string
	PropValue       string
	Name            string
	Currency        string
	ScrollThreshold int16
}

type Funnel struct {
	ID     int64
	SiteID int32
	Name   string
	Steps  []byte
}

func ActiveGoals(ctx context.Context, data *Store) ([]Goal, error) {
	rows, err := data.Pool.Query(ctx, `
		select g.id, g.site_id, g.kind, g.match, g.value,
		       coalesce(g.prop_key, ''), coalesce(g.prop_value, ''), g.name,
		       coalesce(g.currency, ''), coalesce(g.scroll_threshold, 0)
		from goals g
		join sites s on s.id = g.site_id
		where s.deleted_at is null
		order by g.site_id, g.id`)
	if err != nil {
		return nil, fmt.Errorf("query active goals: %w", err)
	}
	defer rows.Close()

	goals := make([]Goal, 0)
	for rows.Next() {
		var goal Goal
		if err := rows.Scan(
			&goal.ID, &goal.SiteID, &goal.Kind, &goal.Match, &goal.Value,
			&goal.PropKey, &goal.PropValue, &goal.Name, &goal.Currency, &goal.ScrollThreshold,
		); err != nil {
			return nil, fmt.Errorf("scan goal: %w", err)
		}
		goals = append(goals, goal)
	}
	if err := rows.Err(); err != nil {
		return nil, fmt.Errorf("iterate goals: %w", err)
	}
	return goals, nil
}

func ActiveFunnels(ctx context.Context, data *Store) ([]Funnel, error) {
	rows, err := data.Pool.Query(ctx, `
		select f.id, f.site_id, f.name, f.steps
		from funnels f
		join sites s on s.id = f.site_id
		where s.deleted_at is null
		order by f.site_id, f.id`)
	if err != nil {
		return nil, fmt.Errorf("query active funnels: %w", err)
	}
	defer rows.Close()

	funnels := make([]Funnel, 0)
	for rows.Next() {
		var funnel Funnel
		if err := rows.Scan(&funnel.ID, &funnel.SiteID, &funnel.Name, &funnel.Steps); err != nil {
			return nil, fmt.Errorf("scan funnel: %w", err)
		}
		funnels = append(funnels, funnel)
	}
	if err := rows.Err(); err != nil {
		return nil, fmt.Errorf("iterate funnels: %w", err)
	}
	return funnels, nil
}
