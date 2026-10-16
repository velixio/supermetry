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

func TestStatsBadgeShowsTotalTodayAndSparkline(t *testing.T) {
	series := make([]int64, RecentDays)
	series[RecentDays-1], series[RecentDays-2] = 5, 10
	svg := RenderStatsBadge(StatsBadgeOptions{Label: "views", Total: 1234, Today: 5, Series: series, Style: "flat", Branded: true})
	wellFormed(t, svg)
	for _, want := range []string{"1.2k", "today 5", "<polyline", "sm-mark"} {
		if !strings.Contains(string(svg), want) {
			t.Errorf("badge lacks %q: %s", want, svg)
		}
	}
	if got := sparkPoints([]int64{0, 10, 5}); got != "0,12 30,0 60,6" {
		t.Errorf("sparkPoints = %q", got)
	}
	if sparkPoints([]int64{3}) != "" {
		t.Error("a single point must not draw a line")
	}
}

func TestStatsBadgePoweredByLinksHomeOnlyWhenBranded(t *testing.T) {
	series := []int64{1, 2, 3}
	branded := RenderStatsBadge(StatsBadgeOptions{Label: "views", Total: 6, Today: 3, Series: series, Style: "flat", Branded: true})
	wellFormed(t, branded)
	for _, want := range []string{"powered by Supermetry", `href="https://supermetry.com"`} {
		if !strings.Contains(string(branded), want) {
			t.Errorf("branded stats badge lacks %q: %s", want, branded)
		}
	}
	plain := string(RenderStatsBadge(StatsBadgeOptions{Label: "views", Total: 6, Today: 3, Series: series, Style: "flat"}))
	if strings.Contains(plain, "Supermetry") || strings.Contains(plain, "href=") {
		t.Errorf("unbranded stats badge mentions Supermetry: %s", plain)
	}
	if len(branded) <= len(plain) {
		t.Error("branded stats badge must be wider than the plain one")
	}
}

func TestSeriesCountsOnlyTheWindowOldestFirst(t *testing.T) {
	c := NewTotalCache()
	c.AddDay(1, "2026-10-30", 2)
	c.AddDay(1, "2026-10-28", 1)
	c.AddDay(1, "2026-09-01", 9)
	c.SeedDays(map[int64]map[string]int64{1: {"2026-10-28": 4, "2026-09-01": 9}}, "2026-10-01")
	got := c.Series(1, "2026-10-30", 3)
	if got[0] != 4 || got[1] != 0 || got[2] != 2 {
		t.Fatalf("series = %v, want [4 0 2]", got)
	}
}
