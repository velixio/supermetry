package stripe

import (
	"context"
	"encoding/json"
	"errors"
	"fmt"
	"net/http"
	"strings"
	"sync"
	"testing"
	"time"

	"github.com/jackc/pgx/v5/pgxpool"

	"supermetry/internal/db"
	"supermetry/internal/dict"
	"supermetry/internal/integrations"
	"supermetry/internal/platform/authz"
	platformcrypto "supermetry/internal/platform/crypto"
	"supermetry/internal/platform/store"
	"supermetry/internal/privacy"
	"supermetry/internal/session"
	"supermetry/internal/sites"
	"supermetry/internal/testpg"
	"supermetry/internal/writer"
)

var serviceNow = time.Date(2026, 11, 3, 12, 0, 0, 0, time.UTC)

type captureSink struct {
	mu     sync.Mutex
	events []writer.Event
	full   bool
}

type captureSlot struct {
	sink *captureSink
	done bool
}

func (s *captureSink) Reserve() (Slot, bool) {
	if s.full {
		return nil, false
	}
	return &captureSlot{sink: s}, true
}

func (s *captureSlot) Commit(e writer.Event) bool {
	if s.done {
		return false
	}
	s.done = true
	s.sink.mu.Lock()
	s.sink.events = append(s.sink.events, e)
	s.sink.mu.Unlock()
	return true
}

func (s *captureSlot) Cancel() { s.done = true }

type serviceFixture struct {
	svc      *Service
	pool     *pgxpool.Pool
	sink     *captureSink
	dict     *dict.Dict
	site     sites.Site
	owner    authz.Principal
	editor   authz.Principal
	otherOrg authz.Principal
	registry registry
	now      time.Time
}

func newServiceFixture(t *testing.T) *serviceFixture {
	t.Helper()
	pool, _ := testpg.New(t)
	ctx := context.Background()
	if err := db.Migrate(ctx, pool); err != nil {
		t.Fatal(err)
	}
	var userID, otherUser, orgID, otherOrgID int64
	for _, q := range []struct {
		sql  string
		dest *int64
	}{
		{`insert into users(email,name,password_hash) values ('owner@example.test','Owner','h') returning id`, &userID},
		{`insert into users(email,name,password_hash) values ('other@example.test','Other','h') returning id`, &otherUser},
		{`insert into orgs(name,slug) values ('Example','example') returning id`, &orgID},
		{`insert into orgs(name,slug) values ('Other','other') returning id`, &otherOrgID},
	} {
		if err := pool.QueryRow(ctx, q.sql).Scan(q.dest); err != nil {
			t.Fatal(err)
		}
	}
	var siteID int32
	if err := pool.QueryRow(ctx, `insert into sites(org_id,public_id,name,timezone,retention_class,revenue_currency,revenue_currency_state)
		values ($1,'siteabcdefgh','Site','UTC',30,'EUR','configured') returning id`, orgID).Scan(&siteID); err != nil {
		t.Fatal(err)
	}
	site := sites.Site{ID: siteID, PublicID: "siteabcdefgh", TZ: time.UTC, Retention: 30, ProfilesEnabled: true,
		RevenueCurrency: "EUR", RevenueCurrencyState: sites.RevenueCurrencyConfigured, AllowedHosts: []string{"shop.example.test"}}
	secrets, err := platformcrypto.NewSecretBox([]byte("01234567890123456789012345678901"), "supermetry/stripe/v1")
	if err != nil {
		t.Fatal(err)
	}
	dictionary := dict.New(pool)
	if err := dictionary.Load(ctx); err != nil {
		t.Fatal(err)
	}
	f := &serviceFixture{
		pool: pool, sink: &captureSink{}, dict: dictionary, site: site, registry: registry{site.PublicID: site}, now: serviceNow,
		owner:    authz.Principal{UserID: userID, OrgID: orgID, Role: authz.RoleOwner},
		editor:   authz.Principal{UserID: userID + 100, OrgID: orgID, Role: authz.RoleEditor},
		otherOrg: authz.Principal{UserID: otherUser, OrgID: otherOrgID, Role: authz.RoleOwner},
	}
	f.svc = New(Deps{
		Store: &store.Store{Pool: pool}, Secrets: secrets, Tokens: testTokens(t, "01234567890123456789012345678901"),
		Sites: f.registry, Dictionary: dictionary, Sink: f.sink, Mode: privacy.Identified, BaseURL: "https://stats.example.test/",
		Now: func() time.Time { return f.now },
	})
	return f
}

