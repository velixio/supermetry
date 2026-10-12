package alerts

import (
	"context"
	"errors"
	"strings"
	"testing"
	"time"

	"github.com/jackc/pgx/v5"
	"github.com/jackc/pgx/v5/pgxpool"

	"supermetry/internal/db"
	"supermetry/internal/platform/authz"
	platformcrypto "supermetry/internal/platform/crypto"
	"supermetry/internal/platform/store"
	"supermetry/internal/testpg"
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
		Kind: ChannelTelegram, Name: "Telegram incidents", ChatID: "@supermetry_incidents", Enabled: &disabled,
	})
	if err != nil {
		t.Fatal(err)
	}
	if updated.Enabled || updated.Config.ChatID != "@supermetry_incidents" {
		t.Fatalf("updated = %+v", updated)
	}
	channels, err := service.DeliveryChannels(ctx, []int64{created.ID})
	if err != nil {
		t.Fatal(err)
	}
	if len(channels) != 1 || channels[0].BotToken != token || channels[0].ChatID != "@supermetry_incidents" || channels[0].Enabled {
		t.Fatalf("delivery channels = %+v", channels)
	}
	var auditPayload string
	if err := pool.QueryRow(ctx, `select metadata::text from audit_log where action='notification_channel.updated' and target_id=$1 order by id desc limit 1`, created.ID).Scan(&auditPayload); err != nil {
		t.Fatal(err)
	}
	if strings.Contains(auditPayload, token) || strings.Contains(auditPayload, "supermetry_incidents") {
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
	if _, err := service.UpdateChannel(ctx, other, created.ID, ChannelInput{Kind: ChannelTelegram, Name: "Stolen", ChatID: "@supermetry_alerts"}); !errors.Is(err, pgx.ErrNoRows) {
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

func TestServiceAlertChannelsRequiredOnlyWhenEnabled(t *testing.T) {
	service, pool, principal, siteID, _ := newServiceTest(t)
	ctx := context.Background()
	disabled := false
	enabled := true

	created, err := service.CreateAlert(ctx, principal, siteID, AlertInput{
		Kind: AlertTrafficSpike, Threshold: 3, Enabled: &disabled,
	})
	if err != nil {
		t.Fatalf("CreateAlert() disabled without channels: %v", err)
	}
	if created.Enabled || len(created.ChannelIDs) != 0 {
		t.Fatalf("CreateAlert() = %+v, want disabled without channels", created)
	}
	var storedNil bool
	if err := pool.QueryRow(ctx, `select channel_ids is null from alerts where id=$1`, created.ID).Scan(&storedNil); err != nil {
		t.Fatal(err)
	}
	if storedNil {
		t.Fatal("disabled alert channel_ids stored as SQL NULL")
	}
	if _, err := service.UpdateAlert(ctx, principal, siteID, created.ID, AlertInput{
		Kind: AlertTrafficSpike, Threshold: 3, ChannelIDs: []int64{}, Enabled: &enabled,
	}); !errors.Is(err, ErrInvalid) {
		t.Fatalf("UpdateAlert() enabling without channels error = %v, want ErrInvalid", err)
	}
	if _, err := service.CreateAlert(ctx, principal, siteID, AlertInput{
		Kind: AlertTrafficSpike, Threshold: 3, ChannelIDs: []int64{},
	}); !errors.Is(err, ErrInvalid) {
		t.Fatalf("CreateAlert() enabled without channels error = %v, want ErrInvalid", err)
	}
}

func TestServiceEnabledAlertsExcludesDeletedOrganizations(t *testing.T) {
	service, pool, principal, siteID, _ := newServiceTest(t)
	ctx := context.Background()
	channel, err := service.CreateChannel(ctx, principal, ChannelInput{
		Kind: ChannelEmail, Name: "Owners", Recipients: []string{"owner@example.test"},
	})
	if err != nil {
		t.Fatal(err)
	}
	alert, err := service.CreateAlert(ctx, principal, siteID, AlertInput{
		Kind: AlertTrafficSpike, Threshold: 5, ChannelIDs: []int64{channel.ID},
	})
	if err != nil {
		t.Fatal(err)
	}
	if alerts, err := service.EnabledAlerts(ctx); err != nil || len(alerts) != 1 || alerts[0].ID != alert.ID {
		t.Fatalf("EnabledAlerts() before organization deletion = %+v, %v", alerts, err)
	}
	if _, err := pool.Exec(ctx, `update orgs set deleted_at=now() where id=$1`, principal.OrgID); err != nil {
		t.Fatal(err)
	}

	alerts, err := service.EnabledAlerts(ctx)
	if err != nil {
		t.Fatal(err)
	}
	if len(alerts) != 0 {
		t.Fatalf("EnabledAlerts() after organization deletion = %+v, want empty", alerts)
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

func TestServiceTestChannelRateLimitsPerActorAndChannel(t *testing.T) {
	service, _, principal, _, _ := newServiceTestWithDeliverer(t, &fakeDeliverer{})
	created, err := service.CreateChannel(context.Background(), principal, ChannelInput{
		Kind: ChannelEmail, Name: "Owners", Recipients: []string{"owner@example.test"},
	})
	if err != nil {
		t.Fatal(err)
	}
	for attempt := 0; attempt < 3; attempt++ {
		if err := service.TestChannel(context.Background(), principal, created.ID); err != nil {
			t.Fatalf("attempt %d: %v", attempt+1, err)
		}
	}
	if err := service.TestChannel(context.Background(), principal, created.ID); !errors.Is(err, ErrRateLimited) {
		t.Fatalf("fourth attempt error=%v, want ErrRateLimited", err)
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

func TestServicePersistsDeliveryStateUntilAlertIsMarkedFired(t *testing.T) {
	service, _, principal, siteID, _ := newServiceTest(t)
	ctx := context.Background()
	channel, err := service.CreateChannel(ctx, principal, ChannelInput{Kind: ChannelEmail, Name: "Owners", Recipients: []string{"owner@example.test"}})
	if err != nil {
		t.Fatal(err)
	}
	alert, err := service.CreateAlert(ctx, principal, siteID, AlertInput{Kind: AlertTrafficSpike, Threshold: 5, ChannelIDs: []int64{channel.ID}})
	if err != nil {
		t.Fatal(err)
	}
	now := time.Date(2026, 10, 10, 12, 0, 0, 0, time.UTC)
	state := DeliveryState{
		Notification:        Notification{AlertID: alert.ID, SiteID: siteID, Kind: AlertTrafficSpike, Value: 7, Threshold: 5, From: now.Add(-time.Hour), To: now, FiredAt: now},
		ChannelIDs:          []int64{channel.ID},
		DeliveredChannelIDs: []int64{channel.ID},
	}
	if err := service.SaveDeliveryState(ctx, alert.ID, state); err != nil {
		t.Fatal(err)
	}

	alerts, err := service.EnabledAlerts(ctx)
	if err != nil {
		t.Fatal(err)
	}
	if len(alerts) != 1 || alerts[0].DeliveryState == nil {
		t.Fatalf("enabled alerts = %+v, want persisted delivery state", alerts)
	}
	got := alerts[0].DeliveryState
	if got.Notification != state.Notification || len(got.ChannelIDs) != 1 || got.ChannelIDs[0] != channel.ID || len(got.DeliveredChannelIDs) != 1 || got.DeliveredChannelIDs[0] != channel.ID {
		t.Fatalf("delivery state = %+v, want %+v", got, state)
	}

	if err := service.MarkFired(ctx, alert.ID, now); err != nil {
		t.Fatal(err)
	}
	alerts, err = service.EnabledAlerts(ctx)
	if err != nil {
		t.Fatal(err)
	}
	if len(alerts) != 1 || alerts[0].LastFiredAt == nil || !alerts[0].LastFiredAt.Equal(now) || alerts[0].DeliveryState != nil {
		t.Fatalf("alert after MarkFired = %+v", alerts)
	}
}

func TestServiceUpdatingAlertClearsPendingDeliveryState(t *testing.T) {
	service, _, principal, siteID, _ := newServiceTest(t)
	ctx := context.Background()
	channel, err := service.CreateChannel(ctx, principal, ChannelInput{Kind: ChannelEmail, Name: "Owners", Recipients: []string{"owner@example.test"}})
	if err != nil {
		t.Fatal(err)
	}
	alert, err := service.CreateAlert(ctx, principal, siteID, AlertInput{Kind: AlertTrafficSpike, Threshold: 5, ChannelIDs: []int64{channel.ID}})
	if err != nil {
		t.Fatal(err)
	}
	now := time.Date(2026, 10, 10, 12, 0, 0, 0, time.UTC)
	state := DeliveryState{
		Notification: Notification{AlertID: alert.ID, SiteID: siteID, Kind: AlertTrafficSpike, Value: 7, Threshold: 5, From: now.Add(-time.Hour), To: now, FiredAt: now},
		ChannelIDs:   []int64{channel.ID},
	}
	if err := service.SaveDeliveryState(ctx, alert.ID, state); err != nil {
		t.Fatal(err)
	}
	if _, err := service.UpdateAlert(ctx, principal, siteID, alert.ID, AlertInput{Kind: AlertTrafficSpike, Threshold: 8, ChannelIDs: []int64{channel.ID}}); err != nil {
		t.Fatal(err)
	}

	alerts, err := service.EnabledAlerts(ctx)
	if err != nil {
		t.Fatal(err)
	}
	if len(alerts) != 1 || alerts[0].DeliveryState != nil {
		t.Fatalf("alert after update = %+v, want pending delivery state cleared", alerts)
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

func TestServiceRetryDeliveryTransitionsFailedToSucceeded(t *testing.T) {
	deliverer := &fakeDeliverer{}
	service, pool, principal, siteID, _ := newServiceTestWithDeliverer(t, deliverer)
	ctx := context.Background()
	created, err := service.CreateChannel(ctx, principal, ChannelInput{Kind: ChannelEmail, Name: "Owners", Recipients: []string{"owner@example.test"}})
	if err != nil {
		t.Fatal(err)
	}
	channels, err := service.DeliveryChannels(ctx, []int64{created.ID})
	if err != nil {
		t.Fatal(err)
	}
	now := time.Date(2026, 10, 12, 12, 0, 0, 0, time.UTC)
	notification := Notification{AlertID: 44, SiteID: siteID, Kind: AlertTrafficDrop, Value: 4, Threshold: 20, FiredAt: now}
	if err := service.RecordDelivery(ctx, 44, channels[0], notification, 3, DeliveryFailed, "provider failed", now); err != nil {
		t.Fatal(err)
	}
	var id int64
	if err := pool.QueryRow(ctx, `select id from alert_delivery_log where alert_id=44`).Scan(&id); err != nil {
		t.Fatal(err)
	}
	got, err := service.RetryDelivery(ctx, principal, siteID, id)
	if err != nil {
		t.Fatal(err)
	}
	if got.Status != DeliverySucceeded || got.Attempts != 4 || deliverer.calls != 1 {
		t.Fatalf("retry = %+v, calls=%d", got, deliverer.calls)
	}
	if _, err := service.RetryDelivery(ctx, principal, siteID, id); !errors.Is(err, ErrInvalid) {
		t.Fatalf("second retry error=%v, want ErrInvalid", err)
	}
}

func TestServicePurgeHistoryUsesOrganizationRetention(t *testing.T) {
	service, pool, principal, siteID, _ := newServiceTest(t)
	ctx := context.Background()
	created, err := service.CreateChannel(ctx, principal, ChannelInput{Kind: ChannelEmail, Name: "Owners", Recipients: []string{"owner@example.test"}})
	if err != nil {
		t.Fatal(err)
	}
	channels, err := service.DeliveryChannels(ctx, []int64{created.ID})
	if err != nil {
		t.Fatal(err)
	}
	now := time.Date(2026, 10, 12, 12, 0, 0, 0, time.UTC)
	for index, age := range []time.Duration{8 * 24 * time.Hour, 6 * 24 * time.Hour} {
		fired := now.Add(-age)
		notification := Notification{AlertID: int64(50 + index), SiteID: siteID, Kind: AlertTrafficSpike, FiredAt: fired}
		if err := service.RecordDelivery(ctx, notification.AlertID, channels[0], notification, 1, DeliverySucceeded, "", fired); err != nil {
			t.Fatal(err)
		}
		if _, err := pool.Exec(ctx, `update alert_delivery_log set created_at=$2 where alert_id=$1`, notification.AlertID, fired); err != nil {
			t.Fatal(err)
		}
	}
	if _, err := service.SaveSettings(ctx, principal, 7); err != nil {
		t.Fatal(err)
	}
	count, err := service.PurgeHistory(ctx, now)
	if err != nil {
		t.Fatal(err)
	}
	if count != 1 {
		t.Fatalf("purged=%d, want 1", count)
	}
}

func TestServiceTemplateOverrideAndReset(t *testing.T) {
	service, _, principal, _, _ := newServiceTest(t)
	ctx := context.Background()
	input := TemplateInput{Subject: "Alert {{.alert_kind}}", Body: "Site {{.site_id}} reached {{.value}}"}
	if _, err := service.SaveTemplate(ctx, principal, ChannelEmail, input); err != nil {
		t.Fatal(err)
	}
	items, settings, err := service.ListTemplates(ctx, principal)
	if err != nil {
		t.Fatal(err)
	}
	if settings.HistoryRetentionDays != 30 || len(items) != 5 || items[0].IsDefault || items[0].Body != input.Body {
		t.Fatalf("templates=%+v settings=%+v", items, settings)
	}
	reset, err := service.ResetTemplate(ctx, principal, ChannelEmail)
	if err != nil {
		t.Fatal(err)
	}
	if !reset.IsDefault || reset.Body == input.Body {
		t.Fatalf("reset=%+v", reset)
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
	box, err := platformcrypto.NewSecretBox([]byte("01234567890123456789012345678901"), "supermetry/alerts/v1")
	if err != nil {
		t.Fatal(err)
	}
	service := New(&store.Store{Pool: pool}, box, deliverer)
	return service, pool, authz.Principal{UserID: userID, OrgID: orgID, Role: authz.RoleOwner}, siteID, goalID
}
