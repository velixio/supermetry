package stripe

import (
	"encoding/json"
	"strings"
	"testing"
	"time"

	platformcrypto "supermetry/internal/platform/crypto"
)

func testTokens(t *testing.T, key string) *Tokens {
	t.Helper()
	box, err := platformcrypto.NewSecretBox([]byte(key), "supermetry/stripe-attribution/v1")
	if err != nil {
		t.Fatal(err)
	}
	return NewTokens(box)
}

func TestAttributionTokenRoundTrip(t *testing.T) {
	tokens := testTokens(t, "01234567890123456789012345678901")
	issued := time.Date(2026, 11, 3, 10, 0, 0, 0, time.UTC)
	in := Attribution{SiteID: 7, SessionID: 1 << 40, VisitorID: -42, Source: 3, UTMSource: 4, Medium: 5, Campaign: 6, Region: 9, Country: "DE", IssuedAt: issued}
	token := tokens.Seal(in)
	if !strings.HasPrefix(token, TokenPrefix) || len(token) > 200 {
		t.Fatalf("token %q (len %d) does not fit client_reference_id", token, len(token))
	}
	for _, r := range token {
		if !(r >= 'a' && r <= 'z' || r >= 'A' && r <= 'Z' || r >= '0' && r <= '9' || r == '_' || r == '-') {
			t.Fatalf("token has character %q not allowed in client_reference_id", r)
		}
	}
	out, err := tokens.Open(token, 7, issued.Add(TokenTTL))
	if err != nil {
		t.Fatal(err)
	}
	if out != in {
		t.Fatalf("round trip = %+v, want %+v", out, in)
	}
	if token == tokens.Seal(in) {
		t.Fatal("tokens are deterministic; sealing must use a fresh nonce")
	}

	if _, err := tokens.Open(token, 8, issued); err == nil {
		t.Fatal("token for another site accepted")
	}
	if _, err := tokens.Open(token, 7, issued.Add(TokenTTL+time.Second)); err == nil {
		t.Fatal("expired token accepted")
	}
	if _, err := tokens.Open(token, 7, issued.Add(-2*time.Minute)); err == nil {
		t.Fatal("token from the future accepted")
	}
	if _, err := testTokens(t, "abcdefghijabcdefghijabcdefghijab").Open(token, 7, issued); err == nil {
		t.Fatal("token of another instance accepted")
	}
	tampered := []byte(token)
	tampered[20] ^= 1
	if tampered[20] == '_' || tampered[20] == '-' {
		tampered[20] = 'A'
	}
	for _, bad := range []string{string(tampered), "sm1_", "sm1_!!!", "nope", TokenPrefix + strings.Repeat("A", 400)} {
		if _, err := tokens.Open(bad, 7, issued); err == nil {
			t.Fatalf("token %q accepted", bad)
		}
	}
	noCountry := tokens.Seal(Attribution{SiteID: 7, IssuedAt: issued})
	if out, err := tokens.Open(noCountry, 7, issued); err != nil || out.Country != "" {
		t.Fatalf("empty country round trip: %+v %v", out, err)
	}
}

func envelope(t *testing.T, eventType string, livemode bool, object map[string]any) Envelope {
	t.Helper()
	data, err := json.Marshal(map[string]any{
		"id": "evt_" + strings.ReplaceAll(eventType, ".", "_"), "object": "event", "type": eventType, "created": 1793700000,
		"livemode": livemode, "data": map[string]any{"object": object},
	})
	if err != nil {
		t.Fatal(err)
	}
	e, err := ParseEnvelope(data)
	if err != nil {
		t.Fatal(err)
	}
	return e
}

func TestParseEnvelopeRejectsMalformedEvents(t *testing.T) {
	for _, body := range []string{
		``, `[]`, `{"id":"x","type":"invoice.paid"}`, `{"id":"evt_1","type":""}`, `{"id":"evt_1","type":"Invoice Paid"}`,
		`{"id":"evt_` + strings.Repeat("a", 300) + `","type":"invoice.paid"}`,
	} {
		if _, err := ParseEnvelope([]byte(body)); err == nil {
			t.Errorf("accepted %s", body)
		}
	}
}

