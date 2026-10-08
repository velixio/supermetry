package sitesvc

import (
	"context"
	"errors"
	"net/netip"
	"strings"
	"testing"
	"time"

	"github.com/jackc/pgx/v5/pgxpool"

	"panel/internal/db"
	"panel/internal/platform/authz"
	"panel/internal/platform/store"
	"panel/internal/sites"
	"panel/internal/testpg"
)

func TestRegistryReflectsWritesAndHidesDeleted(t *testing.T) {
	service, registry, principal, _ := newSites(t)
	ctx := context.Background()

	site, err := service.Create(ctx, principal, "Blog", []string{"blog.example.com"}, "Asia/Kolkata", 30)
	if err != nil {
		t.Fatal(err)
	}
	got, ok := registry.ByPublicID(site.PublicID)
	if !ok || got.TZ.String() != "Asia/Kolkata" {
		t.Fatalf("registry lookup = (%+v, %t)", got, ok)
	}
	// API site checks compare the site's org with the caller's, so the registry
	// must always carry the owning org.
	if got.OrgID != principal.OrgID || got.OrgID == 0 {
		t.Fatalf("registry site OrgID = %d, want %d", got.OrgID, principal.OrgID)
	}
	if byID, ok := registry.Site(site.ID); !ok || byID.OrgID != principal.OrgID {
		t.Fatalf("registry Site(id) OrgID = %d, want %d", byID.OrgID, principal.OrgID)
	}
	if err := service.Delete(ctx, principal, site.ID); err != nil {
		t.Fatal(err)
	}
	if _, ok := registry.ByPublicID(site.PublicID); ok {
		t.Fatal("deleted site still served")
	}
}

func TestRegistryLoadsProfilesEnabledFromSettings(t *testing.T) {
	service, registry, principal, pool := newSites(t)
	ctx := context.Background()
	site, err := service.Create(ctx, principal, "Profiles", []string{"profiles.example.com"}, "UTC", 30)
	if err != nil {
		t.Fatal(err)
	}
	if site.ProfilesEnabled {
		t.Fatal("profiles must default off")
	}
	if _, err := pool.Exec(ctx, `
		update sites
		set settings = jsonb_set(settings, '{profiles_enabled}', 'true'::jsonb)
		where id = $1`, site.ID); err != nil {
		t.Fatal(err)
	}
	if err := registry.Invalidate(); err != nil {
		t.Fatal(err)
	}
	loaded, ok := registry.Site(site.ID)
	if !ok || !loaded.ProfilesEnabled {
		t.Fatalf("registry site = (%+v, %t), want profiles enabled", loaded, ok)
	}
}

func TestRefreshIconDiscoversFirstHostAndPersistsSettings(t *testing.T) {
	service, registry, principal, pool := newSites(t)
	ctx := context.Background()
	site, err := service.Create(ctx, principal, "Icons", []string{"first.example.com", "second.example.com"}, "UTC", 30)
	if err != nil {
		t.Fatal(err)
	}
	var discoveredHost string
	service.discoverIcon = func(_ context.Context, host string) string {
		discoveredHost = host
		return "https://first.example.com/icon.svg"
	}

	updated, err := service.RefreshIcon(ctx, principal, site.ID)
	if err != nil {
		t.Fatal(err)
	}
	if discoveredHost != "first.example.com" || updated.IconURL != "https://first.example.com/icon.svg" {
		t.Fatalf("host=%q updated icon=%q", discoveredHost, updated.IconURL)
	}
	loaded, ok := registry.Site(site.ID)
	if !ok || loaded.IconURL != updated.IconURL {
		t.Fatalf("registry site=(%+v,%t), want refreshed icon", loaded, ok)
	}
	var iconURL, checkedAt string
	if err := pool.QueryRow(ctx, `select settings->>'icon_url', settings->>'icon_checked_at' from sites where id=$1`, site.ID).Scan(&iconURL, &checkedAt); err != nil {
		t.Fatal(err)
	}
	if iconURL != updated.IconURL {
		t.Fatalf("stored icon_url=%q, want %q", iconURL, updated.IconURL)
	}
	if _, err := time.Parse(time.RFC3339, checkedAt); err != nil {
		t.Fatalf("stored icon_checked_at=%q: %v", checkedAt, err)
	}
}

