package ghtrending

import (
	"context"
	"fmt"
	"time"

	"github.com/jackc/pgx/v5/pgxpool"
)

// pgAnnotator writes chart markers into the star tracker's annotation table,
// which is keyed by the repository row id. This is the one place that knows
// that; it is the only code to adapt if the key changes.
type pgAnnotator struct{ pool *pgxpool.Pool }

func (a pgAnnotator) Annotate(ctx context.Context, repoID int64, at time.Time, label string) (int64, error) {
	var id int64
	err := a.pool.QueryRow(ctx, `
		insert into github_star_annotations(repo_id, at, label) values ($1, $2, $3)
		returning id`, repoID, at, label).Scan(&id)
	if err != nil {
		return 0, fmt.Errorf("insert trending annotation: %w", err)
	}
	return id, nil
}
