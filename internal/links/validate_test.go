package links

import (
	"errors"
	"strings"
	"testing"
)

func TestNormalizeDestination(t *testing.T) {
	t.Parallel()
	long := "https://supermetry.com/" + strings.Repeat("a", MaxDestinationLength)
	tests := []struct {
		name, in, self, want string
		ok                   bool
	}{
		{"https", "https://supermetry.com/a?b=1", "", "https://supermetry.com/a?b=1", true},
		{"upper scheme lowercased", "HTTP://supermetry.com", "", "http://supermetry.com", true},
		{"trims", "  https://supermetry.com  ", "", "https://supermetry.com", true},
		{"javascript", "javascript:alert(1)", "", "", false},
		{"JaVaScRiPt mixed", "JaVaScRiPt:alert(1)", "", "", false},
		{"data", "data:text/html,<script>alert(1)</script>", "", "", false},
		{"file", "file:///etc/passwd", "", "", false},
		{"ftp", "ftp://supermetry.com/x", "", "", false},
		{"protocol relative", "//evil.example/x", "", "", false},
		{"relative", "/admin", "", "", false},
		{"no host", "https:///path", "", "", false},
		{"opaque", "https:supermetry.com", "", "", false},
		{"credentials", "https://user:pass@example.com", "", "", false},
		{"user only", "https://user@example.com", "", "", false},
		{"space inside", "https://supermetry.com/a b", "", "", false},
		{"newline injection", "https://supermetry.com/\r\nSet-Cookie: x=1", "", "", false},
		{"tab", "https://exa\tmple.com", "", "", false},
		{"backslash host", `https://supermetry.com\@evil.example`, "", "", false},
		{"too long", long, "", "", false},
		{"empty", "  ", "", "", false},
		{"self loop", "https://sm.example.test/l/abc", "sm.example.test", "", false},
		{"self pixel", "https://SM.example.test/px/x.gif", "sm.example.test", "", false},
		{"self other path ok", "https://sm.example.test/app", "sm.example.test", "https://sm.example.test/app", true},
	}
	for _, test := range tests {
		t.Run(test.name, func(t *testing.T) {
			t.Parallel()
			got, err := NormalizeDestination(test.in, test.self)
			if test.ok {
				if err != nil || got != test.want {
					t.Fatalf("got %q, %v; want %q", got, err, test.want)
				}
				return
			}
			if !errors.Is(err, ErrInvalid) {
				t.Fatalf("got %q, %v; want ErrInvalid", got, err)
			}
		})
	}
}

func TestNormalizeSlug(t *testing.T) {
	t.Parallel()
	for _, good := range []string{"ab", "spring-sale", "A1_b2", "x" + strings.Repeat("y", 63)} {
		if _, err := NormalizeSlug(good); err != nil {
			t.Errorf("NormalizeSlug(%q) = %v", good, err)
		}
	}
	if got, _ := NormalizeSlug(" Spring-Sale "); got != "spring-sale" {
		t.Errorf("not lowercased/trimmed: %q", got)
	}
	for _, bad := range []string{"", "a", "-ab", "_ab", "a b", "a/b", "a.b", "ä-link", "x" + strings.Repeat("y", 64), "api", "ADMIN", "login", "px", "../x", "a%2fb"} {
		if _, err := NormalizeSlug(bad); !errors.Is(err, ErrInvalid) {
			t.Errorf("NormalizeSlug(%q) accepted", bad)
		}
	}
	seen := map[string]bool{}
	for range 50 {
		slug := RandomSlug()
		if _, err := NormalizeSlug(slug); err != nil {
			t.Fatalf("random slug %q invalid: %v", slug, err)
		}
		seen[slug] = true
	}
	if len(seen) < 45 {
		t.Fatalf("random slugs collide too often: %d unique of 50", len(seen))
	}
}

func TestUTMApply(t *testing.T) {
	t.Parallel()
	utm := UTM{Source: "news letter", Medium: "email", Campaign: "spring&sale"}
	tests := []struct{ in, want string }{
		{"https://supermetry.com", "https://supermetry.com?utm_source=news+letter&utm_medium=email&utm_campaign=spring%26sale"},
		{"https://supermetry.com/p?x=1", "https://supermetry.com/p?x=1&utm_source=news+letter&utm_medium=email&utm_campaign=spring%26sale"},
		{"https://supermetry.com/p?utm_source=keep", "https://supermetry.com/p?utm_source=keep&utm_medium=email&utm_campaign=spring%26sale"},
		{"https://supermetry.com/p#frag", "https://supermetry.com/p?utm_source=news+letter&utm_medium=email&utm_campaign=spring%26sale#frag"},
	}
	for _, test := range tests {
		if got := utm.Apply(test.in); got != test.want {
			t.Errorf("Apply(%q) = %q, want %q", test.in, got, test.want)
		}
	}
	if got := (UTM{}).Apply("https://supermetry.com/a?b=1"); got != "https://supermetry.com/a?b=1" {
		t.Errorf("empty UTM changed destination: %q", got)
	}
}

func TestNormalizeCampaign(t *testing.T) {
	t.Parallel()
	for in, want := range map[string]string{
		"spring-2026": "spring-2026", " a_b.c:d ": "a_b.c:d", "": "", "<script>": "", "a b": "",
		strings.Repeat("x", 65): "", "-lead": "", "ok\r\nx": "",
	} {
		if got := NormalizeCampaign(in); got != want {
			t.Errorf("NormalizeCampaign(%q) = %q, want %q", in, got, want)
		}
	}
}
