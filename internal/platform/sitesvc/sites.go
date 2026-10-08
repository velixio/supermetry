// Package sitesvc manages tracked sites and the analytics-core site registry.
package sitesvc

import (
	"context"
	"crypto/rand"
	"encoding/base32"
	"encoding/base64"
	"encoding/binary"
	"errors"
	"fmt"
	"regexp"
	"strings"
	"time"

	"github.com/jackc/pgx/v5"

	"panel/internal/db"
	"panel/internal/platform/audit"
	"panel/internal/platform/authz"
	"panel/internal/platform/store"
	coresites "panel/internal/sites"
	siteicon "panel/internal/sites/icon"
)

const maxCreateAttempts = 8

var (
	ErrForbidden = errors.New("forbidden")
	ErrInvalid   = errors.New("invalid site")
	ErrNotFound  = errors.New("site not found")

	publicIDEncoding = base32.StdEncoding.WithPadding(base32.NoPadding)
	hostLabelPattern = regexp.MustCompile(`^[a-z0-9](?:[a-z0-9-]{0,61}[a-z0-9])?$`)
)

type SitePatch struct {
	Name            *string
	Hosts           *[]string
	TZ              *string
	RetentionDays   *int
	ExcludeIPs      *[]string
	ExcludePaths    *[]string
	ProfilesEnabled *bool
	HeatmapsEnabled *bool
}

type Sites struct {
	store        *store.Store
	registry     *Registry
	discoverIcon func(context.Context, string) string
}

func New(data *store.Store, registry *Registry) *Sites {
	return &Sites{store: data, registry: registry, discoverIcon: siteicon.Discover}
}

func (s *Sites) Create(ctx context.Context, principal authz.Principal, name string, hosts []string, timezone string, retentionDays int) (coresites.Site, error) {
	if !authz.Can(principal, authz.ActionSiteCreate, 0) {
		return coresites.Site{}, ErrForbidden
	}
	name, hosts, location, retention, err := validateSite(name, hosts, timezone, retentionDays)
	if err != nil {
		return coresites.Site{}, err
	}

	var created store.Site
	err = s.store.Tx(ctx, func(tx pgx.Tx) error {
		for attempt := 0; attempt < maxCreateAttempts; attempt++ {
			publicID, err := newPublicID()
			if err != nil {
				return err
			}
			created, err = store.CreateSite(ctx, tx, store.Site{
				OrgID: principal.OrgID, PublicID: publicID, Name: name,
				AllowedHosts: hosts, Timezone: location.String(), RetentionClass: int16(retention),
			})
			if errors.Is(err, pgx.ErrNoRows) {
				continue
			}
			if err != nil {
				return err
			}
			return audit.Write(ctx, tx, principal, "site.created", "site", int64(created.ID), map[string]any{
				"name": name, "timezone": location.String(), "retention_days": retentionDays,
			})
		}
		return errors.New("generate unique site public ID")
	})
	if err != nil {
		return coresites.Site{}, err
	}
	if err := s.registry.Invalidate(); err != nil {
		return coresites.Site{}, fmt.Errorf("refresh site registry: %w", err)
	}
	return coreSite(created)
}

