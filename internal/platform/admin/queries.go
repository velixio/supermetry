package admin

import (
	"context"
	"encoding/json"
	"errors"
	"time"

	"github.com/jackc/pgx/v5"

	"supermetry/internal/platform/lifecycle"
	"supermetry/internal/platform/store"
)

type Membership struct {
	OrgID   int64  `json:"org_id"`
	OrgName string `json:"org_name"`
	Role    string `json:"role"`
}

type User struct {
	ID                    int64        `json:"id"`
	Email                 string       `json:"email"`
	Name                  string       `json:"name"`
	InstanceAdmin         bool         `json:"instance_admin"`
	Disabled              bool         `json:"disabled"`
	PasswordResetRequired bool         `json:"password_reset_required"`
	TOTPEnabled           bool         `json:"totp_enabled"`
	CreatedAt             time.Time    `json:"created_at"`
	LastLoginAt           *time.Time   `json:"last_login_at,omitempty"`
	Memberships           []Membership `json:"memberships"`
}

type Organization struct {
	ID              int64         `json:"id"`
	Name            string        `json:"name"`
	Slug            string        `json:"slug"`
	MemberCount     int64         `json:"member_count"`
	SiteCount       int64         `json:"site_count"`
	SiteLimit       int           `json:"site_limit"`
	DeletedAt       *time.Time    `json:"deleted_at,omitempty"`
	RestorableUntil *time.Time    `json:"restorable_until,omitempty"`
	DeletedByKind   string        `json:"deleted_by_kind,omitempty"`
	DeletedByUserID *int64        `json:"deleted_by_user_id,omitempty"`
	DeletedByName   string        `json:"deleted_by_name,omitempty"`
	Storage         *StorageUsage `json:"storage"`
}

type OrganizationDetail struct {
	Organization
	Members []User `json:"members"`
	// MembersNextCursor is set when the organization has more members than the
	// detail carries (maxDetailMembers); pass it to the members endpoint.
	MembersNextCursor string `json:"members_next_cursor,omitempty"`
	Sites             []Site `json:"sites"`
}

const (
	maxDetailMemberPages = 5
	maxDetailMembers     = maxDetailMemberPages * MaxListLimit
)

type Site struct {
	ID            int32         `json:"id"`
	OrgID         int64         `json:"org_id"`
	Organization  string        `json:"organization"`
	PublicID      string        `json:"public_id"`
	Name          string        `json:"name"`
	RetentionDays int           `json:"retention_days"`
	Pageviews     int64         `json:"pageviews"`
	LastEventAt   *time.Time    `json:"last_event_at,omitempty"`
	Storage       *StorageUsage `json:"storage"`
}

// StorageUsage is the last completely published storage estimate. Category
// fields are populated for sites and omitted from organization totals.
type StorageUsage struct {
	EstimatedStorageBytes int64     `json:"estimated_storage_bytes"`
	DatabaseBytes         int64     `json:"database_bytes"`
	ReplayBytes           int64     `json:"replay_bytes"`
	MeasuredAt            time.Time `json:"measured_at"`
	MeasurementVersion    int       `json:"measurement_version"`
	HotBytes              *int64    `json:"hot_bytes,omitempty"`
	RetainedBytes         *int64    `json:"retained_bytes,omitempty"`
	AuxiliaryBytes        *int64    `json:"auxiliary_bytes,omitempty"`
	ReplayRecordingBytes  *int64    `json:"replay_recording_bytes,omitempty"`
	ReplayCSSBytes        *int64    `json:"replay_css_bytes,omitempty"`
	// UnmeasuredSites is set on organization totals that cover only the
	// measured sites; zero means the total is complete.
	UnmeasuredSites int64 `json:"unmeasured_sites,omitempty"`
}

type Settings struct {
	SignupMode    string `json:"signup_mode"`
	ReplayVisible bool   `json:"replay_visible"`
}

