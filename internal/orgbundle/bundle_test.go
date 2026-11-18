package orgbundle

import (
	"context"
	"encoding/json"
	"strings"
	"testing"
	"time"

	"github.com/jackc/pgx/v5/pgxpool"

	"supermetry/internal/db"
	"supermetry/internal/platform/authz"
	"supermetry/internal/platform/invites"
	"supermetry/internal/platform/sitesvc"
	"supermetry/internal/platform/store"
	"supermetry/internal/privacy"
	"supermetry/internal/testpg"
)

type fakeInviter struct{ calls []string }

func (f *fakeInviter) Create(_ context.Context, _ authz.Principal, email string, role authz.Role, siteIDs []int32, _ ...invites.Access) (string, error) {
	f.calls = append(f.calls, email+"/"+string(role)+"/"+strings.Join(idsText(siteIDs), ","))
	return "link", nil
}

func idsText(ids []int32) []string {
	var out []string
	for _, id := range ids {
		out = append(out, itoa(int64(id)))
	}
	return out
}

func itoa(v int64) string { b, _ := json.Marshal(v); return string(b) }

type rig struct {
	pool  *pgxpool.Pool
	svc   *Service
	inv   *fakeInviter
	ctx   context.Context
	owner authz.Principal // organization 1
	other authz.Principal // organization 2
}

func newRig(t *testing.T) *rig {
	t.Helper()
	pool, _ := testpg.New(t)
	ctx := context.Background()
	if err := db.Migrate(ctx, pool); err != nil {
		t.Fatal(err)
	}
	if _, err := pool.Exec(ctx, `
		insert into orgs(id,name,slug) overriding system value values (1,'Source','source'),(2,'Target','target');
		insert into users(id,email,name,password_hash) overriding system value values
		 (11,'owner@example.test','Owner','hash'),(12,'editor@example.test','Editor','hash'),(13,'viewer@example.test','Viewer','hash'),(14,'o2@example.test','Owner Two','hash');
		insert into memberships(org_id,user_id,role,site_ids) values (1,11,'owner',null),(1,12,'editor','{7}'),(1,13,'viewer',null),(2,14,'owner',null);
		insert into sites(id,org_id,public_id,name,timezone,retention_class,allowed_hosts,settings,slug) overriding system value values
		 (7,1,'bundlesite01','Docs','Europe/Berlin',90,'{docs.example.test}',
		  '{"profiles_enabled":true,"exclude_paths":["/admin"],"smtp":{"pass":"smtp-secret"},"icon_url":"https://x.test/i.png","replay":{"enabled":true,"sample_rate":50}}','docs'),
		 (8,1,'bundlesite02','Shop','UTC',30,'{shop.example.test}','{}','shop');
		insert into goals(site_id,kind,match,value,name) values (7,'page','exact','/thanks','Thanks'),(7,'event','exact','signup','Signup');
		insert into funnels(site_id,name,steps) values (7,'Checkout','[{"type":"page","value":"/cart"},{"type":"page","value":"/thanks"}]');
		insert into segments(site_id,name,filters) values (7,'Mobile','[{"field":"device","op":"is","value":"mobile"}]');
		insert into notification_channels(id,org_id,kind,name,config) overriding system value values
		 (1,1,'webhook','Ops hook','{"url":"https://hooks.example.test/SECRET-PATH","secret":"whsec_123"}'),
		 (2,1,'email','Team mail','{"to":"team@example.test"}');
		insert into notification_channels(id,org_id,kind,name,config) overriding system value values (3,2,'email','Team mail','{"to":"team@example.test"}');
		insert into alerts(site_id,kind,threshold,goal_id,channel_ids,enabled,name) values
		 (7,'goal_reached',5,(select id from goals where name='Thanks'),'{1,2}',true,'Thanks reached'),(7,'traffic_drop',50,null,'{1}',true,'Drop'),(7,'traffic_spike',200,null,'{2}',true,'Spike');
		insert into shared_links(site_id,slug_hash,password_hash,created_by,config) values (7,decode(repeat('ab',32),'hex'),'$argon2id$secrethash',11,'{"show_pages":true}')`); err != nil {
		t.Fatal(err)
	}
	data := &store.Store{Pool: pool}
	registry := sitesvc.NewRegistry(data, time.Hour)
	t.Cleanup(registry.Close)
	sites := sitesvc.New(data, registry, privacy.Identified)
	inv := &fakeInviter{}
	svc := New(pool, sites, inv, func() time.Time { return time.Date(2026, 11, 18, 10, 0, 0, 0, time.UTC) })
	svc.Refresh = registry.Invalidate
	return &rig{pool: pool, svc: svc, inv: inv, ctx: ctx,
		owner: authz.Principal{UserID: 11, OrgID: 1, Role: authz.RoleOwner}, other: authz.Principal{UserID: 14, OrgID: 2, Role: authz.RoleOwner}}
}

