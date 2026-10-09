package alerts

import (
	"context"
	"errors"
	"strings"
	"testing"

	"github.com/jackc/pgx/v5"
	"github.com/jackc/pgx/v5/pgxpool"

	"panel/internal/db"
	"panel/internal/platform/authz"
	platformcrypto "panel/internal/platform/crypto"
	"panel/internal/platform/store"
	"panel/internal/testpg"
)

func TestServiceChannelCRUDEncryptsSecretsAndAudits(t *testing.T) {
	service, pool, principal, _, _ := newServiceTest(t)
	ctx := context.Background()
	created, err := service.CreateChannel(ctx, principal, ChannelInput{
		Kind: ChannelWebhook, Name: "Deploy hook", URL: "https://hooks.example.test/private", Secret: "signing-secret",
	})
	if err != nil {
		t.Fatal(err)
	}
	if created.ID == 0 || !created.Config.Configured || len(created.Config.Recipients) != 0 {
		t.Fatalf("CreateChannel() = %+v", created)
	}
	var raw string
	if err := pool.QueryRow(ctx, `select config::text from notification_channels where id=$1`, created.ID).Scan(&raw); err != nil {
		t.Fatal(err)
	}
	if strings.Contains(raw, "hooks.example.test") || strings.Contains(raw, "signing-secret") {
		t.Fatalf("stored config contains plaintext: %s", raw)
	}
	channels, err := service.DeliveryChannels(ctx, []int64{created.ID})
	if err != nil {
		t.Fatal(err)
	}
	if len(channels) != 1 || channels[0].URL != "https://hooks.example.test/private" || channels[0].Secret != "signing-secret" {
		t.Fatalf("DeliveryChannels() = %+v", channels)
	}
	updated, err := service.UpdateChannel(ctx, principal, created.ID, ChannelInput{Kind: ChannelEmail, Name: "Owners", Recipients: []string{"owner@example.test"}})
	if err != nil {
		t.Fatal(err)
	}
	if !updated.Config.Configured || len(updated.Config.Recipients) != 1 || updated.Config.Recipients[0] != "owner@example.test" {
		t.Fatalf("UpdateChannel() = %+v", updated)
	}
	listed, err := service.ListChannels(ctx, principal)
	if err != nil || len(listed) != 1 || listed[0].ID != created.ID {
		t.Fatalf("ListChannels() = %+v, %v", listed, err)
	}
	if err := service.DeleteChannel(ctx, principal, created.ID); err != nil {
		t.Fatal(err)
	}
	if listed, err = service.ListChannels(ctx, principal); err != nil || len(listed) != 0 {
		t.Fatalf("ListChannels() after delete = %+v, %v", listed, err)
	}
	var audits int
	if err := pool.QueryRow(ctx, `select count(*) from audit_log where target_type='notification_channel'`).Scan(&audits); err != nil {
		t.Fatal(err)
	}
	if audits != 3 {
		t.Fatalf("channel audit rows = %d, want 3", audits)
	}
}

func TestServiceTelegramChannelEncryptsTokenAndPreservesRedactedEdit(t *testing.T) {
	service, pool, principal, _, _ := newServiceTest(t)
	ctx := context.Background()
	token := "123456:fake-token-value-for-tests"
	created, err := service.CreateChannel(ctx, principal, ChannelInput{
		Kind: ChannelTelegram, Name: "Telegram ops", BotToken: token, ChatID: "-1001234567890",
	})
	if err != nil {
		t.Fatal(err)
	}
	if !created.Enabled || !created.Config.Configured || created.Config.ChatID != "-1001234567890" {
		t.Fatalf("created = %+v", created)
	}
	var raw string
	if err := pool.QueryRow(ctx, `select config::text from notification_channels where id=$1`, created.ID).Scan(&raw); err != nil {
		t.Fatal(err)
	}
	if strings.Contains(raw, token) {
		t.Fatal("stored Telegram config contains plaintext token")
	}
	disabled := false
	updated, err := service.UpdateChannel(ctx, principal, created.ID, ChannelInput{
		Kind: ChannelTelegram, Name: "Telegram incidents", ChatID: "@panel_incidents", Enabled: &disabled,
	})
	if err != nil {
		t.Fatal(err)
	}
	if updated.Enabled || updated.Config.ChatID != "@panel_incidents" {
		t.Fatalf("updated = %+v", updated)
	}
	channels, err := service.DeliveryChannels(ctx, []int64{created.ID})
	if err != nil {
		t.Fatal(err)
	}
	if len(channels) != 1 || channels[0].BotToken != token || channels[0].ChatID != "@panel_incidents" || channels[0].Enabled {
		t.Fatalf("delivery channels = %+v", channels)
	}
	var auditPayload string
	if err := pool.QueryRow(ctx, `select metadata::text from audit_log where action='notification_channel.updated' and target_id=$1 order by id desc limit 1`, created.ID).Scan(&auditPayload); err != nil {
		t.Fatal(err)
	}
	if strings.Contains(auditPayload, token) || strings.Contains(auditPayload, "panel_incidents") {
		t.Fatalf("audit payload leaked Telegram config: %s", auditPayload)
	}
}

