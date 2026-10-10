package sitesvc

import (
	"context"
	"errors"
	"net/netip"
	"strings"
	"testing"
	"time"

	"github.com/jackc/pgx/v5/pgxpool"

	"supermetry/internal/db"
	"supermetry/internal/platform/authz"
	"supermetry/internal/platform/store"
	"supermetry/internal/privacy"
	"supermetry/internal/sites"
	"supermetry/internal/testpg"
)

func TestRegistryReflectsWritesAndHidesDeleted(t *testing.T) {
	service, registry, principal, _ := newSites(t)
	ctx := context.Background()

	site, err := service.Create(ctx, principal, "Blog", []string{"blog.example.com"}, "Asia/Kolkata", 30, "USD")
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
	if got.RevenueCurrency != "USD" || got.RevenueCurrencyState != sites.RevenueCurrencyConfigured {
		t.Fatalf("registry revenue currency=%q/%q", got.RevenueCurrency, got.RevenueCurrencyState)
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

func TestCreateRequiresUppercaseProjectCurrency(t *testing.T) {
	service, _, principal, _ := newSites(t)
	for _, currency := range []string{"", "usd", "US", "USDX"} {
		if _, err := service.Create(context.Background(), principal, "Bad currency", []string{"example.test"}, "UTC", 30, currency); !errors.Is(err, ErrInvalid) {
			t.Errorf("currency %q error=%v, want invalid", currency, err)
		}
	}
}

type failingRegistry struct{ err error }

func (f failingRegistry) Invalidate() error { return f.err }

func TestRestoreSiteLimitAndPostCommitInvalidation(t *testing.T) {
	service, registry, principal, pool := newSites(t)
	ctx := context.Background()
	service.SetSiteLimit(1)
	deleted, err := service.Create(ctx, principal, "Deleted", []string{"deleted.example.test"}, "UTC", 30, "USD")
	if err != nil {
		t.Fatal(err)
	}
	if err := service.Delete(ctx, principal, deleted.ID); err != nil {
		t.Fatal(err)
	}
	if _, err := service.Create(ctx, principal, "Live", []string{"live.example.test"}, "UTC", 30, "USD"); err != nil {
		t.Fatal(err)
	}
	if err := service.Restore(ctx, principal, deleted.ID); !errors.Is(err, ErrSiteLimit) {
		t.Fatalf("Restore() error = %v, want ErrSiteLimit", err)
	}
	var stillDeleted bool
	if err := pool.QueryRow(ctx, `select deleted_at is not null from sites where id=$1`, deleted.ID).Scan(&stillDeleted); err != nil || !stillDeleted {
		t.Fatalf("site deleted=%t error=%v, want unchanged", stillDeleted, err)
	}

	service.SetSiteLimit(0)
	service.registry = &Registry{}
	_ = registry
	// A real registry error is injected through the service hook so the committed
	// restore remains successful.
	service.SetInvalidator(failingRegistry{err: errors.New("refresh failed")})
	if err := service.Restore(ctx, principal, deleted.ID); err != nil {
		t.Fatalf("Restore() after commit error = %v, want nil", err)
	}
	var restored bool
	if err := pool.QueryRow(ctx, `select deleted_at is null from sites where id=$1`, deleted.ID).Scan(&restored); err != nil || !restored {
		t.Fatalf("site restored=%t error=%v, want restored despite invalidation failure", restored, err)
	}
}

func TestUpdateRevenueCurrencySerializesConcurrentPermanentSelection(t *testing.T) {
	service, registry, principal, pool := newSites(t)
	ctx := context.Background()
	var siteID int32
	if err := pool.QueryRow(ctx, `insert into sites(org_id,public_id,name,allowed_hosts,timezone,retention_class) values($1,'legacycur001','Legacy',array['example.test'],'UTC',30) returning id`, principal.OrgID).Scan(&siteID); err != nil {
		t.Fatal(err)
	}
	if err := registry.Invalidate(); err != nil {
		t.Fatal(err)
	}
	currencies := []string{"USD", "EUR"}
	errorsByCurrency := make(chan error, len(currencies))
	start := make(chan struct{})
	for _, currency := range currencies {
		currency := currency
		go func() {
			<-start
			_, err := service.Update(ctx, principal, siteID, SitePatch{RevenueCurrency: &currency})
			errorsByCurrency <- err
		}()
	}
	close(start)
	var succeeded, rejected int
	for range currencies {
		if err := <-errorsByCurrency; err == nil {
			succeeded++
		} else if errors.Is(err, ErrInvalid) {
			rejected++
		} else {
			t.Fatalf("unexpected selection error: %v", err)
		}
	}
	if succeeded != 1 || rejected != 1 {
		t.Fatalf("successful=%d rejected=%d, want 1/1", succeeded, rejected)
	}
	var currency, state string
	if err := pool.QueryRow(ctx, `select revenue_currency,revenue_currency_state from sites where id=$1`, siteID).Scan(&currency, &state); err != nil {
		t.Fatal(err)
	}
	if state != "configured" || (currency != "USD" && currency != "EUR") {
		t.Fatalf("stored currency=%q state=%q", currency, state)
	}
	loaded, ok := registry.Site(siteID)
	if !ok || loaded.RevenueCurrency != currency || loaded.RevenueCurrencyState != sites.RevenueCurrencyConfigured {
		t.Fatalf("registry site=(%+v,%t), want %s configured", loaded, ok, currency)
	}
	var auditedCurrency string
	if err := pool.QueryRow(ctx, `select metadata->>'revenue_currency' from audit_log where action='site.updated' and target_id=$1 order by id desc limit 1`, siteID).Scan(&auditedCurrency); err != nil {
		t.Fatal(err)
	}
	if auditedCurrency != currency {
		t.Fatalf("audited currency=%q, want %q", auditedCurrency, currency)
	}
}

func TestUpdateRevenueCurrencyRequiresOwnerOrAdmin(t *testing.T) {
	service, registry, principal, pool := newSites(t)
	ctx := context.Background()
	var siteID int32
	if err := pool.QueryRow(ctx, `insert into sites(org_id,public_id,name,allowed_hosts,timezone,retention_class) values($1,'legacycur002','Legacy',array['example.test'],'UTC',30) returning id`, principal.OrgID).Scan(&siteID); err != nil {
		t.Fatal(err)
	}
	if err := registry.Invalidate(); err != nil {
		t.Fatal(err)
	}
	currency := "USD"
	editor := principal
	editor.Role = authz.RoleEditor
	if _, err := service.Update(ctx, editor, siteID, SitePatch{RevenueCurrency: &currency}); !errors.Is(err, ErrForbidden) {
		t.Fatalf("editor selection error=%v, want forbidden", err)
	}
}

func TestUpdateRevenueCurrencyResolvesLegacyConflict(t *testing.T) {
	service, registry, principal, pool := newSites(t)
	ctx := context.Background()
	var siteID int32
	if err := pool.QueryRow(ctx, `insert into sites(org_id,public_id,name,allowed_hosts,timezone,retention_class,revenue_currency_state) values($1,'legacycur004','Legacy conflict',array['example.test'],'UTC',30,'conflict') returning id`, principal.OrgID).Scan(&siteID); err != nil {
		t.Fatal(err)
	}
	if err := registry.Invalidate(); err != nil {
		t.Fatal(err)
	}
	currency := "USD"
	updated, err := service.Update(ctx, principal, siteID, SitePatch{RevenueCurrency: &currency})
	if err != nil {
		t.Fatalf("resolve conflict: %v", err)
	}
	if updated.RevenueCurrency != "USD" || updated.RevenueCurrencyState != sites.RevenueCurrencyConfigured {
		t.Fatalf("updated currency=%q/%q, want USD/configured", updated.RevenueCurrency, updated.RevenueCurrencyState)
	}
}

func TestStaleRegistryCannotSelectContradictoryCurrency(t *testing.T) {
	service, registry, principal, pool := newSites(t)
	ctx := context.Background()
	data := &store.Store{Pool: pool}
	staleRegistry := NewRegistry(data, 0)
	defer staleRegistry.Close()
	staleService := New(data, staleRegistry)
	var siteID int32
	if err := pool.QueryRow(ctx, `insert into sites(org_id,public_id,name,allowed_hosts,timezone,retention_class) values($1,'legacycur003','Legacy',array['example.test'],'UTC',30) returning id`, principal.OrgID).Scan(&siteID); err != nil {
		t.Fatal(err)
	}
	if err := registry.Invalidate(); err != nil {
		t.Fatal(err)
	}
	if err := staleRegistry.Invalidate(); err != nil {
		t.Fatal(err)
	}
	usd := "USD"
	if _, err := service.Update(ctx, principal, siteID, SitePatch{RevenueCurrency: &usd}); err != nil {
		t.Fatal(err)
	}
	stale, ok := staleRegistry.Site(siteID)
	if !ok || stale.RevenueCurrencyState != sites.RevenueCurrencyUnconfigured {
		t.Fatalf("second registry was not stale: (%+v,%t)", stale, ok)
	}
	eur := "EUR"
	if _, err := staleService.Update(ctx, principal, siteID, SitePatch{RevenueCurrency: &eur}); !errors.Is(err, ErrInvalid) {
		t.Fatalf("contradictory selection error=%v, want invalid", err)
	}
	var stored string
	if err := pool.QueryRow(ctx, `select revenue_currency from sites where id=$1`, siteID).Scan(&stored); err != nil {
		t.Fatal(err)
	}
	if stored != "USD" {
		t.Fatalf("stored currency=%q, want USD", stored)
	}
}

func TestRegistryLoadsProfilesEnabledFromSettings(t *testing.T) {
	service, registry, principal, pool := newSites(t)
	ctx := context.Background()
	site, err := service.Create(ctx, principal, "Profiles", []string{"profiles.example.com"}, "UTC", 30, "USD")
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

func TestRegistryDefaultsPrivacySignalsOnAndLoadsOverride(t *testing.T) {
	service, registry, principal, pool := newSites(t)
	ctx := context.Background()
	site, err := service.Create(ctx, principal, "Privacy", []string{"privacy.example.com"}, "UTC", 30, "USD")
	if err != nil {
		t.Fatal(err)
	}
	if !site.RespectPrivacySignals {
		t.Fatal("privacy signals must default on")
	}
	if _, err := pool.Exec(ctx, `update sites set settings = settings || '{"respect_privacy_signals": false}'::jsonb where id = $1`, site.ID); err != nil {
		t.Fatal(err)
	}
	if err := registry.Invalidate(); err != nil {
		t.Fatal(err)
	}
	loaded, ok := registry.Site(site.ID)
	if !ok || loaded.RespectPrivacySignals {
		t.Fatalf("registry site = (%+v, %t), want privacy signals disabled", loaded, ok)
	}
}

func TestRefreshIconDiscoversFirstHostAndPersistsSettings(t *testing.T) {
	service, registry, principal, pool := newSites(t)
	ctx := context.Background()
	site, err := service.Create(ctx, principal, "Icons", []string{"first.example.com", "second.example.com"}, "UTC", 30, "USD")
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
	site, err := service.Create(ctx, principal, "Icons", []string{"example.com"}, "UTC", 30, "USD")
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
	site, err := service.Create(ctx, principal, "Profiles", []string{"profiles.example.com"}, "UTC", 30, "USD")
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
	a, err := service.Create(ctx, principal, "A", []string{"a.io"}, "UTC", 30, "USD")
	if err != nil {
		t.Fatal(err)
	}
	if _, err := service.Create(ctx, principal, "B", []string{"b.io"}, "UTC", 30, "USD"); err != nil {
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
			if _, err := service.Create(ctx, principal, "Invalid", test.hosts, test.timezone, test.retention, "USD"); !errors.Is(err, ErrInvalid) {
				t.Fatalf("Create error = %v, want ErrInvalid", err)
			}
		})
	}
}

func TestCreateRevalidatesAPIKeyAndOrganization(t *testing.T) {
	service, _, owner, pool := newSites(t)
	ctx := context.Background()
	var keyID int64
	if err := pool.QueryRow(ctx, `
		insert into api_keys(org_id,name,prefix,hash,scopes,site_ids,created_by)
		values ($1,'Provisioner','create01',decode(repeat('05',32),'hex'),array['sites:write'],null,$2)
		returning id`, owner.OrgID, owner.UserID).Scan(&keyID); err != nil {
		t.Fatal(err)
	}
	key := authz.Principal{KeyID: keyID, OrgID: owner.OrgID, Role: authz.RoleKey, Scopes: []string{"sites:write"}}
	if _, err := service.Create(ctx, key, "Key site", []string{"key-create.example.com"}, "UTC", 30, "USD"); err != nil {
		t.Fatalf("active unrestricted key Create error = %v", err)
	}

	if _, err := pool.Exec(ctx, `update api_keys set revoked_at=now() where id=$1`, keyID); err != nil {
		t.Fatal(err)
	}
	if _, err := service.Create(ctx, key, "Revoked", []string{"revoked-create.example.com"}, "UTC", 30, "USD"); !errors.Is(err, ErrForbidden) {
		t.Fatalf("revoked key Create error = %v, want ErrForbidden", err)
	}

	if _, err := pool.Exec(ctx, `update orgs set deleted_at=now() where id=$1`, owner.OrgID); err != nil {
		t.Fatal(err)
	}
	if _, err := service.Create(ctx, owner, "Deleted org", []string{"deleted-org.example.com"}, "UTC", 30, "USD"); !errors.Is(err, ErrForbidden) {
		t.Fatalf("deleted organization Create error = %v, want ErrForbidden", err)
	}
}

func TestRegistryExcludesSitesOwnedByDeletedOrganizations(t *testing.T) {
	service, registry, owner, pool := newSites(t)
	ctx := context.Background()
	site, err := service.Create(ctx, owner, "Deleted org site", []string{"deleted-org.example.com"}, "UTC", 30, "USD")
	if err != nil {
		t.Fatal(err)
	}
	if _, ok := registry.Site(site.ID); !ok {
		t.Fatal("registry did not contain active organization's site")
	}
	if _, err := pool.Exec(ctx, `update orgs set deleted_at=now() where id=$1`, owner.OrgID); err != nil {
		t.Fatal(err)
	}
	if err := registry.Invalidate(); err != nil {
		t.Fatal(err)
	}
	if _, ok := registry.Site(site.ID); ok {
		t.Fatal("registry contained site owned by deleted organization")
	}
}

func TestUpdateRefreshesRegistryAndWritesAudit(t *testing.T) {
	service, registry, principal, pool := newSites(t)
	ctx := context.Background()
	site, err := service.Create(ctx, principal, "Blog", []string{"blog.example.com"}, "UTC", 30, "USD")
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
	site, err := service.Create(ctx, principal, "Blog", []string{"blog.example.com"}, "UTC", 30, "USD")
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
	site, err := service.Create(ctx, principal, "Blog", []string{"blog.example.com"}, "UTC", 30, "USD")
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
	site, err := service.Create(ctx, principal, "Blog", []string{"blog.example.com"}, "UTC", 30, "USD")
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
	site, err := service.Create(ctx, principal, "Blog", []string{"blog.example.com"}, "UTC", 30, "USD")
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

func TestUpdatePrivacySignalsPersistsAndRefreshesRegistry(t *testing.T) {
	service, registry, principal, pool := newSites(t)
	ctx := context.Background()
	site, err := service.Create(ctx, principal, "Privacy", []string{"privacy.example.com"}, "UTC", 30, "USD")
	if err != nil {
		t.Fatal(err)
	}
	disabled := false
	updated, err := service.Update(ctx, principal, site.ID, SitePatch{RespectPrivacySignals: &disabled})
	if err != nil {
		t.Fatal(err)
	}
	loaded, ok := registry.Site(site.ID)
	if !ok || updated.RespectPrivacySignals || loaded.RespectPrivacySignals {
		t.Fatalf("privacy setting not refreshed: updated=%+v loaded=%+v found=%t", updated, loaded, ok)
	}
	var stored bool
	if err := pool.QueryRow(ctx, `select (settings->>'respect_privacy_signals')::boolean from sites where id=$1`, site.ID).Scan(&stored); err != nil {
		t.Fatal(err)
	}
	if stored {
		t.Fatal("respect_privacy_signals was not stored as false")
	}
}

func TestUpdateProtectionSettingsPersistsAndRefreshesRegistry(t *testing.T) {
	service, registry, principal, pool := newSites(t)
	ctx := context.Background()
	site, err := service.Create(ctx, principal, "Protected", []string{"protected.example.com"}, "UTC", 30, "USD")
	if err != nil {
		t.Fatal(err)
	}
	if site.StrictOrigin || len(site.BlockedReferrers) != 0 {
		t.Fatalf("protection settings did not default off: %+v", site)
	}
	strict := true
	blocked := []string{" EVIL.COM ", "evil.com", "spam.example"}
	updated, err := service.Update(ctx, principal, site.ID, SitePatch{StrictOrigin: &strict, BlockedReferrers: &blocked})
	if err != nil {
		t.Fatal(err)
	}
	loaded, ok := registry.Site(site.ID)
	if !ok || !updated.StrictOrigin || !loaded.StrictOrigin || strings.Join(loaded.BlockedReferrers, ",") != "evil.com,spam.example" {
		t.Fatalf("protection settings not refreshed: updated=%+v loaded=%+v found=%t", updated, loaded, ok)
	}
	var storedStrict bool
	var storedBlocked []string
	if err := pool.QueryRow(ctx, `select coalesce((settings->>'strict_origin')::boolean, false), coalesce(settings->'blocked_referrers', '[]'::jsonb) from sites where id=$1`, site.ID).Scan(&storedStrict, &storedBlocked); err != nil {
		t.Fatal(err)
	}
	if !storedStrict || strings.Join(storedBlocked, ",") != "evil.com,spam.example" {
		t.Fatalf("stored protection settings = %t/%v", storedStrict, storedBlocked)
	}

	invalid := []string{"https://evil.com"}
	if _, err := service.Update(ctx, principal, site.ID, SitePatch{BlockedReferrers: &invalid}); !errors.Is(err, ErrInvalid) {
		t.Fatalf("invalid blocked referrer error = %v, want ErrInvalid", err)
	}
}

func TestUpdateHeatmapsRejectsEditor(t *testing.T) {
	service, _, principal, _ := newSites(t)
	site, err := service.Create(context.Background(), principal, "Blog", []string{"blog.example.com"}, "UTC", 30, "USD")
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
	site, err := service.Create(context.Background(), principal, "Blog", []string{"blog.example.com"}, "UTC", 30, "USD")
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
	site, err := service.Create(ctx, principal, "Blog", []string{"blog.example.com"}, "UTC", 30, "USD")
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
	site, err := service.Create(ctx, principal, "Blog", []string{"blog.example.com"}, "UTC", 30, "USD")
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

func TestDeleteRevalidatesCurrentMembership(t *testing.T) {
	service, registry, principal, pool := newSites(t)
	ctx := context.Background()
	site, err := service.Create(ctx, principal, "Lifecycle", []string{"lifecycle.example.com"}, "UTC", 30, "USD")
	if err != nil {
		t.Fatal(err)
	}

	if _, err := pool.Exec(ctx, `update memberships set role='viewer',site_ids=array[$3::integer] where org_id=$1 and user_id=$2`, principal.OrgID, principal.UserID, site.ID); err != nil {
		t.Fatal(err)
	}
	if err := service.Delete(ctx, principal, site.ID); !errors.Is(err, ErrForbidden) {
		t.Fatalf("stale owner Delete error = %v, want ErrForbidden", err)
	}
	if _, ok := registry.Site(site.ID); !ok {
		t.Fatal("denied stale-principal delete removed site from registry")
	}

	if _, err := pool.Exec(ctx, `delete from memberships where org_id=$1 and user_id=$2`, principal.OrgID, principal.UserID); err != nil {
		t.Fatal(err)
	}
	if err := service.Delete(ctx, principal, site.ID); !errors.Is(err, ErrForbidden) {
		t.Fatalf("removed owner Delete error = %v, want ErrForbidden", err)
	}
	var deleted bool
	if err := pool.QueryRow(ctx, `select deleted_at is not null from sites where id=$1`, site.ID).Scan(&deleted); err != nil {
		t.Fatal(err)
	}
	if deleted {
		t.Fatal("denied stale-principal delete soft-deleted site")
	}
}

func TestDeleteKeepsCrossTenantSiteHidden(t *testing.T) {
	service, registry, principal, pool := newSites(t)
	ctx := context.Background()
	site, err := service.Create(ctx, principal, "Tenant A", []string{"tenant-a.example.com"}, "UTC", 30, "USD")
	if err != nil {
		t.Fatal(err)
	}
	otherOrg := insertOrg(t, pool, "Tenant B", "tenant-b")
	if _, err := pool.Exec(ctx, `insert into memberships(org_id,user_id,role) values ($1,$2,'owner')`, otherOrg, principal.UserID); err != nil {
		t.Fatal(err)
	}
	otherPrincipal := authz.Principal{UserID: principal.UserID, OrgID: otherOrg, Role: authz.RoleOwner}
	if err := service.Delete(ctx, otherPrincipal, site.ID); !errors.Is(err, ErrNotFound) {
		t.Fatalf("cross-tenant Delete error = %v, want ErrNotFound", err)
	}
	if _, ok := registry.Site(site.ID); !ok {
		t.Fatal("cross-tenant delete removed site from registry")
	}
}

func TestDeleteRevalidatesAPIKey(t *testing.T) {
	service, registry, owner, pool := newSites(t)
	ctx := context.Background()
	trackedSites := make([]sites.Site, 4)
	for index, host := range []string{"key-active.example.com", "key-revoked.example.com", "key-expired.example.com", "key-restricted.example.com"} {
		created, err := service.Create(ctx, owner, host, []string{host}, "UTC", 30, "USD")
		if err != nil {
			t.Fatal(err)
		}
		trackedSites[index] = created
	}
	insertKey := func(name, prefix, hashByte string, siteIDs []int32, revoked, expired bool) int64 {
		t.Helper()
		var id int64
		err := pool.QueryRow(ctx, `
			insert into api_keys(org_id,name,prefix,hash,scopes,site_ids,created_by,revoked_at,expires_at)
			values ($1,$2,$3,decode(repeat($4,32),'hex'),array['sites:write'],$5,$6,
				case when $7 then now() end,case when $8 then now() - interval '1 minute' end)
			returning id`, owner.OrgID, name, prefix, hashByte, siteIDs, owner.UserID, revoked, expired).Scan(&id)
		if err != nil {
			t.Fatal(err)
		}
		return id
	}

	activeID := insertKey("Active", "active01", "01", nil, false, false)
	active := authz.Principal{KeyID: activeID, OrgID: owner.OrgID, Role: authz.RoleKey, Scopes: []string{"sites:write"}}
	if err := service.Delete(ctx, active, trackedSites[0].ID); err != nil {
		t.Fatalf("active API key Delete error = %v", err)
	}
	if _, ok := registry.Site(trackedSites[0].ID); ok {
		t.Fatal("active API key did not delete authorized site")
	}

	for _, test := range []struct {
		name  string
		keyID int64
		site  sites.Site
	}{
		{
			name:  "revoked",
			keyID: insertKey("Revoked", "revoked1", "02", nil, true, false),
			site:  trackedSites[1],
		},
		{
			name:  "expired",
			keyID: insertKey("Expired", "expired1", "03", nil, false, true),
			site:  trackedSites[2],
		},
		{
			name:  "new restriction",
			keyID: insertKey("Restricted", "restrict", "04", []int32{trackedSites[0].ID}, false, false),
			site:  trackedSites[3],
		},
	} {
		t.Run(test.name, func(t *testing.T) {
			stale := authz.Principal{KeyID: test.keyID, OrgID: owner.OrgID, Role: authz.RoleKey, Scopes: []string{"sites:write"}}
			if err := service.Delete(ctx, stale, test.site.ID); !errors.Is(err, ErrForbidden) {
				t.Fatalf("stale API key Delete error = %v, want ErrForbidden", err)
			}
			if _, ok := registry.Site(test.site.ID); !ok {
				t.Fatal("denied API key delete removed site from registry")
			}
		})
	}
}

func TestDeleteAuditFailureRollsBackSoftDelete(t *testing.T) {
	service, registry, principal, pool := newSites(t)
	ctx := context.Background()
	site, err := service.Create(ctx, principal, "Audited", []string{"audited.example.com"}, "UTC", 30, "USD")
	if err != nil {
		t.Fatal(err)
	}
	if _, err := pool.Exec(ctx, `
		create function reject_site_delete_audit() returns trigger language plpgsql as $$
		begin
			if new.action = 'site.deleted' then raise exception 'audit unavailable'; end if;
			return new;
		end $$;
		create trigger reject_site_delete_audit before insert on audit_log
		for each row execute function reject_site_delete_audit()`); err != nil {
		t.Fatal(err)
	}

	if err := service.Delete(ctx, principal, site.ID); err == nil {
		t.Fatal("Delete succeeded despite audit failure")
	}
	var deleted bool
	if err := pool.QueryRow(ctx, `select deleted_at is not null from sites where id=$1`, site.ID).Scan(&deleted); err != nil {
		t.Fatal(err)
	}
	if deleted {
		t.Fatal("failed delete audit did not roll back soft delete")
	}
	if _, ok := registry.Site(site.ID); !ok {
		t.Fatal("failed delete audit removed site from registry")
	}
}

func TestRestoreAuthorizesAuditsAndRefreshesRegistry(t *testing.T) {
	service, registry, owner, pool := newSites(t)
	ctx := context.Background()
	now := time.Date(2026, time.October, 11, 12, 0, 0, 0, time.UTC)
	service.SetClock(func() time.Time { return now })
	site, err := service.Create(ctx, owner, "Restore", []string{"restore.example.com"}, "UTC", 30, "USD")
	if err != nil {
		t.Fatal(err)
	}
	if err := service.Delete(ctx, owner, site.ID); err != nil {
		t.Fatal(err)
	}
	var deletedAt time.Time
	if err := pool.QueryRow(ctx, `select deleted_at from sites where id=$1`, site.ID).Scan(&deletedAt); err != nil {
		t.Fatal(err)
	}
	if !deletedAt.Equal(now) {
		t.Fatalf("deleted_at = %s, want injected time %s", deletedAt, now)
	}
	if _, ok := registry.Site(site.ID); ok {
		t.Fatal("deleted site remained in registry")
	}
	viewer := owner
	viewer.Role = authz.RoleViewer
	if err := service.Restore(ctx, viewer, site.ID); !errors.Is(err, ErrForbidden) {
		t.Fatalf("viewer Restore() error = %v, want ErrForbidden", err)
	}
	if _, err := pool.Exec(ctx, `update memberships set role='admin' where org_id=$1 and user_id=$2`, owner.OrgID, owner.UserID); err != nil {
		t.Fatal(err)
	}
	admin := owner
	admin.Role = authz.RoleAdmin
	if err := service.Restore(ctx, admin, site.ID); err != nil {
		t.Fatal(err)
	}
	if _, ok := registry.Site(site.ID); !ok {
		t.Fatal("restored site missing from registry")
	}
	var actions string
	if err := pool.QueryRow(ctx, `select string_agg(action, ',' order by id) from audit_log where target_type='site' and target_id=$1`, site.ID).Scan(&actions); err != nil {
		t.Fatal(err)
	}
	if actions != "site.created,site.deleted,site.restored" {
		t.Fatalf("audit actions = %q", actions)
	}
}

func TestRestoreRevalidatesCurrentMembership(t *testing.T) {
	service, _, principal, pool := newSites(t)
	ctx := context.Background()
	now := time.Now().UTC()
	service.SetClock(func() time.Time { return now })
	site, err := service.Create(ctx, principal, "Restore lock", []string{"restore-lock.example.com"}, "UTC", 30, "USD")
	if err != nil {
		t.Fatal(err)
	}
	if err := service.Delete(ctx, principal, site.ID); err != nil {
		t.Fatal(err)
	}
	if _, err := pool.Exec(ctx, `update memberships set role='viewer',site_ids=array[$3::integer] where org_id=$1 and user_id=$2`, principal.OrgID, principal.UserID, site.ID); err != nil {
		t.Fatal(err)
	}
	if err := service.Restore(ctx, principal, site.ID); !errors.Is(err, ErrForbidden) {
		t.Fatalf("stale owner Restore() error = %v, want ErrForbidden", err)
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
	return New(data, registry, privacy.Identified), registry, authz.Principal{UserID: userID, OrgID: orgID, Role: authz.RoleOwner}, pool
}

func insertOrg(t *testing.T, pool *pgxpool.Pool, name, slug string) int64 {
	t.Helper()
	var id int64
	if err := pool.QueryRow(context.Background(), `insert into orgs(name, slug) values ($1, $2) returning id`, name, slug).Scan(&id); err != nil {
		t.Fatal(err)
	}
	return id
}

func TestAnonymousServiceRejectsProfileEscalationBeforeStorage(t *testing.T) {
	enabled := true
	service := New(nil, nil)
	_, err := service.Update(context.Background(), authz.Principal{UserID: 1, OrgID: 1, Role: authz.RoleOwner}, 1, SitePatch{ProfilesEnabled: &enabled})
	if !errors.Is(err, ErrInvalid) {
		t.Fatalf("error=%v", err)
	}
}

func TestSiteLimitAppliesToEveryoneExceptInstanceAdmins(t *testing.T) {
	service, _, principal, pool := newSites(t)
	ctx := context.Background()
	service.SetSiteLimit(2)
	for i, host := range []string{"one.example.test", "two.example.test"} {
		if _, err := service.Create(ctx, principal, "Site", []string{host}, "UTC", 30, "USD"); err != nil {
			t.Fatalf("site %d: %v", i+1, err)
		}
	}
	if _, err := service.Create(ctx, principal, "Third", []string{"three.example.test"}, "UTC", 30, "USD"); !errors.Is(err, ErrLimit) {
		t.Fatalf("third site error=%v, want ErrLimit", err)
	}
	// An API key has no user, so it is capped too.
	keyPrincipal := principal
	keyPrincipal.UserID = 0
	if _, err := service.Create(ctx, keyPrincipal, "Key", []string{"key.example.test"}, "UTC", 30, "USD"); err == nil {
		t.Fatal("keyless principal bypassed the site limit")
	}
	if _, err := pool.Exec(ctx, `update users set is_admin=true where id=$1`, principal.UserID); err != nil {
		t.Fatal(err)
	}
	if _, err := service.Create(ctx, principal, "Third", []string{"three.example.test"}, "UTC", 30, "USD"); err != nil {
		t.Fatalf("instance admin was limited: %v", err)
	}
}
