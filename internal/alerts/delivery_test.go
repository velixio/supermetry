package alerts

import (
	"context"
	"crypto/hmac"
	"crypto/sha256"
	"encoding/hex"
	"encoding/json"
	"errors"
	"io"
	"net"
	"net/http"
	"net/http/httptest"
	"net/netip"
	"strings"
	"testing"
	"time"

	"panel/internal/platform/mail"
)

func TestDispatcherSendsEmail(t *testing.T) {
	sender := &mail.Fake{}
	dispatcher := NewDispatcher(sender, nil)
	notification := testNotification()

	if err := dispatcher.Deliver(context.Background(), DeliveryChannel{Kind: ChannelEmail, Recipients: []string{"a@example.test", "b@example.test"}}, notification); err != nil {
		t.Fatal(err)
	}
	if len(sender.Sent) != 2 {
		t.Fatalf("sent = %d, want 2", len(sender.Sent))
	}
	if sender.Sent[0].To != "a@example.test" || !strings.Contains(sender.Sent[0].Text, "120") {
		t.Fatalf("message = %+v", sender.Sent[0])
	}
}

func TestWebhookUsesExactBodyAndSignature(t *testing.T) {
	var body []byte
	var signature string
	server := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, request *http.Request) {
		body, _ = io.ReadAll(request.Body)
		signature = request.Header.Get("X-Panel-Signature")
		w.WriteHeader(http.StatusNoContent)
	}))
	defer server.Close()
	client := NewWebhookClient(WebhookOptions{AllowPrivate: true})
	notification := testNotification()

	if err := client.Send(context.Background(), ChannelWebhook, server.URL, "secret", notification); err != nil {
		t.Fatal(err)
	}
	var payload map[string]any
	if err := json.Unmarshal(body, &payload); err != nil {
		t.Fatal(err)
	}
	if len(payload) != 5 || payload["alert"] != "traffic_spike" || payload["site"] != float64(9) || payload["value"] != float64(120) || payload["threshold"] != float64(100) || payload["fired_at"] != "2026-10-08T12:00:00Z" {
		t.Fatalf("payload = %s", body)
	}
	mac := hmac.New(sha256.New, []byte("secret"))
	_, _ = mac.Write(body)
	want := "sha256=" + hex.EncodeToString(mac.Sum(nil))
	if signature != want {
		t.Fatalf("signature = %q, want %q", signature, want)
	}
}

func TestWebhookSlackAndDiscordBodies(t *testing.T) {
	tests := []struct {
		name string
		kind ChannelKind
		key  string
	}{
		{name: "slack", kind: ChannelSlack, key: "text"},
		{name: "discord", kind: ChannelDiscord, key: "content"},
	}
	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			var body map[string]any
			server := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, request *http.Request) {
				if err := json.NewDecoder(request.Body).Decode(&body); err != nil {
					t.Error(err)
				}
				w.WriteHeader(http.StatusNoContent)
			}))
			defer server.Close()
			client := NewWebhookClient(WebhookOptions{AllowPrivate: true})
			if err := client.Send(context.Background(), tt.kind, server.URL, "", testNotification()); err != nil {
				t.Fatal(err)
			}
			if len(body) != 1 || !strings.Contains(body[tt.key].(string), "120") {
				t.Fatalf("body = %+v", body)
			}
		})
	}
}