const testSecret = "whsec_dGVzdC1zaWduaW5nLXNlY3JldC0xMjM0NTY3OA"

func (f *serviceFixture) connect(t *testing.T) string {
	t.Helper()
	ctx := context.Background()
	if _, err := f.svc.Enable(ctx, f.owner, f.site.ID); err != nil {
		t.Fatal(err)
	}
	secret := testSecret
	status, err := f.svc.Configure(ctx, f.owner, f.site.ID, Update{SigningSecret: &secret})
	if err != nil {
		t.Fatal(err)
	}
	return strings.TrimPrefix(status.WebhookURL, "https://stats.example.test"+WebhookPath)
}

func (f *serviceFixture) deliver(t *testing.T, endpoint string, event map[string]any) int {
	t.Helper()
	body, err := json.Marshal(event)
	if err != nil {
		t.Fatal(err)
	}
	return f.svc.HandleWebhook(context.Background(), endpoint, SignatureHeader(body, []byte(testSecret), f.now), body)
}

func stripeEvent(id, eventType string, created time.Time, object map[string]any) map[string]any {
	return map[string]any{"id": id, "object": "event", "type": eventType, "livemode": true, "created": created.Unix(),
		"api_version": "2025-03-31.basil", "data": map[string]any{"object": object}}
}

func (f *serviceFixture) status(t *testing.T) Status {
	t.Helper()
	status, err := f.svc.Get(context.Background(), f.owner, f.site.ID)
	if err != nil {
		t.Fatal(err)
	}
	return status
}

func (f *serviceFixture) dictID(t *testing.T, kind dict.Kind, value string) int32 {
	t.Helper()
	ids, err := f.dict.IDsForSite(context.Background(), f.site.ID, kind, []string{value})
	if err != nil {
		t.Fatal(err)
	}
	return ids[0]
}

