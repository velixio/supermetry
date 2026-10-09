package query_test

import (
	"github.com/jackc/pgx/v5/pgxpool"
	"panel/internal/dict"
	"panel/internal/privacy"
	"panel/internal/query"
	"panel/internal/sites"
)

func identifiedEngine(pool *pgxpool.Pool, dictionary *dict.Dict, registry sites.Registry, definitions ...query.GoalLookup) *query.Engine {
	engine := query.NewEngine(pool, dictionary, registry, definitions...)
	engine.SetTrackingMode(privacy.Identified)
	return engine
}
