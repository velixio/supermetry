package enrich

import (
	"os"
	"testing"
)

const mmdb = "../../data/dbip-country-lite.mmdb"

func TestUAParse(t *testing.T) {
	r, _ := Open("")
	cases := []struct{ ua, browser, os, device string }{
		{"Mozilla/5.0 (Windows NT 10.0; Win64; x64) AppleWebKit/537.36 (KHTML, like Gecko) Chrome/128.0.0.0 Safari/537.36", "Chrome", "Windows", "desktop"},
		{"Mozilla/5.0 (iPhone; CPU iPhone OS 17_5 like Mac OS X) AppleWebKit/605.1.15 (KHTML, like Gecko) Version/17.5 Mobile/15E148 Safari/604.1", "Safari", "iOS", "mobile"},
		{"Mozilla/5.0 (X11; Linux x86_64; rv:130.0) Gecko/20100101 Firefox/130.0", "Firefox", "Linux", "desktop"},
		{"Mozilla/5.0 (Linux; Android 14; Pixel 8) AppleWebKit/537.36 (KHTML, like Gecko) Chrome/128.0.0.0 Mobile Safari/537.36", "Chrome", "Android", "mobile"},
	}
	for _, c := range cases {
		got := r.Enrich("", c.ua)
		if got.Browser != c.browser || got.OS != c.os || got.Device != c.device || got.Bot {
			t.Errorf("%s => %+v", c.ua[:40], got)
		}
	}
	if !r.Enrich("", "Mozilla/5.0 (compatible; Googlebot/2.1; +http://www.google.com/bot.html)").Bot {
		t.Error("googlebot not flagged")
	}
}

func TestGeoIP(t *testing.T) {
	if _, err := os.Stat(mmdb); err != nil {
		t.Skip("mmdb not downloaded (see NOTES.md)")
	}
	r, err := Open(mmdb)
	if err != nil {
		t.Fatal(err)
	}
	defer r.Close()
	if c := r.Country("8.8.8.8"); c != "US" {
		t.Errorf("8.8.8.8 => %q", c)
	}
	if c := r.Country("10.0.0.1"); c != "" && c != "ZZ" {
		t.Errorf("private ip => %q", c)
	}
}

func BenchmarkEnrich(b *testing.B) {
	r, err := Open(mmdb)
	if err != nil {
		b.Skip(err)
	}
	ua := "Mozilla/5.0 (Linux; Android 14; Pixel 8) AppleWebKit/537.36 (KHTML, like Gecko) Chrome/128.0.0.0 Mobile Safari/537.36"
	b.ReportAllocs()
	for i := 0; b.Loop(); i++ {
		r.Enrich("81.2.69.160", ua)
	}
}