type SettingsPatch struct {
	SignupMode    *string
	ReplayVisible *bool
}

type AuditEntry struct {
	ID          int64          `json:"id"`
	ActorUserID *int64         `json:"actor_user_id,omitempty"`
	Action      string         `json:"action"`
	TargetType  string         `json:"target_type"`
	TargetID    int64          `json:"target_id"`
	Metadata    map[string]any `json:"metadata"`
	CreatedAt   time.Time      `json:"created_at"`
}

// ListUsers is the first page of ListUsersPage, kept for callers that do not page.
func (s *Service) ListUsers(ctx context.Context, search string, limit int) ([]User, error) {
	page, err := s.ListUsersPage(ctx, ListParams{Search: search, Limit: limit})
	return page.Items, err
}

var userSorts = map[string]sortSpec{
	"id":         {"u.id", "bigint"},
	"created_at": {"u.created_at", "timestamptz"},
	"email":      {"u.email", "text"},
	"name":       {"u.name", "text"},
}

// ListUsersPage lists visible (non-system) users with keyset paging.
func (s *Service) ListUsersPage(ctx context.Context, p ListParams) (Page[User], error) {
	search, err := cleanSearch(p.Search)
	if err != nil {
		return Page[User]{}, err
	}
	return s.listUsersPage(ctx, s.store.Pool, userFilter{search: search}, p)
}

type userQuerier interface {
	Query(ctx context.Context, sql string, args ...any) (pgx.Rows, error)
}

// userFilter narrows the user list: a non-zero onlyID selects that user, a
// non-zero orgID the members of that organization.
type userFilter struct {
	search string
	onlyID int64
	orgID  int64
}

// listUsers lists visible (non-system) users; a non-zero onlyID restricts it to
// that user. db may be a transaction so a just-written user can be read back.
func (s *Service) listUsers(ctx context.Context, db userQuerier, search string, onlyID int64, limit int) ([]User, error) {
	page, err := s.listUsersPage(ctx, db, userFilter{search: search, onlyID: onlyID}, ListParams{Limit: limit})
	return page.Items, err
}

func (s *Service) listUsersPage(ctx context.Context, db userQuerier, f userFilter, p ListParams) (Page[User], error) {
	k, err := newKeyset(p, userSorts)
	if err != nil {
		return Page[User]{}, err
	}
	inner := `
		select u.id,u.email,u.name,u.is_admin,u.disabled_at is not null disabled,u.password_reset_required,
		       u.totp_enabled,u.created_at,u.last_login_at,
		       coalesce(jsonb_agg(jsonb_build_object('org_id',m.org_id,'org_name',o.name,'role',m.role)
		         order by o.name,o.id) filter(where m.org_id is not null),'[]'::jsonb) memberships,
		       ` + k.spec.expr + ` sk
		from users u
		left join memberships m on m.user_id=u.id
		left join orgs o on o.id=m.org_id and o.deleted_at is null
		where u.deleted_at is null and not u.is_system and ($2::bigint=0 or u.id=$2)
		  and ($3::bigint=0 or exists(select 1 from memberships mo where mo.user_id=u.id and mo.org_id=$3))
		  and ($1='' or lower(u.email) like '%'||lower($1)||'%' or lower(u.name) like '%'||lower($1)||'%')
		group by u.id`
	query, args := k.wrap(inner, []any{f.search, f.onlyID, f.orgID})
	rows, err := db.Query(ctx, query, args...)
	if err != nil {
		return Page[User]{}, err
	}
	defer rows.Close()
	users := make([]User, 0)
	var keys []string
	for rows.Next() {
		var user User
		var memberships []byte
		var sk any
		var skText string
		if err := rows.Scan(&user.ID, &user.Email, &user.Name, &user.InstanceAdmin, &user.Disabled, &user.PasswordResetRequired, &user.TOTPEnabled, &user.CreatedAt, &user.LastLoginAt, &memberships, &sk, &skText); err != nil {
			return Page[User]{}, err
		}
		if err := json.Unmarshal(memberships, &user.Memberships); err != nil {
			return Page[User]{}, err
		}
		if f.orgID != 0 {
			own := make([]Membership, 0, 1)
			for _, m := range user.Memberships {
				if m.OrgID == f.orgID {
					own = append(own, m)
				}
			}
			user.Memberships = own
		}
		users = append(users, user)
		keys = append(keys, skText)
	}
	if err := rows.Err(); err != nil {
		return Page[User]{}, err
	}
	page := Page[User]{Items: users}
	if len(users) > k.limit {
		page.Items = users[:k.limit]
		page.NextCursor = k.next(true, page.Items[k.limit-1].ID, keys[k.limit-1])
	}
	return page, nil
}

