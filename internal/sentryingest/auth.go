package sentryingest

import (
	"errors"
	"net/http"
	"net/url"
	"strconv"
	"strings"
	"time"

	"supermetry/internal/platform/appkeys"
	"supermetry/internal/sites"
)

// Authentication errors. All but ErrVersion are a 401.
var (
	ErrAuthMissing  = errors.New("missing DSN key")
	ErrAuthMultiple = errors.New("more than one DSN key")
	ErrAuthMismatch = errors.New("DSN key does not match the project")
	ErrVersion      = errors.New("unsupported sentry_version")
)

const maxProtocolVersion = 7

// authParams is what the request and envelope say about credentials.
type authParams struct {
	keys    []string
	version string
	client  string
}

func (p *authParams) addKey(k string) {
	k = strings.TrimSpace(k)
	if k == "" {
		return
	}
	for _, have := range p.keys {
		if have == k {
			return
		}
	}
	p.keys = append(p.keys, k)
}

// parseSentryAuth reads "Sentry k=v, k=v"; sentry_secret and sentry_timestamp
// are ignored.
func (p *authParams) parseSentryAuth(value string) {
	value = strings.TrimSpace(value)
	if len(value) < 7 || !strings.EqualFold(value[:6], "sentry") || (value[6] != ' ' && value[6] != '\t') {
		return
	}
	for _, part := range strings.Split(value[7:], ",") {
		k, v, ok := strings.Cut(strings.TrimSpace(part), "=")
		if !ok {
			continue
		}
		v = strings.Trim(strings.TrimSpace(v), `"`)
		switch strings.TrimSpace(k) {
		case "sentry_key":
			p.addKey(v)
		case "sentry_version":
			if p.version == "" {
				p.version = v
			}
		case "sentry_client":
			if p.client == "" {
				p.client = v
			}
		}
	}
}

func collectAuth(r *http.Request, header EnvelopeHeader) authParams {
	var p authParams
	p.parseSentryAuth(r.Header.Get("X-Sentry-Auth"))
	p.parseSentryAuth(r.Header.Get("Authorization"))
	q := r.URL.Query()
	p.addKey(q.Get("sentry_key"))
	if p.version == "" {
		p.version = q.Get("sentry_version")
	}
	if p.client == "" {
		p.client = q.Get("sentry_client")
	}
	if header.DSN != "" {
		if u, err := url.Parse(header.DSN); err == nil && u.User != nil {
			p.addKey(u.User.Username())
		}
	}
	return p
}

// Authenticate resolves the DSN key of a request. Carriers are the
// X-Sentry-Auth header, the Authorization header, the sentry_key query
// parameter and the envelope header's dsn. Two different keys are an error.
// The key must belong to the app site named by the path.
func Authenticate(r *http.Request, header EnvelopeHeader, pathID int32, reg sites.DSNRegistry, now time.Time) (sites.Site, sites.DSNKey, error) {
	p := collectAuth(r, header)
	return authenticate(p, pathID, reg, now)
}

func authenticate(p authParams, pathID int32, reg sites.DSNRegistry, now time.Time) (sites.Site, sites.DSNKey, error) {
	if p.version != "" {
		if v, err := strconv.Atoi(p.version); err == nil && v > maxProtocolVersion {
			return sites.Site{}, sites.DSNKey{}, ErrVersion
		}
	}
	switch len(p.keys) {
	case 0:
		return sites.Site{}, sites.DSNKey{}, ErrAuthMissing
	case 1:
	default:
		return sites.Site{}, sites.DSNKey{}, ErrAuthMultiple
	}
	if !appkeys.ValidDSNKey(p.keys[0]) {
		return sites.Site{}, sites.DSNKey{}, ErrAuthMismatch
	}
	site, key, ok := reg.ByDSNKey(p.keys[0])
	if !ok || key.Expired(now) || site.ID != pathID || site.Kind != sites.KindApp || site.App == nil {
		return sites.Site{}, sites.DSNKey{}, ErrAuthMismatch
	}
	return site, key, nil
}