func TestServiceConnectionLifecycle(t *testing.T) {
	f := newServiceFixture(t)
	ctx := context.Background()

	status := f.status(t)
	if status.Enabled || status.RevenueCurrency != "EUR" || len(status.Events) == 0 {
		t.Fatalf("initial status %+v", status)
	}
	for name, principal := range map[string]authz.Principal{"editor": f.editor, "other org": f.otherOrg} {
		if _, err := f.svc.Enable(ctx, principal, f.site.ID); !errors.Is(err, integrations.ErrForbidden) && !errors.Is(err, integrations.ErrNotFound) {
			t.Fatalf("%s enable: %v", name, err)
		}
	}
	if _, err := f.svc.Get(ctx, f.editor, f.site.ID); err != nil {
		t.Fatalf("editor cannot read status: %v", err)
	}
	if _, err := f.svc.Get(ctx, authz.Principal{OrgID: f.owner.OrgID, Role: authz.RoleKey}, f.site.ID); !errors.Is(err, integrations.ErrForbidden) {
		t.Fatalf("API key read status: %v", err)
	}
	secret := testSecret
	if _, err := f.svc.Configure(ctx, f.owner, f.site.ID, Update{SigningSecret: &secret}); !errors.Is(err, integrations.ErrNotFound) {
		t.Fatalf("configure before enable: %v", err)
	}

	first, err := f.svc.Enable(ctx, f.owner, f.site.ID)
	if err != nil {
		t.Fatal(err)
	}
	if !first.Enabled || first.SecretConfigured || !strings.HasPrefix(first.WebhookURL, "https://stats.example.test"+WebhookPath) {
		t.Fatalf("enabled status %+v", first)
	}
	again, err := f.svc.Enable(ctx, f.owner, f.site.ID)
	if err != nil || again.WebhookURL != first.WebhookURL {
		t.Fatalf("enable is not idempotent: %+v %v", again, err)
	}

	for _, bad := range []string{"", "sk_live_123", "whsec_short", "whsec_" + strings.Repeat("a", 300), "whsec_bad chars here!!!!!!!"} {
		if _, err := f.svc.Configure(ctx, f.owner, f.site.ID, Update{SigningSecret: &bad}); !errors.Is(err, integrations.ErrInvalid) {
			t.Fatalf("secret %q: %v", bad, err)
		}
	}
	padded := "  " + testSecret + "\n"
	on := true
	configured, err := f.svc.Configure(ctx, f.owner, f.site.ID, Update{SigningSecret: &padded, RecordPaymentIntents: &on})
	if err != nil || !configured.SecretConfigured || !configured.RecordPaymentIntents {
		t.Fatalf("configure: %+v %v", configured, err)
	}
	var stored []byte
	if err := f.pool.QueryRow(ctx, `select signing_secret from stripe_connections where site_id=$1`, f.site.ID).Scan(&stored); err != nil {
		t.Fatal(err)
	}
	if strings.Contains(string(stored), "whsec_") {
		t.Fatal("signing secret stored in plaintext")
	}
	encoded, _ := json.Marshal(configured)
	if strings.Contains(string(encoded), "whsec_") {
		t.Fatal("status exposes the signing secret")
	}

	if err := f.svc.Disconnect(ctx, f.editor, f.site.ID); !errors.Is(err, integrations.ErrForbidden) {
		t.Fatalf("editor disconnect: %v", err)
	}
	if err := f.svc.Disconnect(ctx, f.otherOrg, f.site.ID); !errors.Is(err, integrations.ErrForbidden) && !errors.Is(err, integrations.ErrNotFound) {
		t.Fatalf("other org disconnect: %v", err)
	}
	if err := f.svc.Disconnect(ctx, f.owner, f.site.ID); err != nil {
		t.Fatal(err)
	}
	if err := f.svc.Disconnect(ctx, f.owner, f.site.ID); !errors.Is(err, integrations.ErrNotFound) {
		t.Fatalf("second disconnect: %v", err)
	}
	for action, want := range map[string]int{"integration.connected": 1, "integration.updated": 1, "integration.disconnected": 1} {
		var count int
		if err := f.pool.QueryRow(ctx, `select count(*) from audit_log where org_id=$1 and action=$2 and target_type='stripe_connection'
			and metadata::text not like '%whsec%'`, f.owner.OrgID, action).Scan(&count); err != nil || count != want {
			t.Fatalf("audit %s: %d %v", action, count, err)
		}
	}
}

func TestServiceRejectsDemoSites(t *testing.T) {
	f := newServiceFixture(t)
	demo := f.site
	demo.Demo = true
	f.registry[demo.PublicID] = demo
	if _, err := f.svc.Enable(context.Background(), f.owner, f.site.ID); !errors.Is(err, integrations.ErrInvalid) {
		t.Fatalf("demo enable: %v", err)
	}
}