// ListOrganizations is the first page of ListOrganizationsPage.
func (s *Service) ListOrganizations(ctx context.Context, search string, limit, siteLimit int) ([]Organization, error) {
	page, err := s.ListOrganizationsPage(ctx, ListParams{Search: search, Limit: limit}, siteLimit)
	return page.Items, err
}

var organizationSorts = map[string]sortSpec{
	"id":         {"o.id", "bigint"},
	"created_at": {"o.created_at", "timestamptz"},
	"name":       {"o.name", "text"},
	"members":    {"(select count(*) from memberships mc where mc.org_id=o.id)", "bigint"},
	"storage":    {"coalesce(storage.estimated_storage_bytes,-1)", "bigint"},
}

// ListOrganizationsPage lists organizations, including restorable deletions,
// with keyset paging. Sorting by storage puts unmeasured organizations first
// when ascending.
func (s *Service) ListOrganizationsPage(ctx context.Context, p ListParams, siteLimit int) (Page[Organization], error) {
	search, err := cleanSearch(p.Search)
	if err != nil {
		return Page[Organization]{}, err
	}
	k, err := newKeyset(p, organizationSorts)
	if err != nil {
		return Page[Organization]{}, err
	}
	query, args := k.wrap(listOrganizationsHead+", "+k.spec.expr+" sk "+listOrganizationsTail, []any{search})
	rows, err := s.store.Pool.Query(ctx, query, args...)
	if err != nil {
		return Page[Organization]{}, err
	}
	defer rows.Close()
	items := make([]Organization, 0)
	var keys []string
	for rows.Next() {
		var item Organization
		var sk any
		var skText string
		storage, targets := storageScanTargets(false)
		if err := rows.Scan(&item.ID, &item.Name, &item.Slug, &item.MemberCount, &item.SiteCount, &item.DeletedAt, &item.DeletedByKind, &item.DeletedByUserID, &item.DeletedByName, targets[0], targets[1], targets[2], targets[3], targets[4], targets[5], &sk, &skText); err != nil {
			return Page[Organization]{}, err
		}
		item.Storage = storage.value()
		item.SiteLimit = siteLimit
		if item.DeletedAt != nil {
			until := lifecycle.PurgeAt(*item.DeletedAt)
			item.RestorableUntil = &until
		}
		items = append(items, item)
		keys = append(keys, skText)
	}
	if err := rows.Err(); err != nil {
		return Page[Organization]{}, err
	}
	page := Page[Organization]{Items: items}
	if len(items) > k.limit {
		page.Items = items[:k.limit]
		page.NextCursor = k.next(true, page.Items[k.limit-1].ID, keys[k.limit-1])
	}
	return page, nil
}

func (s *Service) GetOrganization(ctx context.Context, id int64, siteLimit int) (OrganizationDetail, error) {
	return s.getOrganization(ctx, id, siteLimit, true)
}

// GetOrganizationWithoutMembers is GetOrganization with Members left empty, for
// callers that page the member list themselves.
func (s *Service) GetOrganizationWithoutMembers(ctx context.Context, id int64, siteLimit int) (OrganizationDetail, error) {
	return s.getOrganization(ctx, id, siteLimit, false)
}