func TestRefreshIconKeepsStoredIconWhenDiscoveryFindsNothing(t *testing.T) {
	service, registry, principal, pool := newSites(t)
	ctx := context.Background()
	site, err := service.Create(ctx, principal, "Icons", []string{"example.com"}, "UTC", 30)
	if err != nil {
		t.Fatal(err)
	}
	const (
		storedIcon = "https://example.com/icon.svg"
		oldChecked = "2000-01-01T00:00:00Z"
	)
	if _, err := pool.Exec(ctx, `
		update sites set settings = settings || jsonb_build_object(
			'icon_url', $2::text, 'icon_checked_at', $3::text
		) where id = $1`, site.ID, storedIcon, oldChecked); err != nil {
		t.Fatal(err)
	}
	if err := registry.Invalidate(); err != nil {
		t.Fatal(err)
	}
	service.discoverIcon = func(context.Context, string) string { return "" }

	updated, err := service.RefreshIcon(ctx, principal, site.ID)
	if err != nil {
		t.Fatal(err)
	}
	if updated.IconURL != storedIcon {
		t.Fatalf("updated icon_url=%q, want stored icon %q", updated.IconURL, storedIcon)
	}
	var iconURL, checkedAt string
	if err := pool.QueryRow(ctx, `select settings->>'icon_url', settings->>'icon_checked_at' from sites where id=$1`, site.ID).Scan(&iconURL, &checkedAt); err != nil {
		t.Fatal(err)
	}
	if iconURL != storedIcon {
		t.Fatalf("stored icon_url=%q, want %q", iconURL, storedIcon)
	}
	if checkedAt == oldChecked {
		t.Fatalf("stored icon_checked_at=%q, want refreshed timestamp", checkedAt)
	}
	if _, err := time.Parse(time.RFC3339, checkedAt); err != nil {
		t.Fatalf("stored icon_checked_at=%q: %v", checkedAt, err)
	}
}

func TestUpdateProfilesEnabledPersistsRefreshesAndAudits(t *testing.T) {
	service, registry, principal, pool := newSites(t)
	ctx := context.Background()
	site, err := service.Create(ctx, principal, "Profiles", []string{"profiles.example.com"}, "UTC", 30)
	if err != nil {
		t.Fatal(err)
	}
	enabled := true
	updated, err := service.Update(ctx, principal, site.ID, SitePatch{ProfilesEnabled: &enabled})
	if err != nil {
		t.Fatal(err)
	}
	loaded, ok := registry.Site(site.ID)
	if !updated.ProfilesEnabled || !ok || !loaded.ProfilesEnabled {
		t.Fatalf("updated=%t registry=(%t,%t), want enabled", updated.ProfilesEnabled, ok, loaded.ProfilesEnabled)
	}
	var stored bool
	if err := pool.QueryRow(ctx, `
		select settings->'profiles_enabled' = 'true'::jsonb
		from sites where id = $1`, site.ID).Scan(&stored); err != nil {
		t.Fatal(err)
	}
	if !stored {
		t.Fatal("profiles_enabled was not persisted")
	}
	var audited *bool
	if err := pool.QueryRow(ctx, `
		select (metadata->>'profiles_enabled')::boolean
		from audit_log
		where target_type = 'site' and target_id = $1 and action = 'site.updated'
		order by id desc limit 1`, site.ID).Scan(&audited); err != nil {
		t.Fatal(err)
	}
	if audited == nil || !*audited {
		t.Fatalf("audit profiles_enabled = %v, want true", audited)
	}
}

