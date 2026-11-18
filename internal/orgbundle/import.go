package orgbundle

import (
	"context"
	"encoding/json"
	"errors"
	"fmt"
	"strings"

	"github.com/jackc/pgx/v5"

	"supermetry/internal/alerts"
	"supermetry/internal/platform/audit"
	"supermetry/internal/platform/authz"
	"supermetry/internal/platform/invites"
	"supermetry/internal/platform/sitesvc"
	coresites "supermetry/internal/sites"
)

// ErrInvalid marks a bundle that cannot be imported.
var ErrInvalid = errors.New("orgbundle: invalid bundle")

// SiteCreator creates a project the way the API does (limits, slugs, audit).
type SiteCreator interface {
	Create(ctx context.Context, principal authz.Principal, input any, legacy ...any) (coresites.Site, error)
}

// Inviter creates a member invitation (and mails it when mail is enabled).
type Inviter interface {
	Create(ctx context.Context, principal authz.Principal, email string, role authz.Role, siteIDs []int32, options ...invites.Access) (string, error)
}

// ImportOptions control an import.
type ImportOptions struct {
	// DryRun reports what would happen and writes nothing.
	DryRun bool
	// InviteMembers sends invitations to bundle members. Off by default:
	// members are listed in the report but nobody is invited.
	InviteMembers bool
}

// Report is the result of an import or its dry run.
type Report struct {
	DryRun   bool           `json:"dry_run"`
	Sites    []SiteResult   `json:"sites"`
	Members  []MemberResult `json:"members"`
	Warnings []string       `json:"warnings"`
}

type SiteResult struct {
	Name           string `json:"name"`
	Slug           string `json:"slug"`
	Action         string `json:"action"` // create, created, skip_exists, failed
	Goals          int    `json:"goals"`
	Funnels        int    `json:"funnels"`
	Segments       int    `json:"segments"`
	Alerts         int    `json:"alerts"`
	AlertsDisabled int    `json:"alerts_disabled"`
	ShareLinksSkip int    `json:"share_links_not_recreated"`
	Error          string `json:"error,omitempty"`
}

type MemberResult struct {
	Email  string `json:"email"`
	Role   string `json:"role"`
	Action string `json:"action"` // listed_only, would_invite, invited, already_member, not_invited, failed
	Note   string `json:"note,omitempty"`
}

const (
	maxSites       = 200
	maxPerSite     = 500
	maxMembers     = 1000
	maxWarnings    = 200
	maxNameLength  = 200
	maxJSONPerItem = 64 << 10
)

var (
	goalKinds   = map[string]bool{"page": true, "event": true, "scroll": true}
	goalMatches = map[string]bool{"exact": true, "prefix": true, "wildcard": true}
	siteKinds   = map[string]bool{"web": true, "pixel": true, "app": true}
	memberRoles = map[string]bool{"admin": true, "editor": true, "viewer": true, "owner": true}
)

// Validate checks the bundle envelope and every value an import will insert.
func Validate(b Bundle) error {
	bad := func(format string, a ...any) error {
		return fmt.Errorf("%w: %s", ErrInvalid, fmt.Sprintf(format, a...))
	}
	if b.Kind != Kind {
		return bad("kind must be %q", Kind)
	}
	if b.SchemaVersion < 1 || b.SchemaVersion > SchemaVersion {
		return bad("schema_version %d is not supported (this server reads up to %d)", b.SchemaVersion, SchemaVersion)
	}
	if len(b.Sites) > maxSites || len(b.Members) > maxMembers {
		return bad("too many sites or members")
	}
	slugs := map[string]bool{}
	for _, s := range b.Sites {
		if strings.TrimSpace(s.Name) == "" || len(s.Name) > maxNameLength || !siteKinds[s.Kind] || s.Slug == "" || slugs[s.Slug] {
			return bad("site %q needs a unique slug, a name and a kind of web, pixel or app", s.Name)
		}
		slugs[s.Slug] = true
		if len(s.Goals) > maxPerSite || len(s.Funnels) > maxPerSite || len(s.Segments) > maxPerSite || len(s.Alerts) > maxPerSite {
			return bad("site %q has too many goals, funnels, segments or alerts", s.Name)
		}
		refs := map[string]bool{}
		for _, g := range s.Goals {
			if !goalKinds[g.Kind] || !goalMatches[g.Match] || g.Name == "" || len(g.Name) > maxNameLength || len(g.Value) > 2000 || g.Ref == "" || refs[g.Ref] {
				return bad("site %q has an invalid goal %q", s.Name, g.Name)
			}
			if (g.Kind == "scroll") != (g.ScrollThreshold != nil) || (g.ScrollThreshold != nil && (*g.ScrollThreshold < 1 || *g.ScrollThreshold > 100)) {
				return bad("site %q goal %q has an invalid scroll threshold", s.Name, g.Name)
			}
			refs[g.Ref] = true
		}
		for _, f := range s.Funnels {
			if f.Name == "" || len(f.Name) > maxNameLength || !isJSON(f.Steps, '[') {
				return bad("site %q has an invalid funnel %q", s.Name, f.Name)
			}
		}
		for _, g := range s.Segments {
			if n := len([]rune(g.Name)); n < 1 || n > 80 || !isJSON(g.Filters, '[') {
				return bad("site %q has an invalid segment %q", s.Name, g.Name)
			}
		}
		for _, a := range s.Alerts {
			if a.Kind == "" || a.Threshold <= 0 || len(a.Name) > 120 || (a.GoalRef != "") != (a.Kind == "goal_reached") || (a.GoalRef != "" && !refs[a.GoalRef]) {
				return bad("site %q has an invalid alert %q", s.Name, a.Name)
			}
			if len(a.Config) > 0 && !isJSON(a.Config, '{') {
				return bad("site %q alert %q has an invalid config", s.Name, a.Name)
			}
		}
	}
	for _, m := range b.Members {
		if !strings.Contains(m.Email, "@") || len(m.Email) > 320 || !memberRoles[m.Role] {
			return bad("member %q has an invalid email or role", m.Email)
		}
	}
	return nil
}