func (s *Service) getOrganization(ctx context.Context, id int64, siteLimit int, withMembers bool) (OrganizationDetail, error) {
	var detail OrganizationDetail
	storage, targets := storageScanTargets(false)
	err := s.store.Pool.QueryRow(ctx, getOrganizationSQL, id).Scan(
		&detail.ID, &detail.Name, &detail.Slug, &detail.MemberCount, &detail.SiteCount,
		&detail.DeletedAt, &detail.DeletedByKind, &detail.DeletedByUserID, &detail.DeletedByName,
		targets[0], targets[1], targets[2], targets[3], targets[4], targets[5],
	)
	if errors.Is(err, pgx.ErrNoRows) {
		return OrganizationDetail{}, ErrNotFound
	}
	if err != nil {
		return OrganizationDetail{}, err
	}
	detail.Storage = storage.value()
	detail.SiteLimit = siteLimit
	if detail.DeletedAt != nil {
		until := lifecycle.PurgeAt(*detail.DeletedAt)
		detail.RestorableUntil = &until
	}
	// Every member, not just those among the first users overall: page through
	// the membership list up to maxDetailMembers and point at the rest.
	var cursor string
	for pages := 0; withMembers && pages < maxDetailMemberPages; pages++ {
		page, err := s.listUsersPage(ctx, s.store.Pool, userFilter{orgID: id}, ListParams{Limit: MaxListLimit, Cursor: cursor})
		if err != nil {
			return OrganizationDetail{}, err
		}
		detail.Members = append(detail.Members, page.Items...)
		cursor = page.NextCursor
		if cursor == "" {
			break
		}
	}
	detail.MembersNextCursor = cursor
	if detail.Members == nil {
		detail.Members = []User{}
	}
	rows, err := s.store.Pool.Query(ctx, getOrganizationSitesSQL, id, MaxListLimit)
	if err != nil {
		return OrganizationDetail{}, err
	}
	sites, err := scanSites(rows)
	if err != nil {
		return OrganizationDetail{}, err
	}
	detail.Sites = sites
	return detail, nil
}

// ListSites is the first page of ListSitesPage.
func (s *Service) ListSites(ctx context.Context, search string, limit int) ([]Site, error) {
	page, err := s.ListSitesPage(ctx, ListParams{Search: search, Limit: limit})
	return page.Items, err
}

var siteSorts = map[string]sortSpec{
	"id":         {"s.id", "bigint"},
	"created_at": {"s.created_at", "timestamptz"},
	"name":       {"s.name", "text"},
	"storage":    {"coalesce(u.estimated_storage_bytes,-1)", "bigint"},
}

// ListSitesPage lists live sites with keyset paging; OrgID restricts it to one organization.
func (s *Service) ListSitesPage(ctx context.Context, p ListParams) (Page[Site], error) {
	search, err := cleanSearch(p.Search)
	if err != nil {
		return Page[Site]{}, err
	}
	k, err := newKeyset(p, siteSorts)
	if err != nil {
		return Page[Site]{}, err
	}
	query, args := k.wrap(listSitesHead+", "+k.spec.expr+" sk "+listSitesTail, []any{search, p.OrgID})
	rows, err := s.store.Pool.Query(ctx, query, args...)
	if err != nil {
		return Page[Site]{}, err
	}
	defer rows.Close()
	items := make([]Site, 0)
	var keys []string
	for rows.Next() {
		item, err := scanSite(rows, true)
		if err != nil {
			return Page[Site]{}, err
		}
		items = append(items, item.Site)
		keys = append(keys, item.key)
	}
	if err := rows.Err(); err != nil {
		return Page[Site]{}, err
	}
	page := Page[Site]{Items: items}
	if len(items) > k.limit {
		page.Items = items[:k.limit]
		page.NextCursor = k.next(true, int64(page.Items[k.limit-1].ID), keys[k.limit-1])
	}
	return page, nil
}

