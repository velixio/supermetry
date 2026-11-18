package orgbundle

import (
	"context"
	"encoding/json"
	"fmt"
	"time"

	"github.com/jackc/pgx/v5"
	"github.com/jackc/pgx/v5/pgxpool"

	"supermetry/internal/platform/audit"
	"supermetry/internal/platform/authz"
)

// Service exports and imports organization settings.
type Service struct {
	pool  *pgxpool.Pool
	sites SiteCreator
	inv   Inviter
	now   func() time.Time
	// Refresh is called after an import changed sites (registry invalidation).
	Refresh func() error
}

func New(pool *pgxpool.Pool, sites SiteCreator, inviter Inviter, now func() time.Time) *Service {
	if now == nil {
		now = time.Now
	}
	return &Service{pool: pool, sites: sites, inv: inviter, now: now}
}

func nz[T any](s []T) []T {
	if s == nil {
		return []T{}
	}
	return s
}

// Export builds the bundle of the principal's organization and audits it.
func (s *Service) Export(ctx context.Context, principal authz.Principal) (Bundle, error) {
	b := Bundle{SchemaVersion: SchemaVersion, Kind: Kind, GeneratedAt: s.now().UTC().Format(time.RFC3339),
		Channels: []Channel{}, Sites: []Site{}, Members: []Member{}, Skipped: []SkippedEntry{}}
	err := pgx.BeginTxFunc(ctx, s.pool, pgx.TxOptions{AccessMode: pgx.ReadWrite}, func(tx pgx.Tx) error {
		if err := tx.QueryRow(ctx, `select name,slug from orgs where id=$1 and deleted_at is null`, principal.OrgID).Scan(&b.Org.Name, &b.Org.Slug); err != nil {
			return err
		}
		if err := s.loadChannels(ctx, tx, principal.OrgID, &b); err != nil {
			return err
		}
		if err := s.loadSites(ctx, tx, principal.OrgID, &b); err != nil {
			return err
		}
		if err := s.loadMembers(ctx, tx, principal.OrgID, &b); err != nil {
			return err
		}
		return audit.Write(ctx, tx, principal, "org.settings_exported", "organization", principal.OrgID, map[string]any{"sites": len(b.Sites), "members": len(b.Members)})
	})
	return b, err
}

func (s *Service) loadChannels(ctx context.Context, tx pgx.Tx, orgID int64, b *Bundle) error {
	rows, err := tx.Query(ctx, `select kind,name,enabled from notification_channels where org_id=$1 order by id`, orgID)
	if err != nil {
		return err
	}
	defer rows.Close()
	for rows.Next() {
		var c Channel
		if err := rows.Scan(&c.Kind, &c.Name, &c.Enabled); err != nil {
			return err
		}
		b.Channels = append(b.Channels, c)
	}
	return rows.Err()
}

func (s *Service) loadSites(ctx context.Context, tx pgx.Tx, orgID int64, b *Bundle) error {
	rows, err := tx.Query(ctx, `select id,name,slug,kind,timezone,retention_class,allowed_hosts,coalesce(revenue_currency,''),settings
		from sites where org_id=$1 and deleted_at is null and erased_at is null and not is_demo order by id`, orgID)
	if err != nil {
		return err
	}
	type row struct {
		id int32
		Site
	}
	var list []row
	for rows.Next() {
		var r row
		var settings []byte
		if err := rows.Scan(&r.id, &r.Name, &r.Slug, &r.Kind, &r.Timezone, &r.RetentionDays, &r.AllowedHosts, &r.RevenueCurrency, &settings); err != nil {
			rows.Close()
			return err
		}
		r.Settings = FilterSettings(settings)
		list = append(list, r)
	}
	rows.Close()
	if err := rows.Err(); err != nil {
		return err
	}
	channels := map[int64]ChannelRef{}
	crows, err := tx.Query(ctx, `select id,kind,name from notification_channels where org_id=$1`, orgID)
	if err != nil {
		return err
	}
	for crows.Next() {
		var id int64
		var c ChannelRef
		if err := crows.Scan(&id, &c.Kind, &c.Name); err != nil {
			crows.Close()
			return err
		}
		channels[id] = c
	}
	crows.Close()
	for _, r := range list {
		site := r.Site
		site.AllowedHosts = nz(site.AllowedHosts)
		goalRefs := map[int64]string{}
		if site.Goals, err = loadGoals(ctx, tx, r.id, goalRefs); err != nil {
			return err
		}
		if site.Funnels, err = loadFunnels(ctx, tx, r.id); err != nil {
			return err
		}
		if site.Segments, err = loadSegments(ctx, tx, r.id); err != nil {
			return err
		}
		if site.Alerts, err = loadAlerts(ctx, tx, r.id, r.Slug, goalRefs, channels, b); err != nil {
			return err
		}
		if site.ShareLinks, err = loadShareLinks(ctx, tx, r.id); err != nil {
			return err
		}
		b.Sites = append(b.Sites, site)
	}
	return nil
}

func loadGoals(ctx context.Context, tx pgx.Tx, siteID int32, refs map[int64]string) ([]Goal, error) {
	rows, err := tx.Query(ctx, `select id,kind,match,value,coalesce(prop_key,''),coalesce(prop_value,''),name,coalesce(currency,''),scroll_threshold
		from goals where site_id=$1 order by id`, siteID)
	if err != nil {
		return nil, err
	}
	defer rows.Close()
	out := []Goal{}
	for rows.Next() {
		var id int64
		var g Goal
		if err := rows.Scan(&id, &g.Kind, &g.Match, &g.Value, &g.PropKey, &g.PropValue, &g.Name, &g.Currency, &g.ScrollThreshold); err != nil {
			return nil, err
		}
		g.Ref = fmt.Sprintf("g%d", len(out)+1)
		refs[id] = g.Ref
		out = append(out, g)
	}
	return out, rows.Err()
}

