package auth

import (
	"context"
	"net/netip"
	"strings"
	"unicode"
	"unicode/utf8"
)

// SessionMetadata is captured once at account sign-in, not from analytics.
// Only the parsed browser name is stored; the raw User-Agent is not retained.
type SessionMetadata struct {
	IPAddress   string
	CountryCode string
	Browser     string
}
type sessionMetadataKey struct{}

func WithSessionMetadata(ctx context.Context, metadata SessionMetadata) context.Context {
	if ip, err := netip.ParseAddr(metadata.IPAddress); err == nil && ip.Zone() == "" {
		metadata.IPAddress = ip.Unmap().String()
	} else {
		metadata.IPAddress = ""
		metadata.CountryCode = ""
	}
	if len(metadata.CountryCode) != 2 || strings.IndexFunc(metadata.CountryCode, func(r rune) bool { return r < 'A' || r > 'Z' }) >= 0 {
		metadata.CountryCode = ""
	}
	if !utf8.ValidString(metadata.Browser) || len(metadata.Browser) > 80 || strings.IndexFunc(metadata.Browser, unicode.IsControl) >= 0 {
		metadata.Browser = ""
	}
	return context.WithValue(ctx, sessionMetadataKey{}, metadata)
}