func TestMapCheckoutSession(t *testing.T) {
	session := map[string]any{
		"id": "cs_live_1", "object": "checkout.session", "mode": "payment", "payment_status": "paid", "amount_total": 4900,
		"currency": "eur", "client_reference_id": "sm1_token", "customer": "cus_1",
		"customer_details": map[string]any{"email": "buyer@example.test", "name": "Jane Buyer", "address": map[string]any{"city": "Berlin"}},
		"metadata":         map[string]any{"supermetry_user_id": "user-42", "supermetry_product": "Pro plan", "email": "x@example.test"},
	}
	action, err := MapEvent(envelope(t, "checkout.session.completed", true, session), MapOptions{})
	if err != nil {
		t.Fatal(err)
	}
	if action.Kind != ActionRevenue || action.Name != EventPurchase || action.AmountMinor != 4900 || action.Currency != "eur" ||
		action.Token != "sm1_token" || action.Customer != "cus_1" || action.Profile != "user-42" || action.Product != "Pro plan" {
		t.Fatalf("action = %+v", action)
	}
	want := map[string]string{"stripe_event_type": "checkout.session.completed", "stripe_mode": "payment", "product": "Pro plan"}
	if len(action.Props) != len(want) {
		t.Fatalf("props = %v", action.Props)
	}
	for key, value := range want {
		if action.Props[key] != value {
			t.Fatalf("props = %v", action.Props)
		}
	}
	encoded, _ := json.Marshal(action)
	for _, pii := range []string{"buyer@example.test", "Jane", "Berlin", "x@example.test"} {
		if strings.Contains(string(encoded), pii) {
			t.Fatalf("action carries personal data %q: %s", pii, encoded)
		}
	}

	session["client_reference_id"] = "order-77"
	session["metadata"] = map[string]any{"supermetry_id": "sm1_meta"}
	action, _ = MapEvent(envelope(t, "checkout.session.completed", true, session), MapOptions{})
	if action.Token != "sm1_meta" {
		t.Fatalf("metadata token not used: %+v", action)
	}

	session["payment_status"] = "unpaid"
	action, _ = MapEvent(envelope(t, "checkout.session.completed", true, session), MapOptions{})
	if action.Kind != ActionCustomer || action.Skip == "" {
		t.Fatalf("unpaid session = %+v", action)
	}
	session["payment_status"] = "paid"
	action, _ = MapEvent(envelope(t, "checkout.session.async_payment_succeeded", true, session), MapOptions{})
	if action.Kind != ActionRevenue || action.Name != EventPurchase {
		t.Fatalf("async success = %+v", action)
	}
	session["payment_status"] = "no_payment_required"
	session["amount_total"] = 0
	action, _ = MapEvent(envelope(t, "checkout.session.completed", true, session), MapOptions{})
	if action.Kind != ActionCustomer {
		t.Fatalf("trial session = %+v", action)
	}
	session["customer"] = nil
	action, _ = MapEvent(envelope(t, "checkout.session.completed", true, session), MapOptions{})
	if action.Kind != ActionIgnore || action.Props != nil {
		t.Fatalf("guest trial session = %+v", action)
	}
}