func loadFunnels(ctx context.Context, tx pgx.Tx, siteID int32) ([]Funnel, error) {
	rows, err := tx.Query(ctx, `select name,steps from funnels where site_id=$1 order by id`, siteID)
	if err != nil {
		return nil, err
	}
	defer rows.Close()
	out := []Funnel{}
	for rows.Next() {
		var f Funnel
		if err := rows.Scan(&f.Name, &f.Steps); err != nil {
			return nil, err
		}
		out = append(out, f)
	}
	return out, rows.Err()
}

func loadSegments(ctx context.Context, tx pgx.Tx, siteID int32) ([]Segment, error) {
	rows, err := tx.Query(ctx, `select name,filters from segments where site_id=$1 order by id`, siteID)
	if err != nil {
		return nil, err
	}
	defer rows.Close()
	out := []Segment{}
	for rows.Next() {
		var s Segment
		if err := rows.Scan(&s.Name, &s.Filters); err != nil {
			return nil, err
		}
		out = append(out, s)
	}
	return out, rows.Err()
}

// loadAlerts leaves out rule alerts: their actions carry webhook targets, user
// ids and channel ids that cannot be exported without secrets.
func loadAlerts(ctx context.Context, tx pgx.Tx, siteID int32, siteSlug string, goals map[int64]string, channels map[int64]ChannelRef, b *Bundle) ([]Alert, error) {
	rows, err := tx.Query(ctx, `select name,kind,threshold,goal_id,channel_ids,enabled,config from alerts where site_id=$1 order by id`, siteID)
	if err != nil {
		return nil, err
	}
	defer rows.Close()
	out := []Alert{}
	for rows.Next() {
		var a Alert
		var goalID *int64
		var ids []int64
		var config []byte
		if err := rows.Scan(&a.Name, &a.Kind, &a.Threshold, &goalID, &ids, &a.Enabled, &config); err != nil {
			return nil, err
		}
		if a.Kind == "app_rule" {
			b.Skipped = append(b.Skipped, SkippedEntry{What: "alert:" + a.Name, Site: siteSlug, Reason: "alert rules reference webhook targets, users and channel ids; recreate them in the new organization"})
			continue
		}
		if goalID != nil {
			a.GoalRef = goals[*goalID]
		}
		a.Channels = []ChannelRef{}
		for _, id := range ids {
			if c, ok := channels[id]; ok {
				a.Channels = append(a.Channels, c)
			}
		}
		if len(config) > 0 {
			a.Config = config
		}
		out = append(out, a)
	}
	return out, rows.Err()
}

func loadShareLinks(ctx context.Context, tx pgx.Tx, siteID int32) ([]ShareLink, error) {
	rows, err := tx.Query(ctx, `select to_char(created_at at time zone 'UTC','YYYY-MM-DD"T"HH24:MI:SS"Z"'),
		coalesce(to_char(expires_at at time zone 'UTC','YYYY-MM-DD"T"HH24:MI:SS"Z"'),''),revoked_at is not null,password_hash is not null,config
		from shared_links where site_id=$1 order by id`, siteID)
	if err != nil {
		return nil, err
	}
	defer rows.Close()
	out := []ShareLink{}
	for rows.Next() {
		var l ShareLink
		var config []byte
		if err := rows.Scan(&l.CreatedAt, &l.ExpiresAt, &l.Revoked, &l.HasPassword, &config); err != nil {
			return nil, err
		}
		l.Config = ScrubJSON(config)
		out = append(out, l)
	}
	return out, rows.Err()
}

// ScrubJSON scrubs a share-link config without the settings allow-list.
func ScrubJSON(raw []byte) json.RawMessage {
	var v any
	if json.Unmarshal(raw, &v) != nil {
		return json.RawMessage(`{}`)
	}
	b, _ := json.Marshal(scrub(v))
	return b
}

func (s *Service) loadMembers(ctx context.Context, tx pgx.Tx, orgID int64, b *Bundle) error {
	slugs := map[int32]string{}
	srows, err := tx.Query(ctx, `select id,slug from sites where org_id=$1`, orgID)
	if err != nil {
		return err
	}
	for srows.Next() {
		var id int32
		var slug string
		if err := srows.Scan(&id, &slug); err != nil {
			srows.Close()
			return err
		}
		slugs[id] = slug
	}
	srows.Close()
	rows, err := tx.Query(ctx, `select u.email,m.role,m.site_ids,m.capabilities,m.billing_admin
		from memberships m join users u on u.id=m.user_id
		where m.org_id=$1 and u.deleted_at is null and not u.is_system order by m.created_at,u.id`, orgID)
	if err != nil {
		return err
	}
	defer rows.Close()
	for rows.Next() {
		var m Member
		var ids []int32
		if err := rows.Scan(&m.Email, &m.Role, &ids, &m.Capabilities, &m.BillingAdmin); err != nil {
			return err
		}
		for _, id := range ids {
			if slug, ok := slugs[id]; ok {
				m.SiteSlugs = append(m.SiteSlugs, slug)
			}
		}
		b.Members = append(b.Members, m)
	}
	return rows.Err()
}