func TestServiceTelegramChannelIsTenantScoped(t *testing.T) {
	service, pool, principal, _, _ := newServiceTest(t)
	ctx := context.Background()
	created, err := service.CreateChannel(ctx, principal, ChannelInput{
		Kind: ChannelTelegram, Name: "Telegram ops", BotToken: "123456:fake-token-value-for-tests", ChatID: "-1001234567890",
	})
	if err != nil {
		t.Fatal(err)
	}
	var otherOrgID int64
	if err := pool.QueryRow(ctx, `insert into orgs(name,slug) values('Other','other') returning id`).Scan(&otherOrgID); err != nil {
		t.Fatal(err)
	}
	other := authz.Principal{UserID: principal.UserID + 1, OrgID: otherOrgID, Role: authz.RoleOwner}
	if listed, err := service.ListChannels(ctx, other); err != nil || len(listed) != 0 {
		t.Fatalf("other tenant list = %+v, %v", listed, err)
	}
	if _, err := service.UpdateChannel(ctx, other, created.ID, ChannelInput{Kind: ChannelTelegram, Name: "Stolen", ChatID: "@panel_alerts"}); !errors.Is(err, pgx.ErrNoRows) {
		t.Fatalf("cross-tenant update error = %v, want ErrNoRows", err)
	}
	if err := service.TestChannel(ctx, other, created.ID); !errors.Is(err, pgx.ErrNoRows) {
		t.Fatalf("cross-tenant test error = %v, want ErrNoRows", err)
	}
}

func TestServiceAlertCRUDValidatesRelationsAndAudits(t *testing.T) {
	service, pool, principal, siteID, goalID := newServiceTest(t)
	ctx := context.Background()
	channel, err := service.CreateChannel(ctx, principal, ChannelInput{Kind: ChannelEmail, Name: "Owners", Recipients: []string{"owner@example.test"}})
	if err != nil {
		t.Fatal(err)
	}
	created, err := service.CreateAlert(ctx, principal, siteID, AlertInput{
		Kind: AlertGoalReached, Threshold: 3, GoalID: &goalID, ChannelIDs: []int64{channel.ID},
	})
	if err != nil {
		t.Fatal(err)
	}
	if !created.Enabled || created.GoalID == nil || *created.GoalID != goalID {
		t.Fatalf("CreateAlert() = %+v", created)
	}
	if _, err := service.CreateAlert(ctx, principal, siteID, AlertInput{Kind: AlertTrafficSpike, Threshold: 2, ChannelIDs: []int64{999999}}); err == nil {
		t.Fatal("CreateAlert() accepted channel from outside site's organization")
	}
	disabled := false
	updated, err := service.UpdateAlert(ctx, principal, siteID, created.ID, AlertInput{Kind: AlertTrafficDrop, Threshold: 40, ChannelIDs: []int64{channel.ID}, Enabled: &disabled})
	if err != nil {
		t.Fatal(err)
	}
	if updated.Enabled || updated.GoalID != nil || updated.Kind != AlertTrafficDrop {
		t.Fatalf("UpdateAlert() = %+v", updated)
	}
	listed, err := service.ListAlerts(ctx, principal, siteID)
	if err != nil || len(listed) != 1 || listed[0].ID != created.ID {
		t.Fatalf("ListAlerts() = %+v, %v", listed, err)
	}
	if err := service.DeleteAlert(ctx, principal, siteID, created.ID); err != nil {
		t.Fatal(err)
	}
	var audits int
	if err := pool.QueryRow(ctx, `select count(*) from audit_log where target_type='alert'`).Scan(&audits); err != nil {
		t.Fatal(err)
	}
	if audits != 3 {
		t.Fatalf("alert audit rows = %d, want 3", audits)
	}
}

func TestServiceTestChannelUsesDecryptedConfigurationAndAudits(t *testing.T) {
	deliverer := &fakeDeliverer{}
	service, pool, principal, _, _ := newServiceTestWithDeliverer(t, deliverer)
	created, err := service.CreateChannel(context.Background(), principal, ChannelInput{
		Kind: ChannelWebhook, Name: "Hook", URL: "https://hooks.example.test/secret", Secret: "key",
	})
	if err != nil {
		t.Fatal(err)
	}
	if err := service.TestChannel(context.Background(), principal, created.ID); err != nil {
		t.Fatal(err)
	}
	if deliverer.calls != 1 {
		t.Fatalf("delivery calls = %d, want 1", deliverer.calls)
	}
	var audits int
	if err := pool.QueryRow(context.Background(), `select count(*) from audit_log where action='notification_channel.tested' and target_id=$1`, created.ID).Scan(&audits); err != nil {
		t.Fatal(err)
	}
	if audits != 1 {
		t.Fatalf("channel test audit rows = %d, want 1", audits)
	}
}

