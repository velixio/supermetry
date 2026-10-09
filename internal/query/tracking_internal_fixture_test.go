package query

import (
	"github.com/jackc/pgx/v5/pgxpool"
	"panel/internal/dict"
	"panel/internal/privacy"
	"panel/internal/sites"
)

func identifiedEngine(pool *pgxpool.Pool, dictionary *dict.Dict, registry sites.Registry, definitions ...GoalLookup) *Engine {
	engine := NewEngine(pool, dictionary, registry, definitions...)
	engine.SetTrackingMode(privacy.Identified)
	return engine
}
