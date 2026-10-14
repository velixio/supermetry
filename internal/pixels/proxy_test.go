package pixels

import "testing"

func TestDetectProxy(t *testing.T) {
	cases := map[string]string{
		"github-camo (876de43e)": "camo",
		"Mozilla/5.0 (Windows NT 5.1; rv:11.0) Gecko Firefox/11.0 (via ggpht.com GoogleImageProxy)": "google",
		"Mozilla/5.0 (X11; Linux x86_64) Chrome/124.0":                                              "", "": "", "curl/8.5.0": "",
	}
	for ua, want := range cases {
		got, ok := DetectProxy(ua)
		if got != want || ok != (want != "") {
			t.Errorf("DetectProxy(%q) = %q,%v want %q", ua, got, ok, want)
		}
	}
}

func TestProxyLimiterIsPerPixelAndProxy(t *testing.T) {
	l := NewProxyLimiter()
	allowed := 0
	for range 500 {
		if l.Allow(1, "camo") {
			allowed++
		}
	}
	if allowed < 200 || allowed > 210 {
		t.Fatalf("burst allowed %d, want about 200", allowed)
	}
	if !l.Allow(2, "camo") || !l.Allow(1, "google") {
		t.Fatal("one busy pixel starved another key")
	}
}

func TestProxyLimiterEvictionCreatesFreshIndependentBucket(t *testing.T) {
	l := NewProxyLimiter()
	for range 200 {
		if !l.Allow(1, "camo") {
			t.Fatal("initial bucket exhausted before its configured burst")
		}
	}
	if l.Allow(1, "camo") {
		t.Fatal("saturated bucket unexpectedly allowed another request")
	}
	for key := int64(2); key <= 10_002; key++ {
		if !l.Allow(key, "camo") {
			t.Fatalf("new counter %d shared a saturated overflow bucket", key)
		}
	}
	if !l.Allow(1, "camo") {
		t.Fatal("evicted counter did not receive a fresh bucket")
	}
}
