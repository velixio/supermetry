package query

import (
	"github.com/jackc/pgx/v5/pgxpool"
	"supermetry/internal/dict"
	"supermetry/internal/privacy"
	"supermetry/internal/sites"
)

func identifiedEngine(pool *pgxpool.Pool, dictionary *dict.Dict, registry sites.Registry, definitions ...GoalLookup) *Engine {
	engine := NewEngine(pool, dictionary, registry, definitions...)
	engine.SetTrackingMode(privacy.Identified)
	return engine
}
