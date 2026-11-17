package sentryingest

import (
	"crypto/hmac"
	"crypto/sha256"
	"encoding/json"
	"strings"
	"testing"

	"supermetry/internal/appingest"
)

func TestScrubText(t *testing.T) {
	jwt := "eyJhbGciOiJIUzI1NiJ9.eyJzdWIiOiIxMjM0NTYifQ.c2lnbmF0dXJlc2lnbg"
	cases := []struct{ name, in, want, forbidden string }{
		{"email", "failed for jane.doe+x@example.com now", "failed for [email] now", "jane"},
		{"jwt", "token " + jwt + " rejected", "token [token] rejected", "eyJ"},
		{"bearer", "Authorization: Bearer abcDEF123456 sent", "[token]", "abcDEF"},
		{"key value", "password=hunter2 and api_key: abc123", "password=[token] and api_key=[token]", "hunter2"},
		{"github", "ghp_abcdefghijklmnopqrstuvwxyz0123456789 leaked", "[token] leaked", "ghp_"},
		{"stripe", "sk_live_51HabcdefGHIJKL", "[token]", "sk_live"},
		{"aws", "key AKIAABCDEFGHIJKLMNOP end", "key [token] end", "AKIA"},
		{"long hex", "hash 9f86d081884c7d659a2feaa0c55ad015a3bf4f1b2b0b822cd15d6c15b0f00a08", "hash [token]", "9f86d0"},
		{"card", "card 4111 1111 1111 1111 declined", "card [card] declined", "4111"},
		{"card dashes", "4111-1111-1111-1111", "[card]", "4111"},
		{"ipv4", "connect to 192.0.2.44:8080 failed", "connect to [ip]:8080 failed", "192.0.2.44"},
		{"ipv6", "from 2001:db8::ff00:42:8329 refused", "from [ip] refused", "2001:db8"},
		{"uuid", "user 123e4567-e89b-12d3-a456-426614174000 gone", "user [uuid] gone", "123e4567"},
		{"home unix", "open /home/alice/project/main.go failed", "open ~/project/main.go failed", "alice"},
		{"home mac", "open /Users/bob/Library/x failed", "open ~/Library/x failed", "bob"},
		{"home windows", `open C:\Users\carol\app\x.js failed`, `open ~\app\x.js failed`, "carol"},
		{"long digits", "order 1234567 and 12 and 99999", "order [num] and 12 and 99999", "1234567"},
		{"plain", "fixture unhandled exception", "fixture unhandled exception", ""},
		{"version", "node v22.23.3", "node v22.23.3", ""},
		{"time", "at 13:10:17 ok", "at 13:10:17 ok", ""},
	}
	for _, c := range cases {
		t.Run(c.name, func(t *testing.T) {
			got := ScrubText(c.in)
			if c.forbidden != "" && strings.Contains(got, c.forbidden) {
				t.Fatalf("ScrubText(%q) = %q still has %q", c.in, got, c.forbidden)
			}
			if c.name == "bearer" {
				if !strings.Contains(got, c.want) {
					t.Fatalf("got %q want it to contain %q", got, c.want)
				}
				return
			}
			if got != c.want {
				t.Fatalf("ScrubText(%q) = %q, want %q", c.in, got, c.want)
			}
		})
	}
}

func TestScrubTextBoundsInput(t *testing.T) {
	got := ScrubText(strings.Repeat("a ", 200_000))
	if len(got) > 70_000 {
		t.Fatalf("len %d", len(got))
	}
}

func TestHashUser(t *testing.T) {
	secret := []byte("0123456789abcdef0123456789abcdef")
	got := HashUser(secret, "user-1")
	h := hmac.New(sha256.New, secret)
	h.Write([]byte("user-1"))
	if !hmac.Equal(got, h.Sum(nil)) {
		t.Fatal("HashUser must be HMAC-SHA256 of the id, the same MAC the writer uses for install ids")
	}
	if HashUser(secret, "") != nil || HashUser(nil, "user-1") != nil {
		t.Fatal("empty id or secret gives nil")
	}
	if string(HashUser([]byte("other-secret-other-secret-123456"), "user-1")) == string(got) {
		t.Fatal("different secret must give a different hash")
	}
}

func TestURLPath(t *testing.T) {
	cases := map[string]string{
		"https://shop.example.com/cart/1234567?email=a@example.com#frag": "/cart/[num]",
		"/ping?token=abc":                     "/ping",
		"http://127.0.0.1:42335/#breadcrumbs": "/",
		"app:///app.mjs":                      "/app.mjs",
		"":                                    "",
		"node:internal/timers":                "node:internal/timers",
	}
	for in, want := range cases {
		if got := urlPath(in); got != want {
			t.Errorf("urlPath(%q) = %q, want %q", in, got, want)
		}
	}
}

