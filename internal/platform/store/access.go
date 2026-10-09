package store

import (
	"context"

	"github.com/jackc/pgx/v5"
	"panel/internal/platform/authz"
)

// CurrentMemberForUpdate revalidates a human actor while an authorized mutation
// commits. The organization lock follows the same order as membership changes.
func CurrentMemberForUpdate(ctx context.Context, tx pgx.Tx, orgID, userID int64) (authz.Principal, error) {
	var id int64
	if err := tx.QueryRow(ctx, `select id from orgs where id=$1 and deleted_at is null for share`, orgID).Scan(&id); err != nil {
		return authz.Principal{}, err
	}
	p := authz.Principal{OrgID: orgID, UserID: userID}
	err := tx.QueryRow(ctx, `select role, site_ids, capabilities, billing_admin from memberships where org_id=$1 and user_id=$2 for share`, orgID, userID).Scan(&p.Role, &p.SiteIDs, &p.Capabilities, &p.BillingAdmin)
	return p, err
}