func TestWebhookRecordsAttributedPurchaseRenewalAndRefund(t *testing.T) {
	f := newServiceFixture(t)
	ctx := context.Background()
	endpoint := f.connect(t)

	source, medium, campaign := f.dictID(t, dict.Source, "newsletter"), f.dictID(t, dict.Medium, "email"), f.dictID(t, dict.Campaign, "launch")
	token := f.svc.tokens.Seal(Attribution{SiteID: f.site.ID, SessionID: 99, VisitorID: 1234, Source: source, UTMSource: source, Medium: medium,
		Campaign: campaign, Country: "DE", IssuedAt: f.now.Add(-time.Hour)})
	created := f.now.Add(-time.Minute)
	checkout := stripeEvent("evt_checkout", "checkout.session.completed", created, map[string]any{
		"id": "cs_live_1", "object": "checkout.session", "mode": "subscription", "payment_status": "paid", "amount_total": 4900,
		"currency": "eur", "client_reference_id": token, "customer": "cus_1",
		"customer_details": map[string]any{"email": "buyer@example.test", "name": "Jane Buyer"},
		"metadata":         map[string]any{"supermetry_user_id": "user-42", "supermetry_product": "Pro"},
	})
	if code := f.deliver(t, endpoint, checkout); code != http.StatusOK {
		t.Fatalf("checkout: %d", code)
	}
	if code := f.deliver(t, endpoint, checkout); code != http.StatusOK {
		t.Fatalf("redelivery: %d", code)
	}
	if len(f.sink.events) != 1 {
		t.Fatalf("redelivery recorded twice: %d events", len(f.sink.events))
	}
	purchase := f.sink.events[0]
	if purchase.RevenueMicros != 49_000_000 || purchase.RevenueCurrency != "EUR" || purchase.SessionID != 99 || purchase.VisitorID != 1234 ||
		purchase.Source != source || purchase.Medium != medium || purchase.Campaign != campaign || purchase.Country != "DE" ||
		purchase.ProfileExternalID != "user-42" || !purchase.TS.Equal(created) || purchase.Kind != 1 ||
		purchase.NameID != f.dictID(t, dict.EventName, EventPurchase) || purchase.Props["product"] != "Pro" {
		t.Fatalf("purchase event %+v", purchase)
	}
	var customers int
	var customerKey []byte
	if err := f.pool.QueryRow(ctx, `select count(*),min(customer_key) from stripe_customers where site_id=$1 and source='newsletter' and campaign='launch'`, f.site.ID).
		Scan(&customers, &customerKey); err != nil || customers != 1 || strings.Contains(string(customerKey), "cus_1") {
		t.Fatalf("customer channel rows=%d key=%q err=%v", customers, customerKey, err)
	}

	// A month later the renewal carries no token; the stored channel is reused.
	f.now = f.now.AddDate(0, 1, 0)
	renewal := stripeEvent("evt_renewal", "invoice.paid", f.now, map[string]any{
		"id": "in_2", "object": "invoice", "customer": "cus_1", "amount_paid": 4900, "currency": "eur", "billing_reason": "subscription_cycle",
		"customer_email": "buyer@example.test",
		"parent":         map[string]any{"subscription_details": map[string]any{"subscription": "sub_1", "metadata": map[string]any{"supermetry_user_id": "user-42"}}},
	})
	if code := f.deliver(t, endpoint, renewal); code != http.StatusOK || len(f.sink.events) != 2 {
		t.Fatalf("renewal: %d events=%d", code, len(f.sink.events))
	}
	renewed := f.sink.events[1]
	if renewed.Source != source || renewed.Medium != medium || renewed.Campaign != campaign || renewed.SessionID != 0 || renewed.VisitorID == 1234 ||
		renewed.VisitorID <= 0 || renewed.ProfileExternalID != "user-42" || renewed.Props["product"] != "Pro" ||
		renewed.NameID != f.dictID(t, dict.EventName, EventRenewal) || renewed.RevenueMicros != 49_000_000 {
		t.Fatalf("renewal event %+v", renewed)
	}

	// The first subscription invoice duplicates the checkout and is skipped.
	first := stripeEvent("evt_first_invoice", "invoice.paid", f.now, map[string]any{
		"id": "in_1", "object": "invoice", "customer": "cus_1", "amount_paid": 4900, "currency": "eur", "billing_reason": "subscription_create", "subscription": "sub_1",
	})
	if code := f.deliver(t, endpoint, first); code != http.StatusOK || len(f.sink.events) != 2 {
		t.Fatalf("first invoice: %d events=%d", code, len(f.sink.events))
	}

	for i, refunded := range []int{1000, 2500, 1000} {
		refund := stripeEvent(fmt.Sprintf("evt_refund_%d", i), "charge.refunded", f.now, map[string]any{
			"id": "ch_1", "object": "charge", "amount_refunded": refunded, "currency": "eur", "customer": "cus_1",
		})
		if code := f.deliver(t, endpoint, refund); code != http.StatusOK {
			t.Fatalf("refund %d: %d", i, code)
		}
	}
	status := f.status(t)
	if status.Refunds30d != 1 || status.RefundedMicros30d != 25_000_000 || len(f.sink.events) != 2 {
		t.Fatalf("refund ledger %+v events=%d", status, len(f.sink.events))
	}
	if status.EventsReceived != 6 || status.RevenueEvents != 2 || status.LastEventType != "charge.refunded" || status.LastError != "" ||
		status.LastEventLivemode == nil || !*status.LastEventLivemode {
		t.Fatalf("status %+v", status)
	}

	var stored string
	if err := f.pool.QueryRow(ctx, `select concat_ws(' ',(select string_agg(event_id,' ') from stripe_webhook_events),
		(select string_agg(concat_ws(' ',source,utm_source,medium,campaign,country,product),' ') from stripe_customers))`).Scan(&stored); err != nil {
		t.Fatal(err)
	}
	for _, pii := range []string{"buyer@example.test", "Jane", "cus_1", "ch_1"} {
		if strings.Contains(stored, pii) {
			t.Fatalf("stored personal data %q: %s", pii, stored)
		}
	}

	if err := f.svc.Disconnect(ctx, f.owner, f.site.ID); err != nil {
		t.Fatal(err)
	}
	for _, table := range []string{"stripe_connections", "stripe_webhook_events", "stripe_customers", "stripe_refunds"} {
		var rows int
		if err := f.pool.QueryRow(ctx, `select count(*) from `+table).Scan(&rows); err != nil || rows != 0 {
			t.Fatalf("%s kept %d rows after disconnect (%v)", table, rows, err)
		}
	}
	if code := f.deliver(t, endpoint, renewal); code != http.StatusBadRequest {
		t.Fatalf("delivery after disconnect: %d", code)
	}
}