type scannedSite struct {
	Site
	key string
}

func scanSite(rows pgx.Rows, paged bool) (scannedSite, error) {
	var item scannedSite
	storage, targets := storageScanTargets(true)
	dest := []any{&item.ID, &item.OrgID, &item.Organization, &item.PublicID, &item.Name, &item.RetentionDays, &item.Pageviews, &item.LastEventAt,
		targets[0], targets[1], targets[2], targets[3], targets[4], targets[5], targets[6], targets[7], targets[8], targets[9]}
	if paged {
		var sk any
		dest = append(dest, &sk, &item.key)
	}
	if err := rows.Scan(dest...); err != nil {
		return scannedSite{}, err
	}
	item.Storage = storage.value()
	return item, nil
}

func scanSites(rows pgx.Rows) ([]Site, error) {
	defer rows.Close()
	items := make([]Site, 0)
	for rows.Next() {
		item, err := scanSite(rows, false)
		if err != nil {
			return nil, err
		}
		items = append(items, item.Site)
	}
	return items, rows.Err()
}

// organizationStorageLateralSQL totals the measured sites of an organization.
// A site without a snapshot (for example one created after the run captured its
// sites) does not hide the others: the total covers the measured sites and
// unmeasured_sites says how many are still missing. The total is null only when
// no included site has a snapshot yet, or when snapshots disagree on their
// generation, which publication never produces.
const organizationStorageLateralSQL = `
	left join lateral (
		select
			case
				when count(owned_site.id)=0 and max(generation.completed_at) is not null then 0
				when count(u.site_id)>0 and count(distinct u.measured_at)=1 and count(distinct u.measurement_version)=1 then sum(u.estimated_storage_bytes)
			end estimated_storage_bytes,
			case
				when count(owned_site.id)=0 and max(generation.completed_at) is not null then 0
				when count(u.site_id)>0 and count(distinct u.measured_at)=1 and count(distinct u.measurement_version)=1 then sum(u.database_bytes)
			end database_bytes,
			case
				when count(owned_site.id)=0 and max(generation.completed_at) is not null then 0
				when count(u.site_id)>0 and count(distinct u.measured_at)=1 and count(distinct u.measurement_version)=1 then sum(u.replay_bytes)
			end replay_bytes,
			case
				when count(owned_site.id)=0 then max(generation.completed_at)
				when count(u.site_id)>0 and count(distinct u.measured_at)=1 and count(distinct u.measurement_version)=1 then min(u.measured_at)
			end measured_at,
			case
				when count(owned_site.id)=0 then max(generation.measurement_version)
				when count(u.site_id)>0 and count(distinct u.measured_at)=1 and count(distinct u.measurement_version)=1 then min(u.measurement_version)
			end measurement_version,
			(count(owned_site.id)-count(u.site_id))::bigint unmeasured_sites
		from (values (true)) singleton(present)
		left join lateral (
			select completed_at,measurement_version
			from storage_usage_runs
			where status='complete'
			order by completed_at desc,id desc
			limit 1
		) generation on true
		left join sites owned_site on owned_site.org_id=o.id and owned_site.erased_at is null
		left join site_storage_usage u on u.site_id=owned_site.id
	) storage on true`

const listOrganizationsHead = `
	select o.id,o.name,o.slug,
	       (select count(*) from memberships m where m.org_id=o.id),
	       (select count(*) from sites counted_site where counted_site.org_id=o.id and (counted_site.deleted_at is null or (counted_site.deleted_with_org and counted_site.erased_at is null))),
	       o.deleted_at,coalesce(o.deleted_by_kind,''),o.deleted_by_user_id,coalesce(deleted_by.name,''),
	       storage.estimated_storage_bytes,storage.database_bytes,storage.replay_bytes,storage.measured_at,storage.measurement_version,storage.unmeasured_sites`