func isJSON(raw json.RawMessage, first byte) bool {
	t := strings.TrimSpace(string(raw))
	return len(raw) <= maxJSONPerItem && t != "" && t[0] == first && json.Valid(raw)
}

type plan struct {
	report   *Report
	existing map[string]int32 // live site slug -> id in the target org
	byName   map[string]int32 // lower(name|kind) -> id
	channels map[string]int64 // lower(kind|name) -> id
	members  map[string]bool  // lower(email) of current members
}

func chanKey(kind, name string) string { return strings.ToLower(kind + "|" + name) }

func (s *Service) loadTarget(ctx context.Context, orgID int64) (plan, error) {
	p := plan{existing: map[string]int32{}, byName: map[string]int32{}, channels: map[string]int64{}, members: map[string]bool{}}
	rows, err := s.pool.Query(ctx, `select slug,id,lower(name||'|'||kind) from sites where org_id=$1 and deleted_at is null`, orgID)
	if err != nil {
		return p, err
	}
	for rows.Next() {
		var slug, name string
		var id int32
		if err := rows.Scan(&slug, &id, &name); err != nil {
			rows.Close()
			return p, err
		}
		p.existing[slug], p.byName[name] = id, id
	}
	rows.Close()
	rows, err = s.pool.Query(ctx, `select id,kind,name from notification_channels where org_id=$1 order by id desc`, orgID)
	if err != nil {
		return p, err
	}
	for rows.Next() {
		var id int64
		var kind, name string
		if err := rows.Scan(&id, &kind, &name); err != nil {
			rows.Close()
			return p, err
		}
		p.channels[chanKey(kind, name)] = id
	}
	rows.Close()
	rows, err = s.pool.Query(ctx, `select lower(u.email) from memberships m join users u on u.id=m.user_id where m.org_id=$1`, orgID)
	if err != nil {
		return p, err
	}
	defer rows.Close()
	for rows.Next() {
		var e string
		if err := rows.Scan(&e); err != nil {
			return p, err
		}
		p.members[e] = true
	}
	return p, rows.Err()
}

func (r *Report) warn(format string, a ...any) {
	if len(r.Warnings) < maxWarnings {
		r.Warnings = append(r.Warnings, fmt.Sprintf(format, a...))
	}
}

