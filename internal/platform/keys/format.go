package keys

import (
	"crypto/rand"
	"fmt"
	"hash/crc32"
	"net/http"
	"strings"
)

// A current key reads smkapi_<8 char prefix><32 char body><6 char checksum>.
// The fixed prefix lets secret scanners find it, the body is base62, and the
// checksum is the CRC-32 of everything before it in base62 (the GitHub token
// layout), so a mistyped or truncated key is rejected before any database
// lookup. Keys made before this format (pnl_...) keep working. Upload keys
// stay in the legacy shape because Sentry clients demand a 43 character secret.
const (
	currentKeyPrefix = "smkapi_"
	keyBodyLength    = 32
	keyChecksumLen   = 6
	currentKeyLength = len(currentKeyPrefix) + keyPrefixLength + keyBodyLength + keyChecksumLen

	base62Alphabet = "0123456789ABCDEFGHIJKLMNOPQRSTUVWXYZabcdefghijklmnopqrstuvwxyz"
)

func randomBase62(n int) (string, error) {
	out := make([]byte, 0, n)
	buf := make([]byte, n*2)
	for len(out) < n {
		if _, err := rand.Read(buf); err != nil {
			return "", fmt.Errorf("generate API key body: %w", err)
		}
		for _, b := range buf {
			if b < 248 && len(out) < n { // 248 = 4*62, so the modulo is unbiased
				out = append(out, base62Alphabet[int(b)%62])
			}
		}
	}
	return string(out), nil
}

func base62Checksum(payload string) string {
	sum := crc32.ChecksumIEEE([]byte(payload))
	out := make([]byte, keyChecksumLen)
	for i := keyChecksumLen - 1; i >= 0; i-- {
		out[i] = base62Alphabet[sum%62]
		sum /= 62
	}
	return string(out)
}

// newCurrentKey builds a current-format key around an existing display prefix.
func newCurrentKey(prefix string) (string, error) {
	body, err := randomBase62(keyBodyLength)
	if err != nil {
		return "", err
	}
	payload := currentKeyPrefix + prefix + body
	return payload + base62Checksum(payload), nil
}

func isBase62(s string) bool {
	for i := 0; i < len(s); i++ {
		if !strings.ContainsRune(base62Alphabet, rune(s[i])) {
			return false
		}
	}
	return true
}

// parseCurrentKey validates the shape and checksum of a current-format key and
// returns its display prefix.
func parseCurrentKey(plain string) (string, bool) {
	if len(plain) != currentKeyLength || !strings.HasPrefix(plain, currentKeyPrefix) || !isBase62(plain[len(currentKeyPrefix):]) {
		return "", false
	}
	payload := plain[:currentKeyLength-keyChecksumLen]
	if base62Checksum(payload) != plain[len(payload):] {
		return "", false
	}
	prefix := plain[len(currentKeyPrefix) : len(currentKeyPrefix)+keyPrefixLength]
	return prefix, validPrefix(prefix)
}

func validPrefix(prefix string) bool {
	for _, char := range prefix {
		if (char < 'a' || char > 'z') && (char < '2' || char > '7') {
			return false
		}
	}
	return len(prefix) == keyPrefixLength
}

// Client families recorded for last use.
const (
	ClientMCP       = "mcp"
	ClientCurl      = "curl"
	ClientSDKJS     = "sdk-js"
	ClientSDKPython = "sdk-python"
	ClientBrowser   = "browser"
	ClientOther     = "other"
)

// ClientFamily reduces a User-Agent to a coarse family. The raw header is
// never kept.
func ClientFamily(userAgent string) string {
	ua := strings.ToLower(strings.TrimSpace(userAgent))
	switch {
	case ua == "":
		return ClientOther
	case strings.Contains(ua, "mcp"):
		return ClientMCP
	case strings.HasPrefix(ua, "curl/"):
		return ClientCurl
	case strings.HasPrefix(ua, "supermetry-js"), strings.HasPrefix(ua, "supermetry-node"),
		strings.HasPrefix(ua, "node"), strings.HasPrefix(ua, "axios/"), strings.HasPrefix(ua, "undici"):
		return ClientSDKJS
	case strings.HasPrefix(ua, "supermetry-python"), strings.HasPrefix(ua, "python-requests"),
		strings.HasPrefix(ua, "python-httpx"), strings.HasPrefix(ua, "python-urllib"), strings.HasPrefix(ua, "aiohttp"):
		return ClientSDKPython
	case strings.HasPrefix(ua, "mozilla/"):
		return ClientBrowser
	}
	return ClientOther
}

// SetCountryResolver tells the service how to turn a request into a
// two-letter country using the local GeoIP database. The address itself is
// never kept.
func (k *Keys) SetCountryResolver(resolve func(*http.Request) string) { k.country = resolve }