func TestServiceAlertMutationsAreScopedToPathSite(t *testing.T) {
	service, pool, principal, siteID, _ := newServiceTest(t)
	ctx := context.Background()
	var otherSite int32
	if err := pool.QueryRow(ctx, `insert into sites(org_id,public_id,name,timezone,retention_class) values ($1,'siteotherabc','Other','UTC',30) returning id`, principal.OrgID).Scan(&otherSite); err != nil {
		t.Fatal(err)
	}
	channel, err := service.CreateChannel(ctx, principal, ChannelInput{Kind: ChannelEmail, Name: "Owners", Recipients: []string{"owner@example.test"}})
	if err != nil {
		t.Fatal(err)
	}
	created, err := service.CreateAlert(ctx, principal, siteID, AlertInput{Kind: AlertTrafficSpike, Threshold: 5, ChannelIDs: []int64{channel.ID}})
	if err != nil {
		t.Fatal(err)
	}
	if _, err := service.UpdateAlert(ctx, principal, otherSite, created.ID, AlertInput{Kind: AlertTrafficSpike, Threshold: 9, ChannelIDs: []int64{channel.ID}}); !errors.Is(err, pgx.ErrNoRows) {
		t.Fatalf("UpdateAlert() via other site error = %v, want ErrNoRows", err)
	}
	if err := service.DeleteAlert(ctx, principal, otherSite, created.ID); !errors.Is(err, pgx.ErrNoRows) {
		t.Fatalf("DeleteAlert() via other site error = %v, want ErrNoRows", err)
	}
	if err := service.DeleteAlert(ctx, principal, siteID, created.ID); err != nil {
		t.Fatal(err)
	}
}

func TestServiceDeleteChannelRejectsChannelInUse(t *testing.T) {
	service, _, principal, siteID, _ := newServiceTest(t)
	ctx := context.Background()
	channel, err := service.CreateChannel(ctx, principal, ChannelInput{Kind: ChannelEmail, Name: "Owners", Recipients: []string{"owner@example.test"}})
	if err != nil {
		t.Fatal(err)
	}
	created, err := service.CreateAlert(ctx, principal, siteID, AlertInput{Kind: AlertTrafficSpike, Threshold: 5, ChannelIDs: []int64{channel.ID}})
	if err != nil {
		t.Fatal(err)
	}
	if err := service.DeleteChannel(ctx, principal, channel.ID); !errors.Is(err, ErrChannelInUse) {
		t.Fatalf("DeleteChannel() error = %v, want ErrChannelInUse", err)
	}
	if err := service.DeleteAlert(ctx, principal, siteID, created.ID); err != nil {
		t.Fatal(err)
	}
	if err := service.DeleteChannel(ctx, principal, channel.ID); err != nil {
		t.Fatalf("DeleteChannel() after alert removal = %v", err)
	}
}

func newServiceTest(t *testing.T) (*Service, *pgxpool.Pool, authz.Principal, int32, int64) {
	return newServiceTestWithDeliverer(t, &fakeDeliverer{})
}

func newServiceTestWithDeliverer(t *testing.T, deliverer Deliverer) (*Service, *pgxpool.Pool, authz.Principal, int32, int64) {
	t.Helper()
	pool, _ := testpg.New(t)
	ctx := context.Background()
	if err := db.Migrate(ctx, pool); err != nil {
		t.Fatal(err)
	}
	var userID, orgID int64
	if err := pool.QueryRow(ctx, `insert into users(email,name,password_hash) values ('owner@example.test','Owner','hash') returning id`).Scan(&userID); err != nil {
		t.Fatal(err)
	}
	if err := pool.QueryRow(ctx, `insert into orgs(name,slug) values ('Example','example') returning id`).Scan(&orgID); err != nil {
		t.Fatal(err)
	}
	if _, err := pool.Exec(ctx, `insert into memberships(org_id,user_id,role) values ($1,$2,'owner')`, orgID, userID); err != nil {
		t.Fatal(err)
	}
	var siteID int32
	if err := pool.QueryRow(ctx, `insert into sites(org_id,public_id,name,timezone,retention_class) values ($1,'siteabcdefgh','Site','UTC',30) returning id`, orgID).Scan(&siteID); err != nil {
		t.Fatal(err)
	}
	var goalID int64
	if err := pool.QueryRow(ctx, `insert into goals(site_id,kind,match,value,name) values ($1,'event','exact','signup','Signup') returning id`, siteID).Scan(&goalID); err != nil {
		t.Fatal(err)
	}
	box, err := platformcrypto.NewSecretBox([]byte("01234567890123456789012345678901"), "panel/alerts/v1")
	if err != nil {
		t.Fatal(err)
	}
	service := New(&store.Store{Pool: pool}, box, deliverer)
	return service, pool, authz.Principal{UserID: userID, OrgID: orgID, Role: authz.RoleOwner}, siteID, goalID
}