func TestMapInvoicePaid(t *testing.T) {
	newer := map[string]any{
		"id": "in_1", "object": "invoice", "customer": "cus_1", "amount_paid": 1200, "currency": "usd", "billing_reason": "subscription_cycle",
		"customer_email": "buyer@example.test",
		"parent":         map[string]any{"subscription_details": map[string]any{"subscription": "sub_1", "metadata": map[string]any{"supermetry_user_id": "u1", "supermetry_product": "Team"}}},
	}
	action, err := MapEvent(envelope(t, "invoice.paid", true, newer), MapOptions{})
	if err != nil {
		t.Fatal(err)
	}
	if action.Kind != ActionRevenue || action.Name != EventRenewal || !action.Renewal || action.AmountMinor != 1200 || action.Profile != "u1" ||
		action.Product != "Team" || action.Customer != "cus_1" || action.Props["billing_reason"] != "subscription_cycle" {
		t.Fatalf("action = %+v", action)
	}

	older := map[string]any{
		"id": "in_2", "object": "invoice", "customer": map[string]any{"id": "cus_2", "email": "x@example.test"}, "amount_paid": 990, "currency": "usd",
		"billing_reason": "subscription_update", "subscription": "sub_2",
		"subscription_details": map[string]any{"metadata": map[string]any{"supermetry_user_id": "u2"}},
	}
	action, _ = MapEvent(envelope(t, "invoice.paid", true, older), MapOptions{})
	if action.Kind != ActionRevenue || action.Customer != "cus_2" || action.Profile != "u2" {
		t.Fatalf("older API action = %+v", action)
	}

	for name, change := range map[string]map[string]any{
		"first invoice": {"billing_reason": "subscription_create"},
		"zero amount":   {"amount_paid": 0},
		"one-off":       {"subscription": nil, "subscription_details": nil},
	} {
		object := map[string]any{}
		for k, v := range older {
			object[k] = v
		}
		for k, v := range change {
			object[k] = v
		}
		action, _ := MapEvent(envelope(t, "invoice.paid", true, object), MapOptions{})
		if action.Kind != ActionIgnore || action.Skip == "" {
			t.Errorf("%s: action = %+v", name, action)
		}
	}
}

func TestMapRefundAndPaymentIntent(t *testing.T) {
	charge := map[string]any{"id": "ch_1", "object": "charge", "amount_refunded": 500, "currency": "usd", "customer": "cus_1",
		"billing_details": map[string]any{"email": "x@example.test"}}
	action, _ := MapEvent(envelope(t, "charge.refunded", true, charge), MapOptions{})
	if action.Kind != ActionRefund || action.AmountMinor != 500 || action.ObjectID != "ch_1" {
		t.Fatalf("refund = %+v", action)
	}

	intent := map[string]any{"id": "pi_1", "object": "payment_intent", "amount_received": 700, "currency": "usd",
		"metadata": map[string]any{"supermetry_id": "sm1_x"}}
	action, _ = MapEvent(envelope(t, "payment_intent.succeeded", true, intent), MapOptions{})
	if action.Kind != ActionIgnore {
		t.Fatalf("payment intents are recorded by default: %+v", action)
	}
	action, _ = MapEvent(envelope(t, "payment_intent.succeeded", true, intent), MapOptions{RecordPaymentIntents: true})
	if action.Kind != ActionRevenue || action.Name != EventPayment || action.AmountMinor != 700 || action.Token != "sm1_x" {
		t.Fatalf("opted-in payment intent = %+v", action)
	}
	intent["invoice"] = "in_1"
	action, _ = MapEvent(envelope(t, "payment_intent.succeeded", true, intent), MapOptions{RecordPaymentIntents: true})
	if action.Kind != ActionIgnore {
		t.Fatalf("invoice payment intent double counted: %+v", action)
	}

	action, _ = MapEvent(envelope(t, "customer.created", true, map[string]any{"id": "cus_1", "email": "x@example.test"}), MapOptions{})
	if action.Kind != ActionIgnore {
		t.Fatalf("unknown type = %+v", action)
	}
}

func TestMapEventLimitsMetadata(t *testing.T) {
	object := map[string]any{
		"id": "cs_1", "object": "checkout.session", "payment_status": "paid", "amount_total": 100, "currency": "usd",
		"metadata": map[string]any{"supermetry_user_id": strings.Repeat("u", 129), "supermetry_product": strings.Repeat("p", 101)},
	}
	action, _ := MapEvent(envelope(t, "checkout.session.completed", true, object), MapOptions{})
	if action.Profile != "" || action.Product != "" || action.Props["product"] != "" {
		t.Fatalf("oversized metadata kept: %+v", action)
	}
}
