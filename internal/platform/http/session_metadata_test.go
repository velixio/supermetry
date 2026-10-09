package platformhttp

import (
	"bytes"
	"net"
	"net/http/httptest"
	"net/netip"
	"os"
	"path/filepath"
	"strings"
	"testing"

	"github.com/maxmind/mmdbwriter"
	"github.com/maxmind/mmdbwriter/mmdbtype"
	"supermetry/internal/enrich"
)

func TestAccountSessionMetadataUsesTrustedPeerAndLocalGeoIP(t *testing.T) {
	tree, err := mmdbwriter.New(mmdbwriter.Options{DatabaseType: "GeoLite2-Country", IPVersion: 6, RecordSize: 24, IncludeReservedNetworks: true})
	if err != nil {
		t.Fatal(err)
	}
	for _, prefix := range []string{"198.51.100.0/24", "2001:db8::/32"} {
		_, network, _ := net.ParseCIDR(prefix)
		if err := tree.Insert(network, mmdbtype.Map{"country": mmdbtype.Map{"iso_code": mmdbtype.String("DE")}}); err != nil {
			t.Fatal(err)
		}
	}
	var data bytes.Buffer
	if _, err := tree.WriteTo(&data); err != nil {
		t.Fatal(err)
	}
	path := filepath.Join(t.TempDir(), "country.mmdb")
	if err := os.WriteFile(path, data.Bytes(), 0600); err != nil {
		t.Fatal(err)
	}
	geo, err := enrich.New(path)
	if err != nil {
		t.Fatal(err)
	}
	defer geo.Close()
	for _, tt := range []struct{ name, peer, forwarded, wantIP, wantCountry string }{
		{"direct ignores forged forwarding", "198.51.100.7:443", "8.8.8.8", "198.51.100.7", "DE"},
		{"trusted chain", "10.0.0.1:443", "8.8.8.8, 198.51.100.8, 10.0.0.2", "198.51.100.8", "DE"},
		{"ipv6", "[2001:db8::42]:443", "", "2001:db8::42", "DE"},
		{"mapped ipv4", "[::ffff:198.51.100.7]:443", "", "198.51.100.7", "DE"},
		{"private nearest hop", "10.0.0.1:443", "8.8.8.8, 192.168.1.2", "192.168.1.2", ""},
		{"loopback", "127.0.0.1:443", "198.51.100.7", "127.0.0.1", ""},
		{"ipv6 local", "[fc00::1]:443", "", "fc00::1", ""},
		{"missing geo", "203.0.113.2:443", "", "203.0.113.2", ""},
		{"invalid peer", "broken", "198.51.100.7", "", ""},
		{"invalid trusted hop", "10.0.0.1:443", "198.51.100.7, broken", "", ""},
	} {
		t.Run(tt.name, func(t *testing.T) {
			h := &handler{deps: Deps{TrustedProxies: []netip.Prefix{netip.MustParsePrefix("10.0.0.0/8")}, SessionCountry: geo.Country}}
			r := httptest.NewRequest("POST", "/api/private/auth/login", nil)
			r.RemoteAddr = tt.peer
			r.Header.Set("X-Forwarded-For", tt.forwarded)
			r.Header.Set("User-Agent", "Mozilla/5.0 (Windows NT 10.0; Win64; x64) AppleWebKit/537.36 Chrome/126.0.0.0 Safari/537.36")
			got := h.sessionMetadata(r)
			if got.IPAddress != tt.wantIP || got.CountryCode != tt.wantCountry || got.Browser != "Chrome" {
				t.Fatalf("metadata=%+v", got)
			}
		})
	}
}

func TestAccountSessionBrowserFallbacks(t *testing.T) {
	for _, tt := range []struct{ name, ua, want string }{
		{"firefox", "Mozilla/5.0 (X11; Linux x86_64; rv:131.0) Gecko/20100101 Firefox/131.0", "Firefox"},
		{"safari", "Mozilla/5.0 (iPhone; CPU iPhone OS 18_0 like Mac OS X) AppleWebKit/605.1.15 Version/18.0 Mobile/15E148 Safari/604.1", "Safari"},
		{"missing", "", ""}, {"unknown", "not a browser", ""}, {"bounded", strings.Repeat("x", 1025), ""},
	} {
		t.Run(tt.name, func(t *testing.T) {
			h := &handler{}
			r := httptest.NewRequest("POST", "/api/private/auth/login", nil)
			r.Header.Set("User-Agent", tt.ua)
			got := h.sessionMetadata(r)
			if got.Browser != tt.want || got.CountryCode != "" {
				t.Fatalf("metadata=%+v", got)
			}
		})
	}
}
