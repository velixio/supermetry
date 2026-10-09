package enrich_test

import (
	"net/netip"
	"testing"

	"supermetry/internal/enrich"
)

func TestBotFilter(t *testing.T) {
	e, err := enrich.New("")
	if err != nil {
		t.Fatal(err)
	}

	cases := []struct {
		name string
		ua   string
		ip   [4]byte
		ref  string
		want enrich.BotReason
	}{
		{
			name: "known crawler user agent",
			ua:   userAgent("Mozilla/5.0 (compatible; ", "Googlebot/2.1; +http://www.google.com/bot.html)"),
			ip:   [4]byte{66, 249, 66, 1},
			want: enrich.BotReason(1),
		},
		{
			name: "headless browser",
			ua:   userAgent("Mozilla/5.0 (X11; Linux x86_64) AppleWebKit/537.36 ", "(KHTML, like Gecko) HeadlessChrome/120.0 Safari/537.36"),
			ip:   [4]byte{198, 51, 100, 7},
			want: enrich.BotReason(2),
		},
		{
			name: "cloud or VPN address is a visitor",
			ua:   desktopUserAgent(),
			ip:   [4]byte{3, 5, 140, 2},
			want: 0,
		},
		{
			name: "spam referrer",
			ua:   desktopUserAgent(),
			ip:   [4]byte{198, 51, 100, 7},
			ref:  "https://best-seo-offer.com/",
			want: enrich.BotReason(4),
		},
		{
			name: "human visitor",
			ua:   mobileSafariUserAgent(),
			ip:   [4]byte{198, 51, 100, 7},
			ref:  "https://www.google.com/",
			want: 0,
		},
	}

	for _, tc := range cases {
		t.Run(tc.name, func(t *testing.T) {
			got := e.Bot(tc.ua, netip.AddrFrom4(tc.ip), tc.ref)
			if got != tc.want {
				t.Errorf("Bot() = %d, want %d", got, tc.want)
			}
		})
	}
}

func TestSourceAndClient(t *testing.T) {
	e, err := enrich.New("")
	if err != nil {
		t.Fatal(err)
	}

	t.Run("organic search", func(t *testing.T) {
		source, medium := e.Source("https://www.google.com/search?q=x", "https://a.example/", enrich.UTM{})
		if source != "Google" || medium != "organic" {
			t.Errorf("Source() = %q/%q, want Google/organic", source, medium)
		}
	})

	t.Run("UTM overrides direct traffic", func(t *testing.T) {
		pageURL := "https://a.example/?utm_source=newsletter&utm_medium=email"
		source, medium := e.Source("", pageURL, enrich.ParseUTM(pageURL))
		if source != "newsletter" || medium != "email" {
			t.Errorf("Source() = %q/%q, want newsletter/email", source, medium)
		}
	})

	t.Run("internal referrer", func(t *testing.T) {
		source, _ := e.Source("https://a.example/other", "https://a.example/", enrich.UTM{})
		if source != "" {
			t.Errorf("Source() source = %q, want empty", source)
		}
	})

	t.Run("mobile Safari client", func(t *testing.T) {
		client := e.Client(mobileSafariUserAgent())
		want := enrich.Client{Browser: "Safari", BrowserVersion: "Safari 17", OS: "iOS", OSVersion: "iOS 17", Device: 1}
		if client != want {
			t.Errorf("Client() = %+v, want %+v", client, want)
		}
	})

	t.Run("Chrome 126 on Windows", func(t *testing.T) {
		client := e.Client(userAgent(
			"Mozilla/5.0 (Windows NT 10.0; Win64; x64) ",
			"AppleWebKit/537.36 Chrome/126.0.6478.127 Safari/537.36",
		))
		want := enrich.Client{
			Browser: "Chrome", BrowserVersion: "Chrome 126",
			OS: "Windows", OSVersion: "Windows 10/11",
		}
		if client != want {
			t.Errorf("Client() = %+v, want %+v", client, want)
		}
	})
}

func userAgent(parts ...string) string {
	var ua string
	for _, part := range parts {
		ua += part
	}
	return ua
}

func desktopUserAgent() string {
	return userAgent(
		"Mozilla/5.0 (Windows NT 10.0; Win64; x64) ",
		"AppleWebKit/537.36 Chrome/120.0 Safari/537.36",
	)
}

func mobileSafariUserAgent() string {
	return userAgent(
		"Mozilla/5.0 (iPhone; CPU iPhone OS 17_0 like Mac OS X) ",
		"AppleWebKit/605.1.15 Version/17.0 Mobile/15E148 Safari/604.1",
	)
}
