//go:build !benchtrace

package main

import (
	"context"
	"errors"
	"net/http"

	"github.com/jackc/pgx/v5/pgxpool"
)

func configureTrace(*pgxpool.Config)                 {}
func traceMiddleware(next http.Handler) http.Handler { return next }
func recordQuery(context.Context, float64)           {}
func explainCaptured(context.Context, *pgxpool.Pool, int, string) error {
	return errors.New("--explain requires a binary built with -tags benchtrace")
}