func TestListFiltersRestrictedPrincipal(t *testing.T) {
	service, _, principal, _ := newSites(t)
	ctx := context.Background()
	a, err := service.Create(ctx, principal, "A", []string{"a.io"}, "UTC", 30)
	if err != nil {
		t.Fatal(err)
	}
	if _, err := service.Create(ctx, principal, "B", []string{"b.io"}, "UTC", 30); err != nil {
		t.Fatal(err)
	}

	restricted := authz.Principal{Role: authz.RoleViewer, OrgID: principal.OrgID, SiteIDs: []int32{a.ID}}
	got, next, err := service.List(ctx, restricted, "", 50)
	if err != nil {
		t.Fatal(err)
	}
	if len(got) != 1 || got[0].ID != a.ID || next != "" {
		t.Fatalf("restricted list leaked: sites=%+v next=%q", got, next)
	}
}

func TestCreateValidates(t *testing.T) {
	service, _, principal, _ := newSites(t)
	ctx := context.Background()
	tests := []struct {
		name      string
		hosts     []string
		timezone  string
		retention int
	}{
		{name: "bad timezone", hosts: []string{"example.com"}, timezone: "Mars/Base", retention: 30},
		{name: "unsupported retention", hosts: []string{"example.com"}, timezone: "UTC", retention: 45},
		{name: "empty hosts", timezone: "UTC", retention: 30},
		{name: "host with port", hosts: []string{"example.com:443"}, timezone: "UTC", retention: 30},
		{name: "host with scheme", hosts: []string{"https://example.com"}, timezone: "UTC", retention: 30},
	}
	for _, test := range tests {
		t.Run(test.name, func(t *testing.T) {
			if _, err := service.Create(ctx, principal, "Invalid", test.hosts, test.timezone, test.retention); !errors.Is(err, ErrInvalid) {
				t.Fatalf("Create error = %v, want ErrInvalid", err)
			}
		})
	}
}

func TestUpdateRefreshesRegistryAndWritesAudit(t *testing.T) {
	service, registry, principal, pool := newSites(t)
	ctx := context.Background()
	site, err := service.Create(ctx, principal, "Blog", []string{"blog.example.com"}, "UTC", 30)
	if err != nil {
		t.Fatal(err)
	}
	name := "Docs"
	timezone := "Asia/Kathmandu"
	hosts := []string{"docs.example.com", "*.docs.example.com"}
	retention := 90
	updated, err := service.Update(ctx, principal, site.ID, SitePatch{
		Name: &name, Hosts: &hosts, TZ: &timezone, RetentionDays: &retention,
	})
	if err != nil {
		t.Fatal(err)
	}
	if updated.Retention != db.Ret90 || updated.TZ.String() != timezone || !updated.Allows("www.docs.example.com") {
		t.Fatalf("updated site = %+v", updated)
	}
	if got, ok := registry.Site(site.ID); !ok || got.TZ.String() != timezone {
		t.Fatalf("registry lookup = (%+v, %t)", got, ok)
	}
	var actions string
	if err := pool.QueryRow(ctx, `select string_agg(action, ',' order by id) from audit_log where org_id = $1`, principal.OrgID).Scan(&actions); err != nil {
		t.Fatal(err)
	}
	if actions != "site.created,site.updated" {
		t.Fatalf("audit actions = %q", actions)
	}
}