func TestExportContainsNoSecrets(t *testing.T) {
	r := newRig(t)
	b, err := r.svc.Export(r.ctx, r.owner)
	if err != nil {
		t.Fatal(err)
	}
	raw, _ := json.Marshal(b)
	for _, secret := range []string{"SECRET-PATH", "whsec_123", "smtp-secret", "argon2", "abababab", "x.test/i.png", "team@example.test", "hash"} {
		if strings.Contains(string(raw), secret) {
			t.Errorf("bundle contains %q", secret)
		}
	}
	if len(b.Sites) != 2 || len(b.Channels) != 2 || len(b.Members) != 3 {
		t.Fatalf("sites=%d channels=%d members=%d", len(b.Sites), len(b.Channels), len(b.Members))
	}
	docs := b.Sites[0]
	if docs.Slug != "docs" || docs.RetentionDays != 90 || len(docs.Goals) != 2 || len(docs.Funnels) != 1 || len(docs.Segments) != 1 || len(docs.Alerts) != 3 || len(docs.ShareLinks) != 1 {
		t.Fatalf("docs site = %+v", docs)
	}
	if !docs.ShareLinks[0].HasPassword || docs.Alerts[0].GoalRef != "g1" || len(docs.Alerts[0].Channels) != 2 {
		t.Fatalf("share/alert refs = %+v %+v", docs.ShareLinks[0], docs.Alerts[0])
	}
	var audits int
	_ = r.pool.QueryRow(r.ctx, `select count(*) from audit_log where org_id=1 and action='org.settings_exported'`).Scan(&audits)
	if audits != 1 {
		t.Fatalf("export audit rows = %d", audits)
	}
	if b.Members[1].Email != "editor@example.test" || len(b.Members[1].SiteSlugs) != 1 || b.Members[1].SiteSlugs[0] != "docs" {
		t.Fatalf("members = %+v", b.Members)
	}
}

