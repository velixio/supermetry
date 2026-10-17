package admin

import (
	"context"
	"encoding/json"
	"errors"
	"strings"
	"time"

	"github.com/jackc/pgx/v5"

	"supermetry/internal/platform/store"
)

const MaxListLimit = 100

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
	ID          int64  `json:"id"`
	Name        string `json:"name"`
	Slug        string `json:"slug"`
	MemberCount int64  `json:"member_count"`
	SiteCount   int64  `json:"site_count"`
	SiteLimit   int    `json:"site_limit"`
}

type OrganizationDetail struct {
	Organization
	Members []User `json:"members"`
	Sites   []Site `json:"sites"`
}

type Site struct {
	ID            int32      `json:"id"`
	OrgID         int64      `json:"org_id"`
	Organization  string     `json:"organization"`
	PublicID      string     `json:"public_id"`
	Name          string     `json:"name"`
	RetentionDays int        `json:"retention_days"`
	Pageviews     int64      `json:"pageviews"`
	LastEventAt   *time.Time `json:"last_event_at,omitempty"`
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

func normalizeLimit(limit int) int {
	if limit <= 0 {
		return 50
	}
	if limit > MaxListLimit {
		return MaxListLimit
	}
	return limit
}

func (s *Service) ListUsers(ctx context.Context, search string, limit int) ([]User, error) {
	search = strings.TrimSpace(search)
	if len(search) > 200 {
		return nil, ErrInvalid
	}
	return s.listUsers(ctx, s.store.Pool, search, 0, limit)
}

type userQuerier interface {
	Query(ctx context.Context, sql string, args ...any) (pgx.Rows, error)
}

// listUsers lists visible (non-system) users; a non-zero onlyID restricts it to
// that user. db may be a transaction so a just-written user can be read back.
func (s *Service) listUsers(ctx context.Context, db userQuerier, search string, onlyID int64, limit int) ([]User, error) {
	rows, err := db.Query(ctx, `
		select u.id,u.email,u.name,u.is_admin,u.disabled_at is not null,u.password_reset_required,
		       u.totp_enabled,u.created_at,u.last_login_at,
		       coalesce(jsonb_agg(jsonb_build_object('org_id',m.org_id,'org_name',o.name,'role',m.role)
		         order by o.name,o.id) filter(where m.org_id is not null),'[]'::jsonb)
		from users u
		left join memberships m on m.user_id=u.id
		left join orgs o on o.id=m.org_id and o.deleted_at is null
		where u.deleted_at is null and not u.is_system and ($3::bigint=0 or u.id=$3) and ($1='' or lower(u.email) like '%'||lower($1)||'%' or lower(u.name) like '%'||lower($1)||'%')
		group by u.id order by u.id limit $2`, search, normalizeLimit(limit), onlyID)
	if err != nil {
		return nil, err
	}
	defer rows.Close()
	users := make([]User, 0)
	for rows.Next() {
		var user User
		var memberships []byte
		if err := rows.Scan(&user.ID, &user.Email, &user.Name, &user.InstanceAdmin, &user.Disabled, &user.PasswordResetRequired, &user.TOTPEnabled, &user.CreatedAt, &user.LastLoginAt, &memberships); err != nil {
			return nil, err
		}
		if err := json.Unmarshal(memberships, &user.Memberships); err != nil {
			return nil, err
		}
		users = append(users, user)
	}
	return users, rows.Err()
}

func (s *Service) ListOrganizations(ctx context.Context, search string, limit, siteLimit int) ([]Organization, error) {
	search = strings.TrimSpace(search)
	if len(search) > 200 {
		return nil, ErrInvalid
	}
	rows, err := s.store.Pool.Query(ctx, `
		select o.id,o.name,o.slug,count(distinct m.user_id),count(distinct s.id)
		from orgs o left join memberships m on m.org_id=o.id left join sites s on s.org_id=o.id and s.deleted_at is null
		where o.deleted_at is null and ($1='' or lower(o.name) like '%'||lower($1)||'%' or lower(o.slug) like '%'||lower($1)||'%')
		group by o.id order by o.id limit $2`, search, normalizeLimit(limit))
	if err != nil {
		return nil, err
	}
	defer rows.Close()
	items := make([]Organization, 0)
	for rows.Next() {
		var item Organization
		if err := rows.Scan(&item.ID, &item.Name, &item.Slug, &item.MemberCount, &item.SiteCount); err != nil {
			return nil, err
		}
		item.SiteLimit = siteLimit
		items = append(items, item)
	}
	return items, rows.Err()
}

func (s *Service) GetOrganization(ctx context.Context, id int64, siteLimit int) (OrganizationDetail, error) {
	var detail OrganizationDetail
	err := s.store.Pool.QueryRow(ctx, `
		select o.id,o.name,o.slug,
		       (select count(*) from memberships m where m.org_id=o.id),
		       (select count(*) from sites s where s.org_id=o.id and s.deleted_at is null)
		from orgs o where o.id=$1 and o.deleted_at is null`, id).Scan(
		&detail.ID, &detail.Name, &detail.Slug, &detail.MemberCount, &detail.SiteCount,
	)
	if errors.Is(err, pgx.ErrNoRows) {
		return OrganizationDetail{}, ErrNotFound
	}
	if err != nil {
		return OrganizationDetail{}, err
	}
	detail.SiteLimit = siteLimit
	users, err := s.ListUsers(ctx, "", MaxListLimit)
	if err != nil {
		return OrganizationDetail{}, err
	}
	for _, user := range users {
		for _, membership := range user.Memberships {
			if membership.OrgID == id {
				user.Memberships = []Membership{membership}
				detail.Members = append(detail.Members, user)
				break
			}
		}
	}
	sites, err := s.ListSites(ctx, "", MaxListLimit)
	if err != nil {
		return OrganizationDetail{}, err
	}
	for _, site := range sites {
		if site.OrgID == id {
			detail.Sites = append(detail.Sites, site)
		}
	}
	return detail, nil
}

func (s *Service) ListSites(ctx context.Context, search string, limit int) ([]Site, error) {
	search = strings.TrimSpace(search)
	if len(search) > 200 {
		return nil, ErrInvalid
	}
	rows, err := s.store.Pool.Query(ctx, `
		select s.id,s.org_id,o.name,s.public_id,s.name,s.retention_class,
		       coalesce((select sum(r.pageviews) from rollup_day r where r.site_id=s.id),0),
		       e.last_event_at
		from sites s join orgs o on o.id=s.org_id left join site_last_event e on e.site_id=s.id
		where s.deleted_at is null and o.deleted_at is null
		  and ($1='' or lower(s.name) like '%'||lower($1)||'%' or lower(s.public_id) like '%'||lower($1)||'%' or lower(o.name) like '%'||lower($1)||'%')
		order by s.id limit $2`, search, normalizeLimit(limit))
	if err != nil {
		return nil, err
	}
	defer rows.Close()
	items := make([]Site, 0)
	for rows.Next() {
		var item Site
		if err := rows.Scan(&item.ID, &item.OrgID, &item.Organization, &item.PublicID, &item.Name, &item.RetentionDays, &item.Pageviews, &item.LastEventAt); err != nil {
			return nil, err
		}
		items = append(items, item)
	}
	return items, rows.Err()
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

func (s *Service) ListAudit(ctx context.Context, limit int) ([]AuditEntry, error) {
	rows, err := s.store.Pool.Query(ctx, `select id,actor_user_id,action,target_type,target_id,metadata,created_at from audit_log where org_id is null order by created_at desc,id desc limit $1`, normalizeLimit(limit))
	if err != nil {
		return nil, err
	}
	defer rows.Close()
	items := make([]AuditEntry, 0)
	for rows.Next() {
		var item AuditEntry
		var metadata []byte
		if err := rows.Scan(&item.ID, &item.ActorUserID, &item.Action, &item.TargetType, &item.TargetID, &metadata, &item.CreatedAt); err != nil {
			return nil, err
		}
		if err := json.Unmarshal(metadata, &item.Metadata); err != nil {
			return nil, err
		}
		items = append(items, item)
	}
	return items, rows.Err()
}

func (s *Service) User(ctx context.Context, id int64) (User, error) {
	users, err := s.ListUsers(ctx, "", MaxListLimit)
	if err != nil {
		return User{}, err
	}
	for _, user := range users {
		if user.ID == id {
			return user, nil
		}
	}
	return User{}, ErrNotFound
}

func notFound(err error) error {
	if errors.Is(err, pgx.ErrNoRows) {
		return ErrNotFound
	}
	return err
}