func (s *Sites) Update(ctx context.Context, principal authz.Principal, id int32, patch SitePatch) (coresites.Site, error) {
	if !authz.Can(principal, authz.ActionSiteEdit, id) {
		return coresites.Site{}, ErrForbidden
	}
	if patch.HeatmapsEnabled != nil && principal.Role != authz.RoleOwner && principal.Role != authz.RoleAdmin {
		return coresites.Site{}, ErrForbidden
	}
	var updated store.Site
	err := s.store.Tx(ctx, func(tx pgx.Tx) error {
		current, err := store.SiteForUpdate(ctx, tx, principal.OrgID, id)
		if errors.Is(err, pgx.ErrNoRows) {
			return ErrNotFound
		}
		if err != nil {
			return err
		}
		if patch.Name != nil {
			current.Name = *patch.Name
		}
		if patch.Hosts != nil {
			current.AllowedHosts = append([]string(nil), (*patch.Hosts)...)
		}
		if patch.TZ != nil {
			current.Timezone = *patch.TZ
		}
		if patch.RetentionDays != nil {
			current.RetentionClass = int16(*patch.RetentionDays)
		}
		if patch.ExcludeIPs != nil {
			current.ExcludeIPs = append([]string(nil), (*patch.ExcludeIPs)...)
		}
		if patch.ExcludePaths != nil {
			current.ExcludePaths = append([]string(nil), (*patch.ExcludePaths)...)
		}
		if patch.ProfilesEnabled != nil {
			current.ProfilesEnabled = *patch.ProfilesEnabled
		}
		if patch.HeatmapsEnabled != nil {
			current.HeatmapsEnabled = *patch.HeatmapsEnabled
		}
		name, hosts, location, retention, err := validateSite(
			current.Name, current.AllowedHosts, current.Timezone, int(current.RetentionClass),
		)
		if err != nil {
			return err
		}
		current.Name, current.AllowedHosts = name, hosts
		current.Timezone, current.RetentionClass = location.String(), int16(retention)
		exclusions, err := coresites.CompileExclusions(current.ExcludeIPs, current.ExcludePaths)
		if err != nil {
			return fmt.Errorf("%w: %v", ErrInvalid, err)
		}
		current.ExcludeIPs = append([]string(nil), exclusions.IPRules...)
		current.ExcludePaths = append([]string(nil), exclusions.PathRules...)
		updated, err = store.UpdateSite(ctx, tx, current)
		if err != nil {
			return err
		}
		metadata := map[string]any{
			"name": name, "timezone": location.String(), "retention_days": int(retention),
			"heatmaps_enabled": current.HeatmapsEnabled,
		}
		if patch.ProfilesEnabled != nil {
			metadata["profiles_enabled"] = current.ProfilesEnabled
		}
		return audit.Write(ctx, tx, principal, "site.updated", "site", int64(id), metadata)
	})
	if err != nil {
		return coresites.Site{}, err
	}
	if err := s.registry.Invalidate(); err != nil {
		return coresites.Site{}, fmt.Errorf("refresh site registry: %w", err)
	}
	return coreSite(updated)
}

func (s *Sites) Delete(ctx context.Context, principal authz.Principal, id int32) error {
	if !authz.Can(principal, authz.ActionSiteDelete, id) {
		return ErrForbidden
	}
	err := s.store.Tx(ctx, func(tx pgx.Tx) error {
		if err := store.SoftDeleteSite(ctx, tx, principal.OrgID, id); err != nil {
			if errors.Is(err, pgx.ErrNoRows) {
				return ErrNotFound
			}
			return err
		}
		return audit.Write(ctx, tx, principal, "site.deleted", "site", int64(id), nil)
	})
	if err != nil {
		return err
	}
	if err := s.registry.Invalidate(); err != nil {
		return fmt.Errorf("refresh site registry: %w", err)
	}
	return nil
}

// RefreshIcon discovers and stores the favicon for the site's first allowed
// host. Discovery deliberately happens outside a database transaction.
func (s *Sites) RefreshIcon(ctx context.Context, principal authz.Principal, id int32) (coresites.Site, error) {
	if !authz.Can(principal, authz.ActionSiteEdit, id) {
		return coresites.Site{}, ErrForbidden
	}
	site, ok := s.registry.Site(id)
	if !ok || site.OrgID != principal.OrgID {
		return coresites.Site{}, ErrNotFound
	}
	if len(site.AllowedHosts) == 0 {
		return site, nil
	}
	iconURL := s.discoverIcon(ctx, site.AllowedHosts[0])
	updated, err := store.SetSiteIcon(ctx, s.store, principal.OrgID, id, iconURL, time.Now().UTC())
	if errors.Is(err, pgx.ErrNoRows) {
		return coresites.Site{}, ErrNotFound
	}
	if err != nil {
		return coresites.Site{}, err
	}
	if err := s.registry.Invalidate(); err != nil {
		return coresites.Site{}, fmt.Errorf("refresh site registry: %w", err)
	}
	return coreSite(updated)
}

func (s *Sites) List(ctx context.Context, principal authz.Principal, cursor string, limit int) ([]coresites.Site, string, error) {
	if principal.OrgID == 0 || principal.Role == authz.RoleShare {
		return nil, "", ErrForbidden
	}
	after, err := decodeCursor(cursor)
	if err != nil {
		return nil, "", err
	}
	if limit <= 0 || limit > 100 {
		return nil, "", fmt.Errorf("%w: limit must be between 1 and 100", ErrInvalid)
	}
	rows, err := store.ListSites(ctx, s.store, principal.OrgID, principal.SiteIDs, after, limit+1)
	if err != nil {
		return nil, "", err
	}
	next := ""
	if len(rows) > limit {
		next = encodeCursor(rows[limit-1].ID)
		rows = rows[:limit]
	}
	result := make([]coresites.Site, 0, len(rows))
	for _, row := range rows {
		site, err := coreSite(row)
		if err != nil {
			return nil, "", err
		}
		result = append(result, site)
	}
	return result, next, nil
}