func TestWebhookUnattributedAndRejectedDeliveries(t *testing.T) {
	f := newServiceFixture(t)
	endpoint := f.connect(t)
	body := func(id, currency string, amount int, livemode bool) map[string]any {
		event := stripeEvent(id, "checkout.session.completed", f.now, map[string]any{
			"id": "cs_" + id, "object": "checkout.session", "mode": "payment", "payment_status": "paid", "amount_total": amount, "currency": currency,
			"client_reference_id": "order-1",
		})
		event["livemode"] = livemode
		return event
	}

	if code := f.deliver(t, endpoint, body("evt_plain", "eur", 1999, true)); code != http.StatusOK || len(f.sink.events) != 1 {
		t.Fatalf("unattributed purchase: %d", code)
	}
	plain := f.sink.events[0]
	if plain.Source != 0 || plain.SessionID != 0 || plain.VisitorID <= 0 || plain.RevenueMicros != 19_990_000 {
		t.Fatalf("unattributed event %+v", plain)
	}

	if code := f.deliver(t, endpoint, body("evt_usd", "usd", 1999, true)); code != http.StatusOK || len(f.sink.events) != 1 {
		t.Fatalf("currency mismatch: %d events=%d", code, len(f.sink.events))
	}
	if status := f.status(t); !strings.Contains(status.LastError, "USD") {
		t.Fatalf("currency mismatch not reported: %+v", status)
	}
	if code := f.deliver(t, endpoint, body("evt_test", "eur", 1999, false)); code != http.StatusOK || len(f.sink.events) != 1 {
		t.Fatalf("test mode: %d events=%d", code, len(f.sink.events))
	}
	if status := f.status(t); status.LastError != "" || status.LastEventLivemode == nil || *status.LastEventLivemode {
		t.Fatalf("test-mode status %+v", status)
	}

	f.sink.full = true
	if code := f.deliver(t, endpoint, body("evt_busy", "eur", 1999, true)); code != http.StatusServiceUnavailable {
		t.Fatalf("full writer: %d", code)
	}
	f.sink.full = false
	if code := f.deliver(t, endpoint, body("evt_busy", "eur", 1999, true)); code != http.StatusOK || len(f.sink.events) != 2 {
		t.Fatalf("retry after full writer: %d events=%d", code, len(f.sink.events))
	}

	payload, _ := json.Marshal(body("evt_badsig", "eur", 1, true))
	for name, call := range map[string]func() int{
		"wrong secret": func() int {
			return f.svc.HandleWebhook(context.Background(), endpoint, SignatureHeader(payload, []byte("whsec_other"), f.now), payload)
		},
		"stale": func() int {
			return f.svc.HandleWebhook(context.Background(), endpoint, SignatureHeader(payload, []byte(testSecret), f.now.Add(-time.Hour)), payload)
		},
		"unknown endpoint": func() int {
			return f.svc.HandleWebhook(context.Background(), strings.Repeat("A", 43), SignatureHeader(payload, []byte(testSecret), f.now), payload)
		},
		"malformed endpoint": func() int {
			return f.svc.HandleWebhook(context.Background(), "../x", SignatureHeader(payload, []byte(testSecret), f.now), payload)
		},
	} {
		if code := call(); code != http.StatusBadRequest {
			t.Fatalf("%s: %d", name, code)
		}
	}
	if status := f.status(t); !strings.Contains(status.LastError, "signature") || len(f.sink.events) != 2 {
		t.Fatalf("bad signature status %+v", status)
	}

	f.registry[f.site.PublicID] = func() sites.Site {
		s := f.site
		s.RevenueCurrencyState = sites.RevenueCurrencyUnconfigured
		return s
	}()
	if code := f.deliver(t, endpoint, body("evt_nocurrency", "eur", 1999, true)); code != http.StatusOK || len(f.sink.events) != 2 {
		t.Fatalf("no site currency: %d", code)
	}
	if status := f.status(t); !strings.Contains(status.LastError, "revenue currency") {
		t.Fatalf("missing currency not reported: %+v", status)
	}
}

