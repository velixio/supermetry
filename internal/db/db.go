package db

import (
	"context"
	"errors"
	"fmt"
	"net"

	"github.com/jackc/pgx/v5/pgxpool"
)

// Open creates and verifies a PostgreSQL connection pool.
func Open(ctx context.Context, dsn string) (*pgxpool.Pool, error) {
	config, err := pgxpool.ParseConfig(dsn)
	if err != nil {
		return nil, fmt.Errorf("parse database URL: %w", err)
	}
	config.ConnConfig.RuntimeParams["application_name"] = "supermetry"

	pool, err := pgxpool.NewWithConfig(ctx, config)
	if err != nil {
		return nil, fmt.Errorf("open database pool: %w", err)
	}
	if err := pool.Ping(ctx); err != nil {
		pool.Close()
		return nil, fmt.Errorf("ping database: %w%s", err, connectionHint(config.ConnConfig.Host, err))
	}
	return pool, nil
}

// connectionHint explains the one failure the Docker Compose setup produces on
// its own: the app points at the bundled database's host name, but that
// container was not started.
func connectionHint(host string, err error) string {
	var dnsErr *net.DNSError
	if host == "postgres" && errors.As(err, &dnsErr) && dnsErr.IsNotFound {
		return "\nhint: the host \"postgres\" is the bundled database. Start it with `docker compose --profile bundled-db up -d`, or set SUPERMETRY_DATABASE_URL to your own database"
	}
	return ""
}
