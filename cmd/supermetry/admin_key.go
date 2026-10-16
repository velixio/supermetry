package main

import (
	"context"
	"errors"
	"fmt"
	"log/slog"

	"github.com/jackc/pgx/v5"

	platformhttp "supermetry/internal/platform/http"
	"supermetry/internal/platform/store"
)

// adminKeyServiceUser returns the system user the admin API key acts as on the
// instance admin routes, or 0 when key auth there stays off: no key, a key
// shorter than platformhttp.MinAdminAPIKeyLength, or no system user row.
func adminKeyServiceUser(ctx context.Context, db store.DBTX, key string) (int64, error) {
	if key == "" {
		return 0, nil
	}
	if len(key) < platformhttp.MinAdminAPIKeyLength {
		slog.Warn("SUPERMETRY_ADMIN_API_KEY is shorter than the minimum length; it is not accepted on the instance admin API", "min_length", platformhttp.MinAdminAPIKeyLength)
		return 0, nil
	}
	id, err := store.AdminServiceUserID(ctx, db)
	if errors.Is(err, pgx.ErrNoRows) {
		slog.Warn("the admin API key system user is missing; the key is not accepted on the instance admin API")
		return 0, nil
	}
	if err != nil {
		return 0, fmt.Errorf("load admin API key system user: %w", err)
	}
	return id, nil
}
