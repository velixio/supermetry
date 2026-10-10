package db

import (
	"errors"
	"fmt"
	"net"
	"strings"
	"testing"
)

func TestConnectionHintOnlyForTheBundledHostNotFound(t *testing.T) {
	notFound := fmt.Errorf("dial: %w", &net.DNSError{Err: "no such host", Name: "postgres", IsNotFound: true})
	if hint := connectionHint("postgres", notFound); !strings.Contains(hint, "--profile bundled-db") {
		t.Fatalf("hint = %q, want it to mention the bundled-db profile", hint)
	}
	for name, tc := range map[string]struct {
		host string
		err  error
	}{
		"other host":            {"db.example", notFound},
		"refused, not DNS":      {"postgres", errors.New("connection refused")},
		"DNS timeout, not miss": {"postgres", &net.DNSError{Err: "timeout", IsTimeout: true}},
	} {
		if hint := connectionHint(tc.host, tc.err); hint != "" {
			t.Errorf("%s: hint = %q, want none", name, hint)
		}
	}
}
