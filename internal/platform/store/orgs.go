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
)

type Membership struct {
	OrgID     int64
	UserID    int64
	OrgName   string
	OrgSlug   string
	Role      string
	SiteIDs   []int32
	CreatedAt time.Time
}

func CreateOrg(ctx context.Context, tx pgx.Tx, name string, ownerID int64) (int64, error) {
	base := slugify(name)
	for attempt := 0; attempt < 8; attempt++ {
		slug := base
		if attempt > 0 {
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
		select m.org_id, m.user_id, o.name, o.slug, m.role, m.site_ids, m.created_at
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
			&membership.CreatedAt,
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

func slugify(name string) string {
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

func randomSlugSuffix() (string, error) {
	var value [4]byte
	if _, err := rand.Read(value[:]); err != nil {
		return "", fmt.Errorf("generate organization slug: %w", err)
	}
	return hex.EncodeToString(value[:]), nil
}