// Import recreates the bundle's settings in the principal's organization.
// Sites whose slug already exists there are left untouched. Share links and
// alert rules are never recreated; channels are matched by kind and name, and
// an alert whose channels are missing is imported disabled.
func (s *Service) Import(ctx context.Context, principal authz.Principal, b Bundle, opt ImportOptions) (Report, error) {
	if err := Validate(b); err != nil {
		return Report{}, err
	}
	target, err := s.loadTarget(ctx, principal.OrgID)
	if err != nil {
		return Report{}, err
	}
	report := Report{DryRun: opt.DryRun, Sites: []SiteResult{}, Members: []MemberResult{}, Warnings: []string{}}
	target.report = &report
	slugToID := map[string]int32{}
	for k, v := range target.existing {
		slugToID[k] = v
	}
	changed := false
	for _, site := range b.Sites {
		res := SiteResult{Name: site.Name, Slug: site.Slug, ShareLinksSkip: len(site.ShareLinks)}
		existingID, exists := target.existing[site.Slug]
		if !exists {
			existingID, exists = target.byName[strings.ToLower(site.Name+"|"+site.Kind)]
		}
		if exists {
			slugToID[site.Slug] = existingID
			res.Action = "skip_exists"
			report.warn("site %q already exists in this organization and was left unchanged", site.Name)
			report.Sites = append(report.Sites, res)
			continue
		}
		items := s.prepare(&target, site, &res)
		if opt.DryRun {
			res.Action = "create"
			report.Sites = append(report.Sites, res)
			continue
		}
		created, err := s.sites.Create(ctx, principal, sitesvc.CreateInput{
			Name: site.Name, Kind: site.Kind, Hosts: site.AllowedHosts, Timezone: site.Timezone,
			RetentionDays: site.RetentionDays, RevenueCurrency: site.RevenueCurrency,
		})
		if err != nil {
			res.Action, res.Error = "failed", err.Error()
			report.Sites = append(report.Sites, res)
			continue
		}
		changed = true
		slugToID[site.Slug] = created.ID
		if err := s.apply(ctx, created.ID, site, items); err != nil {
			res.Action, res.Error = "failed", "the site was created but its settings could not be applied: "+err.Error()
		} else {
			res.Action = "created"
		}
		report.Sites = append(report.Sites, res)
	}
	if changed && s.Refresh != nil {
		if err := s.Refresh(); err != nil {
			report.warn("site registry refresh failed: %v", err)
		}
	}
	s.members(ctx, principal, b, opt, &target, slugToID, &report)
	if !opt.DryRun {
		err := pgx.BeginFunc(ctx, s.pool, func(tx pgx.Tx) error {
			return audit.Write(ctx, tx, principal, "org.settings_imported", "organization", principal.OrgID,
				map[string]any{"sites": len(b.Sites), "invite_members": opt.InviteMembers})
		})
		if err != nil {
			return report, err
		}
	}
	return report, nil
}

// items are the validated children of one site, ready to insert.
type items struct {
	alerts   []resolvedAlert
	settings json.RawMessage
}

type resolvedAlert struct {
	Alert
	channelIDs []int64
	enabled    bool
}

// prepare counts what a site import will create and resolves alert channels.
func (s *Service) prepare(t *plan, site Site, res *SiteResult) items {
	res.Goals, res.Funnels, res.Segments = len(site.Goals), len(site.Funnels), len(site.Segments)
	var it items
	var dropped []string
	it.settings, dropped = SanitizeSettings(site.Settings)
	for _, key := range dropped {
		t.report.warn("setting %q of %q was not imported: invalid value", key, site.Slug)
	}
	for _, a := range site.Alerts {
		kind := alerts.AlertKind(a.Kind)
		if a.Kind == "app_rule" || !kind.AllowedOnSiteKind(site.Kind) {
			t.report.warn("alert %q on %q was skipped: kind %s is not importable on a %s project", a.Name, site.Slug, a.Kind, site.Kind)
			continue
		}
		a.Config = SanitizeAlertConfig(a.Config)
		ra := resolvedAlert{Alert: a, enabled: a.Enabled}
		for _, c := range a.Channels {
			if id, ok := t.channels[chanKey(c.Kind, c.Name)]; ok {
				ra.channelIDs = append(ra.channelIDs, id)
			} else {
				t.report.warn("alert %q on %q: channel %s %q does not exist in this organization", a.Name, site.Slug, c.Kind, c.Name)
			}
		}
		if len(ra.channelIDs) == 0 && ra.enabled {
			ra.enabled = false
			res.AlertsDisabled++
			t.report.warn("alert %q on %q was imported disabled: no matching channel", a.Name, site.Slug)
		}
		it.alerts = append(it.alerts, ra)
		res.Alerts++
	}
	return it
}

