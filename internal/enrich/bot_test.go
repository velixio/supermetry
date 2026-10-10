package enrich_test

import (
	"net/netip"
	"os"
	"path/filepath"
	"testing"

	"github.com/maxmind/mmdbwriter/mmdbtype"

	"supermetry/internal/enrich"
)

func TestBotCustomSpamReferrerMatchesLabelBoundaries(t *testing.T) {
	e, err := enrich.New("")
	if err != nil {
		t.Fatal(err)
	}
	t.Cleanup(func() { _ = e.Close() })
	ip := netip.MustParseAddr("192.0.2.42")
	tests := []struct {
		name    string
		ref     string
		blocked []string
		want    enrich.BotReason
	}{
		{name: "exact", ref: "https://evil.com/path", want: enrich.BotSpamReferrer},
		{name: "subdomain", ref: "https://a.evil.com/path", want: enrich.BotSpamReferrer},
		{name: "not a label suffix", ref: "https://notevil.com/path", want: enrich.BotNone},
		{name: "www is a real label", ref: "https://www.evil.com/path", blocked: []string{"www.evil.com"}, want: enrich.BotSpamReferrer},
		{name: "invalid URL", ref: "://evil.com", want: enrich.BotNone},
	}
	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			blocked := tt.blocked
			if blocked == nil {
				blocked = []string{"evil.com"}
			}
			if got := e.BotWithBlockedReferrers(desktopUserAgent(), ip, tt.ref, blocked); got != tt.want {
				t.Fatalf("BotWithBlockedReferrers() = %v, want %v", got, tt.want)
			}
		})
	}
}

func TestMergeReferrerSpamFile(t *testing.T) {
	e, err := enrich.New("")
	if err != nil {
		t.Fatal(err)
	}
	t.Cleanup(func() { _ = e.Close() })
	path := filepath.Join(t.TempDir(), "spam.txt")
	if err := os.WriteFile(path, []byte("# local additions\noperator-spam.example\n\nSECOND.example # inline comments are invalid\n"), 0o600); err != nil {
		t.Fatal(err)
	}
	if err := e.MergeReferrerSpamFile(path); err == nil {
		t.Fatal("invalid operator line accepted")
	}
	if got := e.Bot(desktopUserAgent(), netip.MustParseAddr("192.0.2.42"), "https://operator-spam.example/"); got != enrich.BotNone {
		t.Fatalf("partial invalid file was merged: %v", got)
	}
	if err := os.WriteFile(path, []byte("# local additions\noperator-spam.example\nSECOND.example\n"), 0o600); err != nil {
		t.Fatal(err)
	}
	if err := e.MergeReferrerSpamFile(path); err != nil {
		t.Fatal(err)
	}
	for _, ref := range []string{"https://operator-spam.example/", "https://a.second.example/path"} {
		if got := e.Bot(desktopUserAgent(), netip.MustParseAddr("192.0.2.42"), ref); got != enrich.BotSpamReferrer {
			t.Fatalf("Bot(%q) = %v, want spam", ref, got)
		}
	}
}

func TestMergeReferrerSpamFileRejectsOversizeFile(t *testing.T) {
	e, err := enrich.New("")
	if err != nil {
		t.Fatal(err)
	}
	t.Cleanup(func() { _ = e.Close() })
	path := filepath.Join(t.TempDir(), "spam.txt")
	if err := os.WriteFile(path, make([]byte, enrich.MaxReferrerSpamFileBytes+1), 0o600); err != nil {
		t.Fatal(err)
	}
	if err := e.MergeReferrerSpamFile(path); err == nil {
		t.Fatal("oversize operator file accepted")
	}
}

func TestBotHostingOrganizationIsNotABot(t *testing.T) {
	tests := []struct {
		name         string
		organization string
		want         enrich.BotReason
	}{
		{name: "AWS name", organization: "Amazon Web Services", want: enrich.BotNone},
		{name: "Amazon legal name", organization: "Amazon.com, Inc.", want: enrich.BotNone},
		{name: "Google Cloud", organization: "GOOGLE-CLOUD-PLATFORM", want: enrich.BotNone},
		{name: "Microsoft Azure", organization: "MICROSOFT-CORP-MSN-AS-BLOCK", want: enrich.BotNone},
		{name: "DigitalOcean", organization: "DIGITALOCEAN-ASN", want: enrich.BotNone},
		{name: "Hetzner", organization: "Hetzner Online GmbH", want: enrich.BotNone},
		{name: "OVH", organization: "OVH SAS", want: enrich.BotNone},
		{name: "Linode", organization: "Linode, LLC", want: enrich.BotNone},
		{name: "Akamai", organization: "Akamai Connected Cloud", want: enrich.BotNone},
		{name: "Vultr", organization: "The Constant Company, LLC", want: enrich.BotNone},
		{name: "Oracle Cloud", organization: "Oracle Corporation", want: enrich.BotNone},
		{name: "Alibaba", organization: "Alibaba (US) Technology Co., Ltd.", want: enrich.BotNone},
		{name: "Tencent", organization: "Tencent Building, Kejizhongyi Avenue", want: enrich.BotNone},
		{name: "Cloudflare", organization: "Cloudflare, Inc.", want: enrich.BotNone},
		{name: "Scaleway", organization: "Scaleway S.a.s.", want: enrich.BotNone},
		{name: "Contabo", organization: "Contabo GmbH", want: enrich.BotNone},
		{name: "benign ISP", organization: "Example Regional Broadband Cooperative", want: enrich.BotNone},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			asnPath := writeMMDB(t, "GeoLite2-ASN", mmdbtype.Map{
				"autonomous_system_number":       mmdbtype.Uint32(64500),
				"autonomous_system_organization": mmdbtype.String(tt.organization),
			})
			e, err := enrich.NewWithGeo(enrich.GeoPaths{ASN: asnPath})
			if err != nil {
				t.Fatal(err)
			}
			t.Cleanup(func() { _ = e.Close() })
			got := e.Bot(desktopUserAgent(), netip.MustParseAddr("192.0.2.42"), "")
			if got != tt.want {
				t.Errorf("Bot() = %d, want %d for %q", got, tt.want, tt.organization)
			}
		})
	}
}