const listOrganizationsTail = `
	from orgs o
	left join users deleted_by on deleted_by.id=o.deleted_by_user_id
` + organizationStorageLateralSQL + `
	where o.erased_at is null and ($1='' or lower(o.name) like '%'||lower($1)||'%' or lower(o.slug) like '%'||lower($1)||'%')`

const getOrganizationSQL = `
	select o.id,o.name,o.slug,
	       (select count(*) from memberships m where m.org_id=o.id),
	       (select count(*) from sites counted_site where counted_site.org_id=o.id and (counted_site.deleted_at is null or (counted_site.deleted_with_org and counted_site.erased_at is null))),
	       o.deleted_at,coalesce(o.deleted_by_kind,''),o.deleted_by_user_id,coalesce(deleted_by.name,''),
	       storage.estimated_storage_bytes,storage.database_bytes,storage.replay_bytes,storage.measured_at,storage.measurement_version,storage.unmeasured_sites
	from orgs o
	left join users deleted_by on deleted_by.id=o.deleted_by_user_id
` + organizationStorageLateralSQL + `
	where o.id=$1 and o.erased_at is null`

const listSitesHead = `
	select s.id,s.org_id,o.name,s.public_id,s.name,s.retention_class,
	       coalesce((select sum(r.pageviews) from rollup_day r where r.site_id=s.id),0),
	       e.last_event_at,
	       u.estimated_storage_bytes,u.database_bytes,u.replay_bytes,u.measured_at,u.measurement_version,
	       u.hot_bytes,u.retained_bytes,u.auxiliary_bytes,u.replay_recording_bytes,u.replay_css_bytes`
const listSitesTail = `
	from sites s
	join orgs o on o.id=s.org_id
	left join site_last_event e on e.site_id=s.id
	left join site_storage_usage u on u.site_id=s.id
	where s.deleted_at is null and o.deleted_at is null and ($2::bigint=0 or s.org_id=$2)
	  and ($1='' or lower(s.name) like '%'||lower($1)||'%' or lower(s.public_id) like '%'||lower($1)||'%' or lower(o.name) like '%'||lower($1)||'%')`

const getOrganizationSitesSQL = `
	select s.id,s.org_id,o.name,s.public_id,s.name,s.retention_class,
	       coalesce((select sum(r.pageviews) from rollup_day r where r.site_id=s.id),0),
	       e.last_event_at,
	       u.estimated_storage_bytes,u.database_bytes,u.replay_bytes,u.measured_at,u.measurement_version,
	       u.hot_bytes,u.retained_bytes,u.auxiliary_bytes,u.replay_recording_bytes,u.replay_css_bytes
	from sites s
	join orgs o on o.id=s.org_id
	left join site_last_event e on e.site_id=s.id
	left join site_storage_usage u on u.site_id=s.id
	where s.org_id=$1 and s.erased_at is null and (s.deleted_at is null or s.deleted_with_org)
	order by s.id limit $2`

type nullableStorageUsage struct {
	estimatedStorageBytes *int64
	databaseBytes         *int64
	replayBytes           *int64
	measuredAt            *time.Time
	measurementVersion    *int
	hotBytes              *int64
	retainedBytes         *int64
	auxiliaryBytes        *int64
	replayRecordingBytes  *int64
	replayCSSBytes        *int64
	unmeasuredSites       int64
}

func storageScanTargets(site bool) (*nullableStorageUsage, []any) {
	storage := &nullableStorageUsage{}
	targets := []any{&storage.estimatedStorageBytes, &storage.databaseBytes, &storage.replayBytes, &storage.measuredAt, &storage.measurementVersion}
	if !site {
		targets = append(targets, &storage.unmeasuredSites)
	}
	if site {
		targets = append(targets, &storage.hotBytes, &storage.retainedBytes, &storage.auxiliaryBytes, &storage.replayRecordingBytes, &storage.replayCSSBytes)
	}
	return storage, targets
}

