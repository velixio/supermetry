package store

import (
	"context"
	"crypto/rand"
	"encoding/hex"
	"errors"
	"fmt"
	"strings"
	"time"
	"unicode"

	"github.com/jackc/pgx/v5"

	"supermetry/internal/platform/lifecycle"
)

type Membership struct {
	OrgID        int64
	UserID       int64
	OrgName      string
	OrgSlug      string
	Role         string
	SiteIDs      []int32
	Capabilities []string
	BillingAdmin bool
	CreatedAt    time.Time
}

type DeletedOrg struct {
	ID        int64
	Name      string
	Slug      string
	DeletedAt time.Time
	PurgeAt   time.Time
	Sites     []string
}

type orgRowsQuerier interface {
	Query(context.Context, string, ...any) (pgx.Rows, error)
}

func CreateOrg(ctx context.Context, tx pgx.Tx, name string, ownerID int64) (int64, error) {
	base := OrganizationSlug(name)
	for attempt := 0; attempt < 8; attempt++ {
		slug := base
		if attempt > 0 || IsReservedOrganizationSlug(slug) {
			suffix, err := randomSlugSuffix()
			if err != nil {
				return 0, err
			}
			slug += "-" + suffix
		}
		var orgID int64
		err := tx.QueryRow(ctx, `
			insert into orgs(name, slug) values ($1, $2)
			on conflict (slug) do nothing
			returning id`, name, slug).Scan(&orgID)
		if errors.Is(err, pgx.ErrNoRows) {
			continue
		}
		if err != nil {
			return 0, fmt.Errorf("insert org: %w", err)
		}
		if _, err := tx.Exec(ctx, `insert into memberships(org_id, user_id, role) values ($1, $2, 'owner')`, orgID, ownerID); err != nil {
			return 0, fmt.Errorf("insert owner membership: %w", err)
		}
		return orgID, nil
	}
	return 0, errors.New("generate unique organization slug")
}

func MembershipsFor(ctx context.Context, db *Store, userID int64) ([]Membership, error) {
	rows, err := db.Pool.Query(ctx, `
		select m.org_id, m.user_id, o.name, o.slug, m.role, m.site_ids, m.capabilities, m.billing_admin, m.created_at
		from memberships m
		join orgs o on o.id = m.org_id
		where m.user_id = $1 and o.deleted_at is null
		order by o.name, o.id`, userID)
	if err != nil {
		return nil, fmt.Errorf("query memberships: %w", err)
	}
	defer rows.Close()

	memberships := make([]Membership, 0)
	for rows.Next() {
		var membership Membership
		if err := rows.Scan(
			&membership.OrgID, &membership.UserID, &membership.OrgName,
			&membership.OrgSlug, &membership.Role, &membership.SiteIDs,
			&membership.Capabilities, &membership.BillingAdmin, &membership.CreatedAt,
		); err != nil {
			return nil, fmt.Errorf("scan membership: %w", err)
		}
		memberships = append(memberships, membership)
	}
	if err := rows.Err(); err != nil {
		return nil, fmt.Errorf("iterate memberships: %w", err)
	}
	return memberships, nil
}

func DeletedOrgsForOwner(ctx context.Context, q orgRowsQuerier, userID int64, now time.Time) ([]DeletedOrg, error) {
	rows, err := q.Query(ctx, `
		select o.id,o.name,o.slug,o.deleted_at,
		       coalesce(array_agg(s.name order by s.name,s.id) filter (where s.id is not null), '{}'::text[])
		from memberships m
		join orgs o on o.id=m.org_id
		left join sites s on s.org_id=o.id and not s.is_demo and s.erased_at is null
		where m.user_id=$1 and m.role='owner'
		  and o.deleted_at is not null and o.deleted_at > $2
		  and o.erased_at is null
		  and coalesce(o.deleted_by_kind, 'owner') <> 'instance_admin'
		  and coalesce(
			(select (a.metadata->>'scheduled')::boolean from audit_log a
			 where a.org_id=o.id and a.action='organization.deleted' and a.target_id=o.id
			 order by a.id desc limit 1),
			exists(select 1 from sites scheduled_site where scheduled_site.org_id=o.id and scheduled_site.deleted_with_org)
		  )
		group by o.id,o.name,o.slug,o.deleted_at
		order by o.name,o.id`, userID, now.Add(-lifecycle.DeletionGrace))
	if err != nil {
		return nil, fmt.Errorf("query deleted organizations: %w", err)
	}
	defer rows.Close()

	deleted := make([]DeletedOrg, 0)
	for rows.Next() {
		var org DeletedOrg
		if err := rows.Scan(&org.ID, &org.Name, &org.Slug, &org.DeletedAt, &org.Sites); err != nil {
			return nil, fmt.Errorf("scan deleted organization: %w", err)
		}
		org.PurgeAt = lifecycle.PurgeAt(org.DeletedAt)
		deleted = append(deleted, org)
	}
	if err := rows.Err(); err != nil {
		return nil, fmt.Errorf("iterate deleted organizations: %w", err)
	}
	return deleted, nil
}

// OrganizationSlug converts a display name into the canonical URL segment.
func OrganizationSlug(name string) string {
	var slug strings.Builder
	lastDash := false
	for _, r := range strings.ToLower(strings.TrimSpace(name)) {
		if unicode.IsLetter(r) || unicode.IsDigit(r) {
			slug.WriteRune(r)
			lastDash = false
			continue
		}
		if slug.Len() > 0 && !lastDash {
			slug.WriteByte('-')
			lastDash = true
		}
	}
	value := strings.Trim(slug.String(), "-")
	if value == "" {
		return "org"
	}
	return value
}

// IsReservedOrganizationSlug reports whether the application router owns a
// first path segment that organizations must never claim.
func IsReservedOrganizationSlug(slug string) bool {
	switch strings.ToLower(strings.TrimSpace(slug)) {
	case "account", "admin", "api", "auth", "invite", "login", "logout", "password-reset", "reset", "setup", "share", "signup":
		return true
	default:
		return false
	}
}

func randomSlugSuffix() (string, error) {
	var value [4]byte
	if _, err := rand.Read(value[:]); err != nil {
		return "", fmt.Errorf("generate organization slug: %w", err)
	}
	return hex.EncodeToString(value[:]), nil
}