func (s *Service) apply(ctx context.Context, siteID int32, site Site, it items) error {
	return pgx.BeginFunc(ctx, s.pool, func(tx pgx.Tx) error {
		if _, err := tx.Exec(ctx, `update sites set settings = settings || $2::jsonb where id=$1`, siteID, string(it.settings)); err != nil {
			return err
		}
		goalIDs := map[string]int64{}
		for _, g := range site.Goals {
			var id int64
			if err := tx.QueryRow(ctx, `insert into goals(site_id,kind,match,value,prop_key,prop_value,name,currency,scroll_threshold)
				values($1,$2,$3,$4,nullif($5,''),nullif($6,''),$7,nullif($8,''),$9) returning id`,
				siteID, g.Kind, g.Match, g.Value, g.PropKey, g.PropValue, g.Name, g.Currency, g.ScrollThreshold).Scan(&id); err != nil {
				return fmt.Errorf("goal %q: %w", g.Name, err)
			}
			goalIDs[g.Ref] = id
		}
		for _, f := range site.Funnels {
			if _, err := tx.Exec(ctx, `insert into funnels(site_id,name,steps) values($1,$2,$3)`, siteID, f.Name, []byte(f.Steps)); err != nil {
				return fmt.Errorf("funnel %q: %w", f.Name, err)
			}
		}
		for _, g := range site.Segments {
			if _, err := tx.Exec(ctx, `insert into segments(site_id,name,filters) values($1,$2,$3) on conflict do nothing`, siteID, g.Name, []byte(g.Filters)); err != nil {
				return fmt.Errorf("segment %q: %w", g.Name, err)
			}
		}
		for _, a := range it.alerts {
			var goalID *int64
			if a.GoalRef != "" {
				id := goalIDs[a.GoalRef]
				goalID = &id
			}
			ids := a.channelIDs
			if ids == nil {
				ids = []int64{}
			}
			var config any
			if len(a.Config) > 0 {
				config = []byte(a.Config)
			}
			if _, err := tx.Exec(ctx, `insert into alerts(site_id,kind,threshold,goal_id,channel_ids,enabled,config,name) values($1,$2,$3,$4,$5,$6,$7,$8)`,
				siteID, a.Kind, a.Threshold, goalID, ids, a.enabled, config, a.Name); err != nil {
				return fmt.Errorf("alert %q: %w", a.Name, err)
			}
		}
		return nil
	})
}

func (s *Service) members(ctx context.Context, principal authz.Principal, b Bundle, opt ImportOptions, t *plan, slugToID map[string]int32, report *Report) {
	for _, m := range b.Members {
		res := MemberResult{Email: m.Email, Role: m.Role}
		switch {
		case t.members[strings.ToLower(m.Email)]:
			res.Action = "already_member"
		case !opt.InviteMembers:
			res.Action = "listed_only"
		case m.Role == "owner":
			res.Action, res.Note = "not_invited", "owners are never invited by an import; invite them with a role chosen in the new organization"
		case opt.DryRun:
			res.Action = "would_invite"
		default:
			var siteIDs []int32
			for _, slug := range m.SiteSlugs {
				if id, ok := slugToID[slug]; ok {
					siteIDs = append(siteIDs, id)
				}
			}
			if s.inv == nil {
				res.Action, res.Note = "failed", "invitations are unavailable"
				break
			}
			if _, err := s.inv.Create(ctx, principal, m.Email, authz.Role(m.Role), siteIDs, invites.Access{Capabilities: nz(m.Capabilities), BillingAdmin: m.BillingAdmin}); err != nil {
				res.Action, res.Note = "failed", err.Error()
			} else {
				res.Action = "invited"
			}
		}
		report.Members = append(report.Members, res)
	}
}

// SanitizeSettings filters site settings like FilterSettings and then checks
// every kept value with the parser the server uses to read it back, so a
// hand-edited bundle cannot store settings the site registry would reject. It
// returns the dropped keys.
func SanitizeSettings(raw json.RawMessage) (json.RawMessage, []string) {
	var kept map[string]json.RawMessage
	if json.Unmarshal(FilterSettings(raw), &kept) != nil {
		return json.RawMessage(`{}`), nil
	}
	var dropped []string
	for key, value := range kept {
		var err error
		switch key {
		case "tracker":
			_, err = coresites.ParseTrackerSettings(value)
		case "replay":
			_, err = coresites.ParseReplaySiteInput(value)
		case "app":
			_, err = coresites.ParseAppSettings(value)
		case "profiles_enabled", "heatmaps_enabled", "strict_origin", "respect_privacy_signals":
			var v bool
			err = json.Unmarshal(value, &v)
		default: // exclude_paths, exclude_ips, exclude_countries, blocked_referrers
			var v []string
			err = json.Unmarshal(value, &v)
		}
		if err != nil {
			delete(kept, key)
			dropped = append(dropped, key)
		}
	}
	out, _ := json.Marshal(kept)
	return out, dropped
}