func TestScrubRequest(t *testing.T) {
	r := ScrubRequest(map[string]any{
		"url":          "https://shop.example.com/cart?email=a@example.com",
		"method":       "post",
		"query_string": "email=a@example.com",
		"cookies":      map[string]any{"session": "SECRETCOOKIE"},
		"data":         map[string]any{"password": "hunter2"},
		"env":          map[string]any{"REMOTE_ADDR": "192.0.2.9"},
		"headers": map[string]any{
			"Cookie": "session=SECRETCOOKIE", "Authorization": "Bearer abc", "Proxy-Authorization": "x",
			"X-Api-Key": "k", "User-Agent": "Mozilla/5.0", "Content-Type": "application/json",
			"X-Forwarded-For": "192.0.2.9", "Accept-Language": "en-US",
		},
	})
	raw, _ := json.Marshal(r)
	for _, bad := range []string{"SECRETCOOKIE", "hunter2", "192.0.2", "a@example", "Bearer", "Mozilla", "shop.example", "query", "email="} {
		if strings.Contains(string(raw), bad) {
			t.Errorf("request body %s contains %q", raw, bad)
		}
	}
	if r.Path != "/cart" || r.Method != "POST" || r.Headers["content-type"] != "application/json" || r.Headers["accept-language"] != "en-US" {
		t.Fatalf("unexpected request %+v", r)
	}
	if ScrubRequest(map[string]any{}) != nil || ScrubRequest(nil) != nil {
		t.Fatal("empty request is nil")
	}
}

func TestScrubBreadcrumbsCaps(t *testing.T) {
	var in []any
	for i := 0; i < 250; i++ {
		in = append(in, map[string]any{"category": "ui.click", "message": "click " + itoa(i), "timestamp": float64(1000 + i)})
	}
	out := scrubBreadcrumbs(in)
	if len(out) != 100 {
		t.Fatalf("kept %d, want 100", len(out))
	}
	if out[len(out)-1].Message != "click 249" || out[0].Message != "click 150" {
		t.Fatalf("must keep the newest 100: %q .. %q", out[0].Message, out[len(out)-1].Message)
	}
	var big []any
	for i := 0; i < 100; i++ {
		big = append(big, map[string]any{"category": "console", "message": strings.Repeat("x", 500), "data": map[string]any{"a": strings.Repeat("y", 250), "b": strings.Repeat("z", 250)}})
	}
	out = scrubBreadcrumbs(big)
	raw, _ := json.Marshal(out)
	if len(raw) > 16<<10 {
		t.Fatalf("breadcrumbs %d bytes, cap is 16 KiB", len(raw))
	}
	if len(out) == 0 {
		t.Fatal("kept none")
	}
}

func TestScrubBreadcrumbContent(t *testing.T) {
	out := scrubBreadcrumbs([]any{map[string]any{
		"type": "http", "category": "fetch", "level": "info", "timestamp": "2026-11-17T09:59:00.000Z",
		"message": "GET for a@example.com",
		"data": map[string]any{
			"url": "https://api.example.com/users/1234567?token=abc", "status_code": float64(200),
			"email": "a@example.com", "nested": map[string]any{"x": "y"}, "authorization": "Bearer q",
			"note": "ip 192.0.2.7",
		},
	}})
	raw, _ := json.Marshal(out)
	for _, bad := range []string{"a@example", "token=abc", "192.0.2", "Bearer", "api.example", "nested"} {
		if strings.Contains(string(raw), bad) {
			t.Errorf("breadcrumb %s contains %q", raw, bad)
		}
	}
	if len(out) != 1 || out[0].Data["status_code"] != float64(200) || out[0].Data["url"] != "/users/[num]" || out[0].Timestamp == 0 {
		t.Fatalf("unexpected %+v", out)
	}
	var _ appingest.Breadcrumb = out[0]
}

func TestScrubContexts(t *testing.T) {
	out := scrubContexts(map[string]any{
		"os":     map[string]any{"name": "Android", "version": "14"},
		"device": map[string]any{"name": "Jane's phone", "model": "Pixel", "ip_address": "192.0.2.1", "email": "a@example.com", "arch": "arm64"},
		"custom": map[string]any{"secret": "x"},
		"trace":  map[string]any{"trace_id": "abc", "span_id": "def"},
	})
	raw, _ := json.Marshal(out)
	for _, bad := range []string{"Jane", "192.0.2", "a@example", "custom", "secret"} {
		if strings.Contains(string(raw), bad) {
			t.Errorf("contexts %s contains %q", raw, bad)
		}
	}
	if out["os"]["name"] != "Android" || out["device"]["model"] != "Pixel" || out["trace"]["trace_id"] != "abc" {
		t.Fatalf("unexpected %v", out)
	}
}
