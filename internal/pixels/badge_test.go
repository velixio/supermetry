package pixels

import (
	"encoding/xml"
	"io"
	"net/url"
	"strings"
	"testing"
)

func TestFormatCount(t *testing.T) {
	cases := map[int64]string{0: "0", 999: "999", 1000: "1k", 1234: "1.2k", 12_345: "12.3k", 999_949: "999.9k", 999_950: "1M", 3_400_000: "3.4M", 1_234_567_890: "1.2B"}
	for n, want := range cases {
		if got := FormatCount(n); got != want {
			t.Errorf("FormatCount(%d) = %q, want %q", n, got, want)
		}
	}
}

func wellFormed(t *testing.T, svg []byte) {
	t.Helper()
	d := xml.NewDecoder(strings.NewReader(string(svg)))
	for {
		if _, err := d.Token(); err != nil {
			if err == io.EOF {
				return
			}
			t.Fatalf("invalid SVG: %v\n%s", err, svg)
		}
	}
}

func TestRenderEscapesLabel(t *testing.T) {
	svg := RenderBadge(BadgeOptions{Label: `<script>&"x"`, Count: 1234, Style: "flat"})
	wellFormed(t, svg)
	if strings.Contains(string(svg), "<script>") || !strings.Contains(string(svg), "1.2k") {
		t.Fatalf("svg = %s", svg)
	}
}

func TestRenderBrandingAndStyle(t *testing.T) {
	branded := string(RenderBadge(BadgeOptions{Label: "views", Count: 5, Style: "flat", Branded: true}))
	plain := string(RenderBadge(BadgeOptions{Label: "views", Count: 5, Style: "flat-square", Color: "ff0000"}))
	if !strings.Contains(branded, `id="sm-mark"`) || strings.Contains(plain, `id="sm-mark"`) {
		t.Fatal("branding mark toggle broken")
	}
	if !strings.Contains(plain, `#ff0000`) || strings.Contains(plain, `rx="3"`) || !strings.Contains(branded, `rx="3"`) {
		t.Fatal("style/color not applied")
	}
	if !strings.Contains(branded, `height="20"`) {
		t.Fatal("badge must be 20px high")
	}
}

func TestParseBadgeQuery(t *testing.T) {
	s, c := ParseBadgeQuery(url.Values{"style": {"flat-square"}, "color": {"0a0B0c"}})
	if s != "flat-square" || c != "0a0b0c" {
		t.Fatalf("%q %q", s, c)
	}
	s, c = ParseBadgeQuery(url.Values{"style": {"plastic"}, "color": {"red;}<"}})
	if s != "flat" || c != "" {
		t.Fatalf("fallback %q %q", s, c)
	}
}