func TestUpdateExclusionsPersistsAndRefreshesRegistry(t *testing.T) {
	service, registry, principal, pool := newSites(t)
	ctx := context.Background()
	site, err := service.Create(ctx, principal, "Blog", []string{"blog.example.com"}, "UTC", 30)
	if err != nil {
		t.Fatal(err)
	}
	ips := []string{"203.0.113.7", "2001:db8::/32"}
	paths := []string{"/admin/*", "/preview/**", "/internal"}
	updated, err := service.Update(ctx, principal, site.ID, SitePatch{
		ExcludeIPs: &ips, ExcludePaths: &paths,
	})
	if err != nil {
		t.Fatal(err)
	}
	if got := strings.Join(updated.Exclusions.IPRules, ","); got != "203.0.113.7/32,2001:db8::/32" {
		t.Fatalf("updated IP rules = %q", got)
	}
	loaded, ok := registry.Site(site.ID)
	if !ok || !loaded.Exclusions.ExcludesIP(netip.MustParseAddr("::ffff:203.0.113.7")) || !loaded.Exclusions.ExcludesPath("/preview/build/7") {
		t.Fatalf("registry exclusions not refreshed: found=%t site=%+v", ok, loaded)
	}
	var storedIPs, storedPaths string
	if err := pool.QueryRow(ctx, `select settings->'exclude_ips', settings->'exclude_paths' from sites where id=$1`, site.ID).Scan(&storedIPs, &storedPaths); err != nil {
		t.Fatal(err)
	}
	if storedIPs != `["203.0.113.7/32", "2001:db8::/32"]` || storedPaths != `["/admin/*", "/preview/**", "/internal"]` {
		t.Fatalf("stored exclusions = (%s, %s)", storedIPs, storedPaths)
	}

	empty := []string{}
	if _, err := service.Update(ctx, principal, site.ID, SitePatch{ExcludeIPs: &empty, ExcludePaths: &empty}); err != nil {
		t.Fatal(err)
	}
	loaded, ok = registry.Site(site.ID)
	if !ok || loaded.Exclusions.ExcludesIP(netip.MustParseAddr("203.0.113.7")) || loaded.Exclusions.ExcludesPath("/internal") {
		t.Fatalf("replace-to-empty exclusions = %+v, found=%t", loaded.Exclusions, ok)
	}
}

func TestUpdateTrackerAndCountryExclusionsRoundTripsAndOmittedPatchPreserves(t *testing.T) {
	service, registry, principal, pool := newSites(t)
	ctx := context.Background()
	site, err := service.Create(ctx, principal, "Blog", []string{"blog.example.com"}, "UTC", 30)
	if err != nil {
		t.Fatal(err)
	}
	hash := true
	tracker := sites.TrackerSettingsInput{Hash: &hash}
	countries := []string{"ru", " RU "}
	updated, err := service.Update(ctx, principal, site.ID, SitePatch{
		Tracker: &tracker, ExcludeCountries: &countries,
	})
	if err != nil {
		t.Fatal(err)
	}
	if updated.Tracker.Hash == nil || !*updated.Tracker.Hash || strings.Join(updated.Exclusions.CountryRules, ",") != "RU" {
		t.Fatalf("updated tracker/countries = %+v/%v", updated.Tracker, updated.Exclusions.CountryRules)
	}
	forms := false
	partial := sites.TrackerSettingsInput{Forms: &forms}
	updated, err = service.Update(ctx, principal, site.ID, SitePatch{Tracker: &partial})
	if err != nil {
		t.Fatal(err)
	}
	if updated.Tracker.Hash == nil || !*updated.Tracker.Hash || updated.Tracker.Forms == nil || *updated.Tracker.Forms {
		t.Fatalf("partial tracker PATCH reset or lost fields: %+v", updated.Tracker)
	}
	loaded, ok := registry.Site(site.ID)
	if !ok || loaded.Tracker.Hash == nil || !*loaded.Tracker.Hash || !loaded.Exclusions.ExcludesCountry("RU") {
		t.Fatalf("registry tracker/countries = %+v/%+v, found=%t", loaded.Tracker, loaded.Exclusions, ok)
	}
	*loaded.Tracker.Hash = false
	loaded.Exclusions.Countries["US"] = struct{}{}
	immutable, ok := registry.Site(site.ID)
	if !ok || immutable.Tracker.Hash == nil || !*immutable.Tracker.Hash || immutable.Exclusions.ExcludesCountry("US") {
		t.Fatalf("registry returned mutable tracker/exclusions: %+v/%+v", immutable.Tracker, immutable.Exclusions)
	}

	name := "Renamed"
	if _, err := service.Update(ctx, principal, site.ID, SitePatch{Name: &name}); err != nil {
		t.Fatal(err)
	}
	loaded, ok = registry.Site(site.ID)
	if !ok || loaded.Tracker.Hash == nil || !*loaded.Tracker.Hash || !loaded.Exclusions.ExcludesCountry("RU") {
		t.Fatalf("omitted patch replaced tracker/countries: %+v/%+v", loaded.Tracker, loaded.Exclusions)
	}

	var storedTrackerMatches bool
	var storedCountries string
	if err := pool.QueryRow(ctx, `select settings->'tracker'='{"hash":true,"forms":false}'::jsonb, settings->'exclude_countries' from sites where id=$1`, site.ID).Scan(&storedTrackerMatches, &storedCountries); err != nil {
		t.Fatal(err)
	}
	if !storedTrackerMatches || storedCountries != `["RU"]` {
		t.Fatalf("stored tracker match/countries = %t/%s", storedTrackerMatches, storedCountries)
	}
}