func (s *nullableStorageUsage) value() *StorageUsage {
	if s.estimatedStorageBytes == nil || s.databaseBytes == nil || s.replayBytes == nil || s.measuredAt == nil || s.measurementVersion == nil {
		return nil
	}
	return &StorageUsage{
		EstimatedStorageBytes: *s.estimatedStorageBytes,
		DatabaseBytes:         *s.databaseBytes,
		ReplayBytes:           *s.replayBytes,
		MeasuredAt:            *s.measuredAt,
		MeasurementVersion:    *s.measurementVersion,
		HotBytes:              s.hotBytes,
		RetainedBytes:         s.retainedBytes,
		AuxiliaryBytes:        s.auxiliaryBytes,
		ReplayRecordingBytes:  s.replayRecordingBytes,
		ReplayCSSBytes:        s.replayCSSBytes,
		UnmeasuredSites:       s.unmeasuredSites,
	}
}

func (s *Service) EffectiveSettings(ctx context.Context, defaultSignup string, defaultReplay bool) (Settings, error) {
	var signup *string
	var replay *bool
	if err := s.store.Pool.QueryRow(ctx, `select signup_mode_override,replay_visible_override from instance_settings where id`).Scan(&signup, &replay); err != nil {
		return Settings{}, err
	}
	settings := Settings{SignupMode: defaultSignup, ReplayVisible: defaultReplay}
	if signup != nil {
		settings.SignupMode = *signup
	}
	if replay != nil {
		settings.ReplayVisible = *replay
	}
	return settings, nil
}

func (s *Service) UpdateSettings(ctx context.Context, actorID int64, patch SettingsPatch) error {
	if patch.SignupMode == nil && patch.ReplayVisible == nil {
		return ErrInvalid
	}
	if patch.SignupMode != nil && *patch.SignupMode != "open" && *patch.SignupMode != "invite" {
		return ErrInvalid
	}
	return s.store.Tx(ctx, func(tx pgx.Tx) error {
		if err := requireAdmin(ctx, tx, actorID); err != nil {
			return err
		}
		if _, err := tx.Exec(ctx, `update instance_settings set signup_mode_override=coalesce($1,signup_mode_override),replay_visible_override=coalesce($2,replay_visible_override),updated_at=now() where id`, patch.SignupMode, patch.ReplayVisible); err != nil {
			return err
		}
		return store.WriteInstanceAudit(ctx, tx, store.InstanceAuditEntry{ActorUserID: actorID, Action: "instance.settings.updated", TargetType: "instance", TargetID: 1, Metadata: map[string]any{"signup_mode": patch.SignupMode, "replay_visible": patch.ReplayVisible}})
	})
}

// User returns one visible (non-system, undeleted) user.
func (s *Service) User(ctx context.Context, id int64) (User, error) {
	users, err := s.listUsers(ctx, s.store.Pool, "", id, 1)
	if err != nil {
		return User{}, err
	}
	if len(users) == 0 {
		return User{}, ErrNotFound
	}
	return users[0], nil
}

// GetOrganizationMembers pages through the members of an organization, each
// with only that organization's membership.
func (s *Service) GetOrganizationMembers(ctx context.Context, orgID int64, p ListParams) (Page[User], error) {
	var exists bool
	if err := s.store.Pool.QueryRow(ctx, `select exists(select 1 from orgs where id=$1 and erased_at is null)`, orgID).Scan(&exists); err != nil {
		return Page[User]{}, err
	}
	if !exists {
		return Page[User]{}, ErrNotFound
	}
	search, err := cleanSearch(p.Search)
	if err != nil {
		return Page[User]{}, err
	}
	return s.listUsersPage(ctx, s.store.Pool, userFilter{search: search, orgID: orgID}, p)
}

func notFound(err error) error {
	if errors.Is(err, pgx.ErrNoRows) {
		return ErrNotFound
	}
	return err
}