func TestRoundTripIntoNewOrganization(t *testing.T) {
	r := newRig(t)
	b, err := r.svc.Export(r.ctx, r.owner)
	if err != nil {
		t.Fatal(err)
	}
	// Through JSON, as the endpoints do.
	raw, _ := json.Marshal(b)
	var in Bundle
	if err := json.Unmarshal(raw, &in); err != nil {
		t.Fatal(err)
	}

	dry, err := r.svc.Import(r.ctx, r.other, in, ImportOptions{DryRun: true, InviteMembers: true})
	if err != nil {
		t.Fatal(err)
	}
	if !dry.DryRun || len(dry.Sites) != 2 || dry.Sites[0].Action != "create" || dry.Sites[0].Goals != 2 || dry.Sites[0].Alerts != 3 {
		t.Fatalf("dry run = %+v", dry)
	}
	var n int
	_ = r.pool.QueryRow(r.ctx, `select count(*) from sites where org_id=2`).Scan(&n)
	if n != 0 || len(r.inv.calls) != 0 {
		t.Fatalf("a dry run wrote data: sites=%d invites=%v", n, r.inv.calls)
	}

	rep, err := r.svc.Import(r.ctx, r.other, in, ImportOptions{})
	if err != nil {
		t.Fatal(err)
	}
	for _, s := range rep.Sites {
		if s.Action != "created" {
			t.Fatalf("site result %+v (report %+v)", s, rep)
		}
	}
	if len(r.inv.calls) != 0 {
		t.Fatalf("members were invited without InviteMembers: %v", r.inv.calls)
	}
	for _, m := range rep.Members {
		if m.Action != "listed_only" {
			t.Fatalf("member %+v", m)
		}
	}
	var siteID int32
	var tz, hosts, settings string
	var retention int
	if err := r.pool.QueryRow(r.ctx, `select id,timezone,array_to_string(allowed_hosts,','),retention_class,settings::text from sites where org_id=2 and name='Docs'`).
		Scan(&siteID, &tz, &hosts, &retention, &settings); err != nil {
		t.Fatal(err)
	}
	if tz != "Europe/Berlin" || hosts != "docs.example.test" || retention != 90 {
		t.Fatalf("site = %s %s %d", tz, hosts, retention)
	}
	if !strings.Contains(settings, "/admin") || strings.Contains(settings, "smtp") {
		t.Fatalf("settings = %s", settings)
	}
	var goals, funnels, segments, alerts, disabled, linked int
	_ = r.pool.QueryRow(r.ctx, `select (select count(*) from goals where site_id=$1),(select count(*) from funnels where site_id=$1),(select count(*) from segments where site_id=$1),
		(select count(*) from alerts where site_id=$1),(select count(*) from alerts where site_id=$1 and not enabled),
		(select count(*) from alerts a join goals g on g.id=a.goal_id where a.site_id=$1 and g.site_id=$1)`, siteID).Scan(&goals, &funnels, &segments, &alerts, &disabled, &linked)
	if goals != 2 || funnels != 1 || segments != 1 || alerts != 3 || linked != 1 {
		t.Fatalf("goals=%d funnels=%d segments=%d alerts=%d goal-linked=%d", goals, funnels, segments, alerts, linked)
	}
	// Org 2 has an email channel "Team mail" but no webhook "Ops hook": the
	// drop alert only used the webhook and arrives disabled.
	if disabled != 1 {
		t.Fatalf("disabled alerts = %d, want 1 (no matching channel)", disabled)
	}
	var links int
	_ = r.pool.QueryRow(r.ctx, `select count(*) from shared_links where site_id=$1`, siteID).Scan(&links)
	if links != 0 {
		t.Fatal("an import must not create share links")
	}

	again, err := r.svc.Import(r.ctx, r.other, in, ImportOptions{})
	if err != nil {
		t.Fatal(err)
	}
	for _, s := range again.Sites {
		if s.Action != "skip_exists" {
			t.Fatalf("second import site %+v", s)
		}
	}
	_ = r.pool.QueryRow(r.ctx, `select count(*) from goals where site_id=$1`, siteID).Scan(&goals)
	if goals != 2 {
		t.Fatalf("second import duplicated goals: %d", goals)
	}
}

func TestInviteMembersOnRequest(t *testing.T) {
	r := newRig(t)
	b, _ := r.svc.Export(r.ctx, r.owner)
	if _, err := r.svc.Import(r.ctx, r.other, b, ImportOptions{InviteMembers: true}); err != nil {
		t.Fatal(err)
	}
	want := []string{"editor@example.test/editor/", "viewer@example.test/viewer/"}
	if len(r.inv.calls) != 2 {
		t.Fatalf("invites = %v (the owner is never invited)", r.inv.calls)
	}
	var docs int32
	_ = r.pool.QueryRow(r.ctx, `select id from sites where org_id=2 and name='Docs'`).Scan(&docs)
	want[0] += itoa(int64(docs))
	if r.inv.calls[0] != want[0] || r.inv.calls[1] != want[1] {
		t.Fatalf("invites = %v, want %v", r.inv.calls, want)
	}
}

func TestImportRejectsBadBundles(t *testing.T) {
	r := newRig(t)
	good, _ := r.svc.Export(r.ctx, r.owner)
	mutations := map[string]func(*Bundle){
		"wrong kind":     func(b *Bundle) { b.Kind = "other" },
		"future version": func(b *Bundle) { b.SchemaVersion = 99 },
		"bad goal kind":  func(b *Bundle) { b.Sites[0].Goals[0].Kind = "drop table" },
		"bad alert ref":  func(b *Bundle) { b.Sites[0].Alerts[0].GoalRef = "g99" },
		"bad role":       func(b *Bundle) { b.Members[0].Role = "root" },
		"duplicate slug": func(b *Bundle) { b.Sites[1].Slug = b.Sites[0].Slug },
	}
	for name, mutate := range mutations {
		raw, _ := json.Marshal(good)
		var b Bundle
		_ = json.Unmarshal(raw, &b)
		mutate(&b)
		if _, err := r.svc.Import(r.ctx, r.other, b, ImportOptions{DryRun: true}); err == nil || !strings.Contains(err.Error(), "invalid bundle") {
			t.Errorf("%s: err = %v", name, err)
		}
	}
}

