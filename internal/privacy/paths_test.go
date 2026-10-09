package privacy

import (
	"strings"
	"sync"
	"testing"
)

func TestPathPolicyExactPublicPaths(t *testing.T) {
	p, err := NewPathPolicy([]string{"/", "/public", "/docs/", "/日本語"})
	if err != nil {
		t.Fatal(err)
	}
	for _, tc := range []struct{ input, want string }{
		{"https://person:secret@example.com/public?email=secret#secret", "/public"},
		{"https://person:sec%72et@example.com/public?q=%20", "/public"},
		{"/public?identity=secret#private", "/public"},
		{"https://example.com", "/"},
		{"https://example.com/docs/", "/docs/"},
		{"https://example.com/日本語", "/日本語"},
		{"https://example.com/%E6%97%A5%E6%9C%AC%E8%AA%9E", RedactedPath},
		{"/docs", RedactedPath}, {"/public/customer", RedactedPath},
		{"/PUBLIC", RedactedPath}, {"/private/secret", RedactedPath},
		{"https://example.com/%70ublic", RedactedPath},
		{"/x/../public", RedactedPath}, {"//example.com//public", RedactedPath},
		{"mailto:person@example.com", RedactedPath}, {"public", RedactedPath},
		{"https:/public", RedactedPath}, {"https://example.com/%zz", RedactedPath},
		{"", RedactedPath},
	} {
		t.Run(tc.input, func(t *testing.T) {
			got := p.SanitizePath(tc.input)
			if got.Path != tc.want || got.Version != PathSanitizerVersion || got.Policy != p.Fingerprint() {
				t.Fatalf("got %+v want path %q", got, tc.want)
			}
		})
	}
}

func TestPathPolicyConfigurationValidation(t *testing.T) {
	for _, input := range []string{"", "relative", "https://example.com/public", "//host/path", "/x?query", "/x#fragment", "/%70ublic", "/users/*", "/x/../public", "/./public", "/a//b", "/back\\slash", "/space here", "/line\nfeed", "/\u202eprivate", "/" + strings.Repeat("a", 512), "/\xff"} {
		t.Run(input, func(t *testing.T) {
			if _, err := NewPathPolicy([]string{input}); err == nil {
				t.Fatalf("accepted %q", input)
			}
		})
	}
	if _, err := NewPathPolicy([]string{"/" + strings.Repeat("a", 511)}); err != nil {
		t.Fatal(err)
	}
	if _, err := NewPathPolicy([]string{"/" + strings.Repeat("日", 171)}); err == nil {
		t.Fatal("accepted path exceeding byte limit with multibyte characters")
	}
	if _, err := NewPathPolicy([]string{"/" + strings.Repeat("日", 170)}); err != nil {
		t.Fatal(err)
	}
	if _, err := NewPathPolicy(make([]string, 129)); err == nil {
		t.Fatal("accepted over 128 paths")
	}
}

func TestPathPolicyZeroAndDeterministicFingerprint(t *testing.T) {
	var zero PathPolicy
	empty, err := NewPathPolicy(nil)
	if err != nil {
		t.Fatal(err)
	}
	if len(zero.Fingerprint()) != 64 || zero.Fingerprint() != empty.Fingerprint() {
		t.Fatal("invalid zero policy fingerprint")
	}
	if got := zero.SanitizePath("https://example.com/public"); got.Path != RedactedPath || got.Version != 1 || got.Policy != empty.Fingerprint() {
		t.Fatalf("zero policy: %+v", got)
	}
	a, _ := NewPathPolicy([]string{"/a", "/b"})
	b, _ := NewPathPolicy([]string{"/b", "/a", "/a"})
	c, _ := NewPathPolicy([]string{"/a"})
	if a.Fingerprint() != b.Fingerprint() || a.Fingerprint() == c.Fingerprint() || a.Fingerprint() == zero.Fingerprint() {
		t.Fatal("fingerprint not deterministic or policy-specific")
	}
}

func TestPathPolicyImmutableAndRevalidatesOutput(t *testing.T) {
	input := []string{"/public"}
	p, _ := NewPathPolicy(input)
	input[0] = "/private"
	if p.PublicPath("/public") != "/public" || p.PublicPath("/private") != RedactedPath || p.PublicPath("/public?secret") != RedactedPath {
		t.Fatal("policy mutated or accepted unapproved label")
	}
	var removed PathPolicy
	if removed.PublicPath(p.SanitizePath("/public").Path) != RedactedPath || removed.PublicPath(RedactedPath) != RedactedPath {
		t.Fatal("removed label survived current policy")
	}
	for _, policy := range []PathPolicy{p, removed} {
		if got := policy.SanitizePath(RedactedPath); got.Path != RedactedPath || got.Version != PathSanitizerVersion || got.Policy != policy.Fingerprint() {
			t.Fatalf("fallback not idempotent: %+v", got)
		}
	}
	var wg sync.WaitGroup
	for range 8 {
		wg.Go(func() {
			for range 100 {
				if p.SanitizePath("/public").Path != "/public" {
					t.Error("concurrent policy changed")
				}
			}
		})
	}
	wg.Wait()
}

func FuzzPathPolicyNeverEmitsUndeclaredPath(f *testing.F) {
	p, err := NewPathPolicy([]string{"/public", "/docs/"})
	if err != nil {
		f.Fatal(err)
	}
	for _, seed := range []string{"https://name:secret@host/public?q=secret#secret", "/secret", "/%70ublic", "/docs/", "//host/public", RedactedPath} {
		f.Add(seed)
	}
	f.Fuzz(func(t *testing.T, raw string) {
		got := p.SanitizePath(raw)
		if got.Path != "/public" && got.Path != "/docs/" && got.Path != RedactedPath {
			t.Fatalf("undeclared path emitted: %+v", got)
		}
		if got.Version != PathSanitizerVersion || got.Policy != p.Fingerprint() {
			t.Fatalf("invalid evidence: %+v", got)
		}
		if again := p.SanitizePath(got.Path); again != got {
			t.Fatalf("not idempotent: %+v -> %+v", got, again)
		}
	})
}