func TestRegistryMalformedTrackerUsesDefaults(t *testing.T) {
	service, registry, principal, pool := newSites(t)
	ctx := context.Background()
	site, err := service.Create(ctx, principal, "Blog", []string{"blog.example.com"}, "UTC", 30)
	if err != nil {
		t.Fatal(err)
	}
	if _, err := pool.Exec(ctx, `update sites set settings=jsonb_set(settings,'{tracker}','[]'::jsonb) where id=$1`, site.ID); err != nil {
		t.Fatal(err)
	}
	if err := registry.Invalidate(); err != nil {
		t.Fatalf("Invalidate() error = %v, want malformed tracker to fall back", err)
	}
	loaded, ok := registry.Site(site.ID)
	if !ok || !loaded.Tracker.Resolve(false).Outbound {
		t.Fatalf("registry site = %+v, found=%t, want tracker defaults", loaded, ok)
	}
}

func TestUpdateHeatmapsPersistsRefreshesRegistryAndAudits(t *testing.T) {
	service, registry, principal, pool := newSites(t)
	ctx := context.Background()
	site, err := service.Create(ctx, principal, "Blog", []string{"blog.example.com"}, "UTC", 30)
	if err != nil {
		t.Fatal(err)
	}
	if site.HeatmapsEnabled {
		t.Fatal("heatmaps must default off")
	}
	enabled := true
	updated, err := service.Update(ctx, principal, site.ID, SitePatch{HeatmapsEnabled: &enabled})
	if err != nil {
		t.Fatal(err)
	}
	loaded, ok := registry.Site(site.ID)
	if !ok || !updated.HeatmapsEnabled || !loaded.HeatmapsEnabled {
		t.Fatalf("heatmap setting not refreshed: updated=%+v loaded=%+v found=%t", updated, loaded, ok)
	}
	var stored bool
	if err := pool.QueryRow(ctx, `select coalesce((settings->>'heatmaps_enabled')::boolean, false) from sites where id=$1`, site.ID).Scan(&stored); err != nil {
		t.Fatal(err)
	}
	if !stored {
		t.Fatal("heatmaps_enabled was not stored")
	}
	var details []byte
	if err := pool.QueryRow(ctx, `select metadata from audit_log where action='site.updated' and target_id=$1 order by id desc limit 1`, site.ID).Scan(&details); err != nil {
		t.Fatal(err)
	}
	if !strings.Contains(string(details), `"heatmaps_enabled": true`) {
		t.Fatalf("audit details = %s", details)
	}
}

func TestUpdateHeatmapsRejectsEditor(t *testing.T) {
	service, _, principal, _ := newSites(t)
	site, err := service.Create(context.Background(), principal, "Blog", []string{"blog.example.com"}, "UTC", 30)
	if err != nil {
		t.Fatal(err)
	}
	enabled := true
	editor := principal
	editor.Role = authz.RoleEditor
	if _, err := service.Update(context.Background(), editor, site.ID, SitePatch{HeatmapsEnabled: &enabled}); !errors.Is(err, ErrForbidden) {
		t.Fatalf("editor heatmap update error = %v, want ErrForbidden", err)
	}
}

func TestUpdateTrackerAndCountryExclusionsRejectsEditor(t *testing.T) {
	service, _, principal, _ := newSites(t)
	site, err := service.Create(context.Background(), principal, "Blog", []string{"blog.example.com"}, "UTC", 30)
	if err != nil {
		t.Fatal(err)
	}
	editor := principal
	editor.Role = authz.RoleEditor
	hash := true
	tracker := sites.TrackerSettingsInput{Hash: &hash}
	countries := []string{"RU"}
	tests := []struct {
		name  string
		patch SitePatch
	}{
		{name: "tracker", patch: SitePatch{Tracker: &tracker}},
		{name: "countries", patch: SitePatch{ExcludeCountries: &countries}},
	}
	for _, test := range tests {
		t.Run(test.name, func(t *testing.T) {
			if _, err := service.Update(context.Background(), editor, site.ID, test.patch); !errors.Is(err, ErrForbidden) {
				t.Fatalf("Update error = %v, want ErrForbidden", err)
			}
		})
	}
}

