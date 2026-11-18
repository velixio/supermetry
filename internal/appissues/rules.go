package appissues

import (
	"context"
	"errors"
	"fmt"

	"github.com/jackc/pgx/v5"
)

// AssignOwner applies an ownership rule to a just-created issue: it sets the
// assignee and records an `assigned` activity by the system. It never
// re-assigns (an issue that already has an assignee is left alone) and it
// skips an owner who is no longer a member with access to the site. It runs
// only when the writer creates an issue, so it adds no cost to events of
// existing issues.
func AssignOwner(ctx context.Context, tx pgx.Tx, siteID int32, issueID, owner int64) error {
	var assigned bool
	err := tx.QueryRow(ctx, `update app_issues i set assignee_user_id = $3
where i.id = $2 and i.site_id = $1 and i.assignee_user_id is null
  and exists (select 1 from memberships m join sites s on s.org_id = m.org_id
              where s.id = $1 and m.user_id = $3 and (m.site_ids is null or $1 = any(m.site_ids)))
returning true`, siteID, issueID, owner).Scan(&assigned)
	if errors.Is(err, pgx.ErrNoRows) {
		return nil
	}
	if err != nil {
		return fmt.Errorf("assign owner: %w", err)
	}
	return RecordSystem(ctx, tx, siteID, issueID, KindAssigned, map[string]any{"assignee": owner, "rule": "ownership"})
}