func TestImportDoesNotSmuggleSettings(t *testing.T) {
	r := newRig(t)
	b, _ := r.svc.Export(r.ctx, r.owner)
	b.Sites[1].Settings = json.RawMessage(`{"smtp":{"host":"evil"},"replay_storage":{"x":1},"exclude_paths":["/ok"],"tracker":{"api_token":"t","mode":"x"},"profiles_enabled":"yes"}`)
	if _, err := r.svc.Import(r.ctx, r.other, b, ImportOptions{}); err != nil {
		t.Fatal(err)
	}
	var settings string
	_ = r.pool.QueryRow(r.ctx, `select settings::text from sites where org_id=2 and name='Shop'`).Scan(&settings)
	if strings.Contains(settings, "smtp") || strings.Contains(settings, "replay_storage") || strings.Contains(settings, "api_token") || strings.Contains(settings, `"yes"`) || !strings.Contains(settings, "/ok") {
		t.Fatalf("settings = %s", settings)
	}
}

// Alert destinations (webhook URLs, routing keys, tokens) live in channels,
// which are exported by kind and name only; alert config is reduced to its
// typed fields on export and again on import.
func TestAlertConfigAndDestinationsAreScrubbed(t *testing.T) {
	r := newRig(t)
	if _, err := r.pool.Exec(r.ctx, `
		insert into notification_channels(id,org_id,kind,name,config) overriding system value values
		 (101,1,'slack','S','{"url":"https://hooks.slack.test/T0/B0/SLACKTOKEN"}'),
		 (102,1,'discord','D','{"url":"https://discord.test/api/webhooks/1/DISCORDTOKEN"}'),
		 (103,1,'telegram','T','{"bot_token":"TGTOKEN","chat_id":"1"}'),
		 (104,1,'pagerduty','P','{"routing_key":"PDROUTINGKEY"}');
		update alerts set config='{"kinds":["crash"],"webhook_url":"https://leak.test/TAMPER","routing_key":"CFGKEY"}' where name='Spike'`); err != nil {
		t.Fatal(err)
	}
	b, err := r.svc.Export(r.ctx, r.owner)
	if err != nil {
		t.Fatal(err)
	}
	raw, _ := json.Marshal(b)
	for _, secret := range []string{"SLACKTOKEN", "DISCORDTOKEN", "TGTOKEN", "PDROUTINGKEY", "TAMPER", "CFGKEY", "leak.test", "hooks.example.test"} {
		if strings.Contains(string(raw), secret) {
			t.Errorf("bundle contains %q", secret)
		}
	}
	// Tampered bundle: a secret in alert config is dropped on import.
	for i := range b.Sites[0].Alerts {
		b.Sites[0].Alerts[i].Config = json.RawMessage(`{"metric":"users","webhook_url":"https://leak.test/TAMPER2"}`)
	}
	if _, err := r.svc.Import(r.ctx, r.other, b, ImportOptions{}); err != nil {
		t.Fatal(err)
	}
	var n int
	_ = r.pool.QueryRow(r.ctx, `select count(*) from alerts a join sites s on s.id=a.site_id where s.org_id=2 and a.config::text like '%TAMPER2%'`).Scan(&n)
	if n != 0 {
		t.Fatal("tampered alert config was stored")
	}
	var disabled int
	_ = r.pool.QueryRow(r.ctx, `select count(*) from alerts a join sites s on s.id=a.site_id where s.org_id=2 and not a.enabled`).Scan(&disabled)
	if disabled == 0 {
		t.Fatal("alerts whose destination is missing must arrive disabled")
	}
}
