package enrich_test

import (
	"net/netip"
	"testing"

	"github.com/maxmind/mmdbwriter/mmdbtype"

	"panel/internal/enrich"
)

func TestBotHostingOrganization(t *testing.T) {
	tests := []struct {
		name         string
		organization string
		want         enrich.BotReason
	}{
		{name: "AWS name", organization: "Amazon Web Services", want: enrich.BotDatacenter},
		{name: "Amazon legal name", organization: "Amazon.com, Inc.", want: enrich.BotDatacenter},
		{name: "Google Cloud", organization: "GOOGLE-CLOUD-PLATFORM", want: enrich.BotDatacenter},
		{name: "Microsoft Azure", organization: "MICROSOFT-CORP-MSN-AS-BLOCK", want: enrich.BotDatacenter},
		{name: "DigitalOcean", organization: "DIGITALOCEAN-ASN", want: enrich.BotDatacenter},
		{name: "Hetzner", organization: "Hetzner Online GmbH", want: enrich.BotDatacenter},
		{name: "OVH", organization: "OVH SAS", want: enrich.BotDatacenter},
		{name: "Linode", organization: "Linode, LLC", want: enrich.BotDatacenter},
		{name: "Akamai", organization: "Akamai Connected Cloud", want: enrich.BotDatacenter},
		{name: "Vultr", organization: "The Constant Company, LLC", want: enrich.BotDatacenter},
		{name: "Oracle Cloud", organization: "Oracle Corporation", want: enrich.BotDatacenter},
		{name: "Alibaba", organization: "Alibaba (US) Technology Co., Ltd.", want: enrich.BotDatacenter},
		{name: "Tencent", organization: "Tencent Building, Kejizhongyi Avenue", want: enrich.BotDatacenter},
		{name: "Cloudflare", organization: "Cloudflare, Inc.", want: enrich.BotDatacenter},
		{name: "Scaleway", organization: "Scaleway S.a.s.", want: enrich.BotDatacenter},
		{name: "Contabo", organization: "Contabo GmbH", want: enrich.BotDatacenter},
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

func TestBotPrecedenceWithHostingOrganization(t *testing.T) {
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
			name: "ASN before spam referrer",
			ua:   desktopUserAgent(),
			ref:  "https://best-seo-offer.com/",
			want: enrich.BotDatacenter,
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