func TestBotPrecedenceIgnoresHostingOrganization(t *testing.T) {
	asnPath := writeMMDB(t, "GeoLite2-ASN", mmdbtype.Map{
		"autonomous_system_number":       mmdbtype.Uint32(64500),
		"autonomous_system_organization": mmdbtype.String("AMAZON-02"),
	})
	e, err := enrich.NewWithGeo(enrich.GeoPaths{ASN: asnPath})
	if err != nil {
		t.Fatal(err)
	}
	t.Cleanup(func() { _ = e.Close() })
	ip := netip.MustParseAddr("192.0.2.42")

	tests := []struct {
		name string
		ua   string
		ref  string
		want enrich.BotReason
	}{
		{
			name: "headless before crawler and ASN",
			ua:   "Googlebot HeadlessChrome",
			want: enrich.BotHeadless,
		},
		{
			name: "crawler before ASN",
			ua:   "Mozilla/5.0 (compatible; Googlebot/2.1; +http://www.google.com/bot.html)",
			want: enrich.BotUserAgent,
		},
		{
			name: "hosting ASN does not hide a spam referrer verdict",
			ua:   desktopUserAgent(),
			ref:  "https://best-seo-offer.com/",
			want: enrich.BotSpamReferrer,
		},
		{
			name: "VPN or hosting ASN with a normal browser is a visitor",
			ua:   desktopUserAgent(),
			want: enrich.BotNone,
		},
	}
	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			if got := e.Bot(tt.ua, ip, tt.ref); got != tt.want {
				t.Errorf("Bot() = %d, want %d", got, tt.want)
			}
		})
	}
}

func TestInAppBrowsersAreNotBots(t *testing.T) {
	enricher, err := enrich.New("")
	if err != nil {
		t.Fatal(err)
	}
	defer enricher.Close()
	visitors := map[string]string{
		"WhatsApp in-app browser": "Mozilla/5.0 (Linux; Android 13; CPH2381 Build/TP1A.220905.001; wv) AppleWebKit/537.36 (KHTML, like Gecko) Version/4.0 Chrome/129.0.6668.81 Mobile Safari/537.36 WhatsApp/2.24.20.89",
	}
	for name, ua := range visitors {
		if reason := enricher.Bot(ua, netip.MustParseAddr("203.0.113.10"), ""); reason != enrich.BotNone {
			t.Errorf("%s classified as bot (%v)", name, reason)
		}
	}
	if reason := enricher.Bot("WhatsApp/2.24.20.89 A", netip.MustParseAddr("203.0.113.10"), ""); reason != enrich.BotUserAgent {
		t.Errorf("WhatsApp link-preview fetcher = %v, want BotUserAgent", reason)
	}
}

func TestBotRuleReturnsOnlyBoundedFilterMetadata(t *testing.T) {
	enricher, err := enrich.New("")
	if err != nil {
		t.Fatal(err)
	}
	defer enricher.Close()
	tests := []struct {
		name   string
		ua     string
		ref    string
		reason enrich.BotReason
		want   string
	}{
		{name: "crawler rule", ua: "Mozilla/5.0 Googlebot/2.1 private-marker", reason: enrich.BotUserAgent, want: `Googlebot\/`},
		{name: "headless category", ua: "HeadlessChrome private-marker", reason: enrich.BotHeadless, want: "headless"},
		{name: "spam category", ua: desktopUserAgent(), ref: "https://best-seo-offer.com/private-marker", reason: enrich.BotSpamReferrer, want: "spam_referrer"},
	}
	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			got := enricher.BotRule(tt.ua, tt.ref, tt.reason)
			if got != tt.want {
				t.Fatalf("BotRule() = %q, want %q", got, tt.want)
			}
			if got == tt.ua || got == tt.ref {
				t.Fatal("BotRule retained request text")
			}
		})
	}
}