func TestUpdateExclusionsNamesInvalidEntry(t *testing.T) {
	service, _, principal, _ := newSites(t)
	ctx := context.Background()
	site, err := service.Create(ctx, principal, "Blog", []string{"blog.example.com"}, "UTC", 30)
	if err != nil {
		t.Fatal(err)
	}

	ips := []string{"203.0.113.0/99"}
	_, err = service.Update(ctx, principal, site.ID, SitePatch{ExcludeIPs: &ips})
	if !errors.Is(err, ErrInvalid) || !strings.Contains(err.Error(), `203.0.113.0/99`) {
		t.Fatalf("Update error = %v, want ErrInvalid naming bad entry", err)
	}
}

func TestCrossOrgAndForbiddenWritesAreHiddenOrDenied(t *testing.T) {
	service, _, principal, pool := newSites(t)
	ctx := context.Background()
	site, err := service.Create(ctx, principal, "Blog", []string{"blog.example.com"}, "UTC", 30)
	if err != nil {
		t.Fatal(err)
	}
	otherOrg := insertOrg(t, pool, "Other", "other")
	other := authz.Principal{UserID: principal.UserID, OrgID: otherOrg, Role: authz.RoleOwner}
	name := "Stolen"
	if _, err := service.Update(ctx, other, site.ID, SitePatch{Name: &name}); !errors.Is(err, ErrNotFound) {
		t.Fatalf("cross-org Update error = %v, want ErrNotFound", err)
	}
	viewer := authz.Principal{UserID: principal.UserID, OrgID: principal.OrgID, Role: authz.RoleViewer, SiteIDs: []int32{site.ID}}
	if err := service.Delete(ctx, viewer, site.ID); !errors.Is(err, ErrForbidden) {
		t.Fatalf("viewer Delete error = %v, want ErrForbidden", err)
	}
}

func TestSnippet(t *testing.T) {
	want := `<script defer src="https://stats.example.com/js/abc123def456.js"></script>`
	for _, full := range []bool{false, true} {
		if got := Snippet("https://stats.example.com/", "/js/p.js", "/api/public/v1/events", "abc123def456", full); got != want {
			t.Fatalf("Snippet(full=%t) = %q, want %q", full, got, want)
		}
	}
}

func newSites(t *testing.T) (*Sites, *Registry, authz.Principal, *pgxpool.Pool) {
	t.Helper()
	pool, _ := testpg.New(t)
	ctx := context.Background()
	if err := db.Migrate(ctx, pool); err != nil {
		t.Fatal(err)
	}
	var userID int64
	if err := pool.QueryRow(ctx, `insert into users(email, name, password_hash) values ('owner@example.test', 'Owner', 'hash') returning id`).Scan(&userID); err != nil {
		t.Fatal(err)
	}
	orgID := insertOrg(t, pool, "Test", "test")
	if _, err := pool.Exec(ctx, `insert into memberships(org_id, user_id, role) values ($1, $2, 'owner')`, orgID, userID); err != nil {
		t.Fatal(err)
	}
	data := &store.Store{Pool: pool}
	registry := NewRegistry(data, time.Hour)
	t.Cleanup(registry.Close)
	return New(data, registry), registry, authz.Principal{UserID: userID, OrgID: orgID, Role: authz.RoleOwner}, pool
}

func insertOrg(t *testing.T, pool *pgxpool.Pool, name, slug string) int64 {
	t.Helper()
	var id int64
	if err := pool.QueryRow(context.Background(), `insert into orgs(name, slug) values ($1, $2) returning id`, name, slug).Scan(&id); err != nil {
		t.Fatal(err)
	}
	return id
}