func TestWebhookRejectsPrivateOrMixedDNSAnswersBeforeDial(t *testing.T) {
	tests := []struct {
		name  string
		addrs []netip.Addr
	}{
		{name: "private", addrs: []netip.Addr{netip.MustParseAddr("10.0.0.1")}},
		{name: "loopback", addrs: []netip.Addr{netip.MustParseAddr("127.0.0.1")}},
		{name: "link local", addrs: []netip.Addr{netip.MustParseAddr("169.254.1.1")}},
		{name: "unspecified", addrs: []netip.Addr{netip.MustParseAddr("0.0.0.0")}},
		{name: "multicast", addrs: []netip.Addr{netip.MustParseAddr("224.0.0.1")}},
		{name: "metadata", addrs: []netip.Addr{netip.MustParseAddr("169.254.169.254")}},
		{name: "cgnat", addrs: []netip.Addr{netip.MustParseAddr("100.64.0.1")}},
		{name: "this network", addrs: []netip.Addr{netip.MustParseAddr("0.1.2.3")}},
		{name: "benchmarking", addrs: []netip.Addr{netip.MustParseAddr("198.18.0.1")}},
		{name: "reserved", addrs: []netip.Addr{netip.MustParseAddr("240.0.0.1")}},
		{name: "broadcast", addrs: []netip.Addr{netip.MustParseAddr("255.255.255.255")}},
		{name: "ipv6 unspecified", addrs: []netip.Addr{netip.MustParseAddr("::")}},
		{name: "ipv6 loopback", addrs: []netip.Addr{netip.MustParseAddr("::1")}},
		{name: "ipv6 unique local", addrs: []netip.Addr{netip.MustParseAddr("fd00::1")}},
		{name: "ipv6 link local", addrs: []netip.Addr{netip.MustParseAddr("fe80::1")}},
		{name: "ipv4 mapped loopback", addrs: []netip.Addr{netip.MustParseAddr("::ffff:127.0.0.1")}},
		{name: "ipv4 mapped metadata", addrs: []netip.Addr{netip.MustParseAddr("::ffff:169.254.169.254")}},
		{name: "nat64 private", addrs: []netip.Addr{netip.MustParseAddr("64:ff9b::10.0.0.1")}},
		{name: "nat64 public", addrs: []netip.Addr{netip.MustParseAddr("64:ff9b::5db8:d822")}},
		{name: "mixed rebinding", addrs: []netip.Addr{netip.MustParseAddr("93.184.216.34"), netip.MustParseAddr("10.0.0.1")}},
	}
	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			dialed := false
			client := NewWebhookClient(WebhookOptions{
				Resolver: fakeResolver{addrs: tt.addrs},
				DialContext: func(context.Context, string, string) (net.Conn, error) {
					dialed = true
					return nil, errors.New("unexpected dial")
				},
			})
			err := client.Send(context.Background(), ChannelWebhook, "http://example.test/hook", "", testNotification())
			if err == nil || dialed {
				t.Fatalf("Send() error=%v dialed=%v, want rejection before dial", err, dialed)
			}
		})
	}
}

func TestWebhookRetriesTwiceAndReportsSanitizedFailure(t *testing.T) {
	attempts := 0
	server := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, _ *http.Request) {
		attempts++
		w.WriteHeader(http.StatusBadGateway)
	}))
	defer server.Close()
	var failures []string
	client := NewWebhookClient(WebhookOptions{
		AllowPrivate: true,
		Sleep:        func(context.Context, time.Duration) error { return nil },
		OnFailure:    func(err error) { failures = append(failures, err.Error()) },
	})

	err := client.Send(context.Background(), ChannelWebhook, server.URL+"/secret-path", "", testNotification())
	if err == nil || attempts != 3 {
		t.Fatalf("Send() error=%v attempts=%d, want error after 3 attempts", err, attempts)
	}
	if len(failures) != 1 || strings.Contains(failures[0], "secret-path") || strings.Contains(err.Error(), "secret-path") {
		t.Fatalf("failure leaked URL: callback=%v error=%v", failures, err)
	}
}

func TestWebhookDoesNotFollowRedirects(t *testing.T) {
	targetCalls := 0
	target := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, _ *http.Request) {
		targetCalls++
		w.WriteHeader(http.StatusNoContent)
	}))
	defer target.Close()
	redirect := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, request *http.Request) {
		http.Redirect(w, request, target.URL, http.StatusFound)
	}))
	defer redirect.Close()
	client := NewWebhookClient(WebhookOptions{AllowPrivate: true, Sleep: func(context.Context, time.Duration) error { return nil }})

	if err := client.Send(context.Background(), ChannelWebhook, redirect.URL, "", testNotification()); err == nil {
		t.Fatal("Send() error = nil, want redirect rejection")
	}
	if targetCalls != 0 {
		t.Fatalf("redirect target calls = %d, want 0", targetCalls)
	}
}

type fakeResolver struct{ addrs []netip.Addr }

func (r fakeResolver) LookupNetIP(context.Context, string, string) ([]netip.Addr, error) {
	return append([]netip.Addr(nil), r.addrs...), nil
}

func testNotification() Notification {
	return Notification{
		AlertID: 3, SiteID: 9, Kind: AlertTrafficSpike, Value: 120, Threshold: 100,
		From:    time.Date(2026, 10, 8, 11, 0, 0, 0, time.UTC),
		To:      time.Date(2026, 10, 8, 12, 0, 0, 0, time.UTC),
		FiredAt: time.Date(2026, 10, 8, 12, 0, 0, 0, time.UTC),
	}
}

func TestBlockedAddressAllowsPublicDestinations(t *testing.T) {
	for _, raw := range []string{"93.184.216.34", "2606:2800:220:1::1", "::ffff:93.184.216.34"} {
		if blockedAddress(netip.MustParseAddr(raw)) {
			t.Errorf("blockedAddress(%s) = true, want false", raw)
		}
	}
}