func TestWebhookPendingConnectionAndAnonymousMode(t *testing.T) {
	f := newServiceFixture(t)
	ctx := context.Background()
	status, err := f.svc.Enable(ctx, f.owner, f.site.ID)
	if err != nil {
		t.Fatal(err)
	}
	endpoint := strings.TrimPrefix(status.WebhookURL, "https://stats.example.test"+WebhookPath)
	purchase := stripeEvent("evt_pending", "checkout.session.completed", f.now, map[string]any{
		"id": "cs_1", "object": "checkout.session", "payment_status": "paid", "amount_total": 500, "currency": "eur", "customer": "cus_9",
	})
	if code := f.deliver(t, endpoint, purchase); code != http.StatusBadRequest {
		t.Fatalf("pending connection: %d", code)
	}
	if status := f.status(t); !strings.Contains(status.LastError, "signing secret") {
		t.Fatalf("pending status %+v", status)
	}

	secret := testSecret
	if _, err := f.svc.Configure(ctx, f.owner, f.site.ID, Update{SigningSecret: &secret}); err != nil {
		t.Fatal(err)
	}
	f.svc.mode = privacy.Anonymous
	token := f.svc.tokens.Seal(Attribution{SiteID: f.site.ID, SessionID: 5, VisitorID: 6, Source: f.dictID(t, dict.Source, "ads"), IssuedAt: f.now})
	purchase["data"] = map[string]any{"object": map[string]any{
		"id": "cs_1", "object": "checkout.session", "payment_status": "paid", "amount_total": 500, "currency": "eur", "customer": "cus_9",
		"client_reference_id": token,
	}}
	if code := f.deliver(t, endpoint, purchase); code != http.StatusOK || len(f.sink.events) != 1 {
		t.Fatalf("anonymous purchase: %d", code)
	}
	var rows int
	if err := f.pool.QueryRow(ctx, `select count(*) from stripe_customers`).Scan(&rows); err != nil || rows != 0 {
		t.Fatalf("anonymous mode stored a customer channel: %d %v", rows, err)
	}
}

