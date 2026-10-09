package privacy_test

import (
	"panel/internal/privacy"
	"testing"
)

func TestAnonymousURL(t *testing.T) {
	t.Parallel()
	for _, tt := range []struct{ name, raw, want string }{
		{"absolute", "https://person:secret@example.test/checkout?customer=123#user", "https://example.test/checkout"},
		{"relative", "/checkout?email=private#fragment", "/checkout"},
		{"already clean", "https://example.test/a%20b", "https://example.test/a%20b"},
		{"invalid", "https://[bad", ""},
		{"opaque", "mailto:person@example.test", ""},
		{"empty", "", ""},
	} {
		t.Run(tt.name, func(t *testing.T) {
			if got := privacy.AnonymousURL(tt.raw); got != tt.want {
				t.Fatalf("got %q, want %q", got, tt.want)
			}
		})
	}
}
