package sentryapi

import (
	"crypto/rand"
	"crypto/sha256"
	"encoding/hex"
	"math"
	"net/http"
	"net/netip"
	"strconv"
	"strings"

	"supermetry/internal/clientip"
	"supermetry/internal/ingest"
)

const (
	// DefaultIPPerMinute and DefaultTokenPerMinute bound requests to /api/0/.
	DefaultIPPerMinute    = 600
	DefaultTokenPerMinute = 300
)

// limits throttles requests per client address and per API token. Both keys
// are salted hashes, so neither a raw address nor a credential is held in
// memory beyond the request.
type limits struct {
	ip, token         ingest.RateLimiter
	ipRate, tokenRate float64
	salt              [16]byte
	trusted           []netip.Prefix
}

// newLimits builds the limiters; a negative rate disables that limiter and
// zero selects the default.
func newLimits(ipPerMin, tokenPerMin int, trusted []netip.Prefix) *limits {
	l := &limits{trusted: trusted}
	_, _ = rand.Read(l.salt[:])
	if ipPerMin == 0 {
		ipPerMin = DefaultIPPerMinute
	}
	if tokenPerMin == 0 {
		tokenPerMin = DefaultTokenPerMinute
	}
	if ipPerMin > 0 {
		l.ipRate = float64(ipPerMin) / 60
		l.ip = ingest.NewTokenBuckets(l.ipRate, burstFor(ipPerMin))
	}
	if tokenPerMin > 0 {
		l.tokenRate = float64(tokenPerMin) / 60
		l.token = ingest.NewTokenBuckets(l.tokenRate, burstFor(tokenPerMin))
	}
	return l
}

// burstFor allows a sentry-cli upload (several parallel chunk requests) to
// start at full speed: half a minute of the sustained rate, at least 20.
func burstFor(perMin int) int { return max(20, perMin/2) }

func (l *limits) key(kind, v string) string {
	h := sha256.New()
	h.Write(l.salt[:])
	h.Write([]byte(kind))
	h.Write([]byte(v))
	return hex.EncodeToString(h.Sum(nil)[:12])
}

// check returns the seconds to wait when the request is over a limit.
func (l *limits) check(r *http.Request) (retryAfter int, limited bool) {
	if l == nil {
		return 0, false
	}
	if l.ip != nil {
		if ip, ok := clientip.Resolve(r, l.trusted); ok && !l.ip.Allow(l.key("ip", ip.String())) {
			return wait(l.ipRate), true
		}
	}
	if l.token != nil {
		if _, cred, ok := strings.Cut(r.Header.Get("Authorization"), " "); ok && strings.TrimSpace(cred) != "" {
			if !l.token.Allow(l.key("token", strings.TrimSpace(cred))) {
				return wait(l.tokenRate), true
			}
		}
	}
	return 0, false
}

// wait is the time for one token to refill, rounded up to whole seconds.
func wait(perSec float64) int {
	if perSec <= 0 {
		return 60
	}
	return max(1, int(math.Ceil(1/perSec)))
}

func tooManyRequests(w http.ResponseWriter, retryAfter int) {
	w.Header().Set("Retry-After", strconv.Itoa(retryAfter))
	writeDetail(w, http.StatusTooManyRequests, "rate limit exceeded, retry later")
}