func TestStripeRetention(t *testing.T) {
	f := newServiceFixture(t)
	ctx := context.Background()
	f.connect(t)
	old, fresh := f.now.Add(-EventRetention-time.Hour), f.now.Add(-time.Hour)
	for _, q := range []struct {
		sql  string
		args []any
	}{
		{`insert into stripe_webhook_events(site_id,event_id,received_at) values($1,'evt_old',$2),($1,'evt_new',$3)`, []any{f.site.ID, old, fresh}},
		{`insert into stripe_customers(site_id,customer_key,last_seen_at) values($1,sha256('a'),$2),($1,sha256('b'),$3)`,
			[]any{f.site.ID, f.now.Add(-CustomerRetention - time.Hour), fresh}},
		{`insert into stripe_refunds(site_id,charge_key,amount_micros,currency,refunded_at) values($1,sha256('a'),1,'EUR',$2),($1,sha256('b'),1,'EUR',$3)`,
			[]any{f.site.ID, f.now.Add(-RefundRetention - time.Hour), fresh}},
	} {
		if _, err := f.pool.Exec(ctx, q.sql, q.args...); err != nil {
			t.Fatal(err)
		}
	}
	if err := f.svc.Retention(ctx); err != nil {
		t.Fatal(err)
	}
	for _, table := range []string{"stripe_webhook_events", "stripe_customers", "stripe_refunds"} {
		var rows int
		if err := f.pool.QueryRow(ctx, `select count(*) from `+table).Scan(&rows); err != nil || rows != 1 {
			t.Fatalf("%s: %d rows (%v)", table, rows, err)
		}
	}
}

// TestWebhookRevenueReachesEventsTable runs a purchase through the real
// writer, so the stored event carries revenue and attribution.
func TestWebhookRevenueReachesEventsTable(t *testing.T) {
	f := newServiceFixture(t)
	ctx := context.Background()
	if err := db.EnsurePartitions(ctx, f.pool, f.now.AddDate(0, 0, -1), f.now.AddDate(0, 0, 1)); err != nil {
		t.Fatal(err)
	}
	sessions := session.NewMemory(4, nil)
	w := writer.New(f.pool, sessions, 32, 10*time.Millisecond, time.Hour)
	w.SetTrackingPolicy(privacy.Identified, f.registry)
	runCtx, cancel := context.WithCancel(ctx)
	stopped := make(chan error, 1)
	go func() { stopped <- w.Run(runCtx) }()
	f.svc.sink = WriterSink{Writer: w}
	endpoint := f.connect(t)

	source := f.dictID(t, dict.Source, "newsletter")
	token := f.svc.tokens.Seal(Attribution{SiteID: f.site.ID, SessionID: 77, VisitorID: 88, Source: source, IssuedAt: f.now})
	if code := f.deliver(t, endpoint, stripeEvent("evt_real", "checkout.session.completed", f.now, map[string]any{
		"id": "cs_real", "object": "checkout.session", "payment_status": "paid", "amount_total": 1234, "currency": "eur", "client_reference_id": token,
		"metadata": map[string]any{"supermetry_product": "Starter"},
	})); code != http.StatusOK {
		t.Fatalf("delivery: %d", code)
	}
	cancel()
	if err := <-stopped; err != nil && !errors.Is(err, context.Canceled) {
		t.Fatal(err)
	}
	var micros int64
	var currency, name, product string
	var sourceID int32
	var sessionID int64
	if err := f.pool.QueryRow(ctx, `select e.revenue_micros,e.revenue_currency,d.value,e.props->>'product',e.source_id,e.session_id
		from events e join dict d on d.kind=$1 and d.id=e.name_id where e.site_id=$2`, dict.EventName, f.site.ID).
		Scan(&micros, &currency, &name, &product, &sourceID, &sessionID); err != nil {
		t.Fatal(err)
	}
	if micros != 12_340_000 || currency != "EUR" || name != EventPurchase || product != "Starter" || sourceID != source || sessionID != 77 {
		t.Fatalf("stored event micros=%d currency=%s name=%s product=%s source=%d session=%d", micros, currency, name, product, sourceID, sessionID)
	}
}