func validateSite(name string, hosts []string, timezone string, retentionDays int) (string, []string, *time.Location, db.RetentionClass, error) {
	name = strings.TrimSpace(name)
	if name == "" {
		return "", nil, nil, 0, fmt.Errorf("%w: name is required", ErrInvalid)
	}
	location, err := time.LoadLocation(timezone)
	if err != nil {
		return "", nil, nil, 0, fmt.Errorf("%w: unknown timezone", ErrInvalid)
	}
	retention, ok := retentionClass(retentionDays)
	if !ok {
		return "", nil, nil, 0, fmt.Errorf("%w: unsupported retention", ErrInvalid)
	}
	hosts, err = normalizeHosts(hosts)
	if err != nil {
		return "", nil, nil, 0, err
	}
	return name, hosts, location, retention, nil
}

func normalizeHosts(hosts []string) ([]string, error) {
	if len(hosts) == 0 {
		return nil, fmt.Errorf("%w: at least one host is required", ErrInvalid)
	}
	normalized := make([]string, 0, len(hosts))
	seen := make(map[string]struct{}, len(hosts))
	for _, raw := range hosts {
		host := strings.ToLower(strings.TrimSpace(raw))
		name := host
		if strings.HasPrefix(name, "*.") {
			name = strings.TrimPrefix(name, "*.")
		}
		if !validHostname(name) || strings.ContainsAny(host, ":/?#@") {
			return nil, fmt.Errorf("%w: invalid host %q", ErrInvalid, raw)
		}
		if _, ok := seen[host]; ok {
			continue
		}
		seen[host] = struct{}{}
		normalized = append(normalized, host)
	}
	return normalized, nil
}

func validHostname(host string) bool {
	if host == "" || len(host) > 253 || strings.HasPrefix(host, ".") || strings.HasSuffix(host, ".") {
		return false
	}
	for _, label := range strings.Split(host, ".") {
		if !hostLabelPattern.MatchString(label) {
			return false
		}
	}
	return true
}

func retentionClass(days int) (db.RetentionClass, bool) {
	switch days {
	case 0:
		return db.RetForever, true
	case 7:
		return db.Ret7, true
	case 30:
		return db.Ret30, true
	case 90:
		return db.Ret90, true
	case 365:
		return db.Ret365, true
	default:
		return 0, false
	}
}

func coreSite(row store.Site) (coresites.Site, error) {
	location, err := time.LoadLocation(row.Timezone)
	if err != nil {
		return coresites.Site{}, fmt.Errorf("load timezone for site %d: %w", row.ID, err)
	}
	retention, ok := retentionClass(int(row.RetentionClass))
	if !ok {
		return coresites.Site{}, fmt.Errorf("site %d has invalid retention class %d", row.ID, row.RetentionClass)
	}
	exclusions, err := coresites.CompileExclusions(row.ExcludeIPs, row.ExcludePaths)
	if err != nil {
		return coresites.Site{}, fmt.Errorf("load exclusions for site %d: %w", row.ID, err)
	}
	return coresites.Site{
		ID: row.ID, OrgID: row.OrgID, PublicID: row.PublicID, TZ: location, Retention: retention,
		AllowedHosts: append([]string(nil), row.AllowedHosts...), Exclusions: exclusions,
		ProfilesEnabled: row.ProfilesEnabled,
		HeatmapsEnabled: row.HeatmapsEnabled,
		IconURL:         row.IconURL,
	}, nil
}

func newPublicID() (string, error) {
	var random [8]byte
	if _, err := rand.Read(random[:]); err != nil {
		return "", fmt.Errorf("generate site public ID: %w", err)
	}
	return strings.ToLower(publicIDEncoding.EncodeToString(random[:])[:12]), nil
}

func encodeCursor(id int32) string {
	var value [4]byte
	binary.BigEndian.PutUint32(value[:], uint32(id))
	return base64.RawURLEncoding.EncodeToString(value[:])
}

func decodeCursor(cursor string) (int32, error) {
	if cursor == "" {
		return 0, nil
	}
	value, err := base64.RawURLEncoding.DecodeString(cursor)
	if err != nil || len(value) != 4 {
		return 0, fmt.Errorf("%w: invalid cursor", ErrInvalid)
	}
	id := binary.BigEndian.Uint32(value)
	if id > uint32(^uint32(0)>>1) {
		return 0, fmt.Errorf("%w: invalid cursor", ErrInvalid)
	}
	return int32(id), nil
}
