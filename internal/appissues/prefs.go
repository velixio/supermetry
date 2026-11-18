package appissues

import (
	"context"
	"errors"
	"fmt"

	"github.com/jackc/pgx/v5"
	"github.com/jackc/pgx/v5/pgxpool"
)

// Prefs are one user's workflow-notification switches for one project.
type Prefs struct {
	Assigned  bool `json:"assigned"`
	Resolved  bool `json:"resolved"`
	Regressed bool `json:"regressed"`
	// WeeklyDigest mails the Monday digest of the week that just ended.
	WeeklyDigest bool `json:"weekly_digest"`
}

// DefaultPrefs apply until a user saves their own.
var DefaultPrefs = Prefs{Assigned: true, Resolved: false, Regressed: true, WeeklyDigest: false}

// SitePrefs is a project the user can read, with their switches for it.
type SitePrefs struct {
	SiteID  int32  `json:"site_id"`
	Site    string `json:"site_name"`
	OrgSlug string `json:"org_slug"`
	OrgName string `json:"org_name"`
	Slug    string `json:"site_slug"`
	Prefs
}

// accessSQL is true when the user is a member of the project's organisation
// with access to the project ($1 user, $2 site).
const accessSQL = `exists (select 1 from memberships m join sites s on s.org_id = m.org_id
where s.id = $2 and m.user_id = $1 and (m.site_ids is null or $2 = any(m.site_ids)))`

// ListPrefs returns the user's app projects with their switches (the
// defaults where none are saved). Only projects the user can read appear.
func ListPrefs(ctx context.Context, pool *pgxpool.Pool, userID int64) ([]SitePrefs, error) {
	rows, err := pool.Query(ctx, `
select s.id, s.name, o.slug, o.name, s.slug,
  coalesce(p.assigned, $2), coalesce(p.resolved, $3), coalesce(p.regressed, $4), coalesce(p.weekly_digest, $5)
from memberships m
join sites s on s.org_id = m.org_id
join orgs o on o.id = s.org_id
left join user_notification_prefs p on p.user_id = m.user_id and p.site_id = s.id
where m.user_id = $1 and s.kind = 'app' and s.deleted_at is null and o.deleted_at is null
  and (m.site_ids is null or s.id = any(m.site_ids))
order by o.name, s.name, s.id`, userID, DefaultPrefs.Assigned, DefaultPrefs.Resolved, DefaultPrefs.Regressed, DefaultPrefs.WeeklyDigest)
	if err != nil {
		return nil, fmt.Errorf("list notification prefs: %w", err)
	}
	defer rows.Close()
	out := []SitePrefs{}
	for rows.Next() {
		var sp SitePrefs
		if err := rows.Scan(&sp.SiteID, &sp.Site, &sp.OrgSlug, &sp.OrgName, &sp.Slug, &sp.Assigned, &sp.Resolved, &sp.Regressed, &sp.WeeklyDigest); err != nil {
			return nil, err
		}
		out = append(out, sp)
	}
	return out, rows.Err()
}

// SetPrefs saves the user's own switches for a project they can read, and
// returns ErrNotFound for any other project (another organisation's, or one
// outside the user's site list).
func SetPrefs(ctx context.Context, pool *pgxpool.Pool, userID int64, siteID int32, p Prefs) error {
	var ok bool
	if err := pool.QueryRow(ctx, `select `+accessSQL+` and exists (select 1 from sites where id = $2 and kind = 'app' and deleted_at is null)`, userID, siteID).Scan(&ok); err != nil {
		return fmt.Errorf("check site access: %w", err)
	}
	if !ok {
		return ErrNotFound
	}
	_, err := pool.Exec(ctx, `
insert into user_notification_prefs (user_id, site_id, assigned, resolved, regressed) values ($1, $2, $3, $4, $5)
on conflict (user_id, site_id) do update set assigned = excluded.assigned, resolved = excluded.resolved, regressed = excluded.regressed`,
		userID, siteID, p.Assigned, p.Resolved, p.Regressed)
	return err
}

// SetWeeklyDigest switches the user's own weekly digest for a project they can
// read; any other project is ErrNotFound. Turning it on does not mail the week
// in progress: the digest goes out on the next Monday morning.
func SetWeeklyDigest(ctx context.Context, pool *pgxpool.Pool, userID int64, siteID int32, on bool) error {
	var ok bool
	if err := pool.QueryRow(ctx, `select `+accessSQL+` and exists (select 1 from sites where id = $2 and kind = 'app' and deleted_at is null)`, userID, siteID).Scan(&ok); err != nil {
		return fmt.Errorf("check site access: %w", err)
	}
	if !ok {
		return ErrNotFound
	}
	_, err := pool.Exec(ctx, `
insert into user_notification_prefs (user_id, site_id, weekly_digest) values ($1, $2, $3)
on conflict (user_id, site_id) do update set weekly_digest = excluded.weekly_digest`, userID, siteID, on)
	return err
}

// GetPrefs returns the user's switches for a project (the defaults where none
// are saved).
func GetPrefs(ctx context.Context, pool *pgxpool.Pool, userID int64, siteID int32) (Prefs, error) {
	return getPrefs(ctx, pool, userID, siteID)
}

func getPrefs(ctx context.Context, q interface {
	QueryRow(context.Context, string, ...any) pgx.Row
}, userID int64, siteID int32) (Prefs, error) {
	p := DefaultPrefs
	err := q.QueryRow(ctx, `select assigned, resolved, regressed, weekly_digest from user_notification_prefs where user_id = $1 and site_id = $2`, userID, siteID).Scan(&p.Assigned, &p.Resolved, &p.Regressed, &p.WeeklyDigest)
	if errors.Is(err, pgx.ErrNoRows) {
		return DefaultPrefs, nil
	}
	return p, err
}
