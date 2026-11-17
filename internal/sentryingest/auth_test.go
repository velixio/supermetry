package sentryingest

import (
	"errors"
	"net/http"
	"net/http/httptest"
	"testing"
	"time"

	"supermetry/internal/sites"
)

const (
	testKey   = "0123456789abcdef0123456789abcdef"
	otherKey  = "fedcba9876543210fedcba9876543210"
	testSite  = int32(42)
	testPath  = "/api/42/envelope/"
	testQuery = "?sentry_version=7&sentry_key=" + testKey
)

var testNow = time.Date(2026, 11, 17, 14, 0, 0, 0, time.UTC)

type fakeRegistry struct {
	sites map[int32]sites.Site
	keys  map[string]int32
}

func (f fakeRegistry) ByPublicID(string) (sites.Site, bool) { return sites.Site{}, false }
func (f fakeRegistry) Site(id int32) (sites.Site, bool)     { s, ok := f.sites[id]; return s, ok }
func (f fakeRegistry) All() []sites.Site                    { return nil }
func (f fakeRegistry) ByDSNKey(k string) (sites.Site, sites.DSNKey, bool) {
	id, ok := f.keys[k]
	if !ok {
		return sites.Site{}, sites.DSNKey{}, false
	}
	s := f.sites[id]
	for _, dk := range s.App.DSNKeys {
		if dk.PublicKey == k {
			return s, dk, true
		}
	}
	return sites.Site{}, sites.DSNKey{}, false
}

func appSite(id int32, keys ...sites.DSNKey) sites.Site {
	return sites.Site{ID: id, Kind: sites.KindApp, TZ: time.UTC, App: &sites.AppConfig{DSNKeys: keys, MaxReportAgeDays: 7}}
}

func newRegistry(ss ...sites.Site) fakeRegistry {
	r := fakeRegistry{sites: map[int32]sites.Site{}, keys: map[string]int32{}}
	for _, s := range ss {
		r.sites[s.ID] = s
		for _, k := range s.App.DSNKeys {
			r.keys[k.PublicKey] = s.ID
		}
	}
	return r
}

func authRequest(query string, headers map[string]string) *http.Request {
	r := httptest.NewRequest(http.MethodPost, testPath+query, nil)
	for k, v := range headers {
		r.Header.Set(k, v)
	}
	return r
}

func TestAuthenticateCarriers(t *testing.T) {
	reg := newRegistry(appSite(testSite, sites.DSNKey{PublicKey: testKey}))
	dsn := "https://" + testKey + "@example.test/42"
	for name, c := range map[string]struct {
		query   string
		headers map[string]string
		hdr     EnvelopeHeader
	}{
		"X-Sentry-Auth":     {"", map[string]string{"X-Sentry-Auth": "Sentry sentry_version=7, sentry_client=x/1, sentry_key=" + testKey + ", sentry_secret=zzz"}, EnvelopeHeader{}},
		"Authorization":     {"", map[string]string{"Authorization": "Sentry sentry_key=" + testKey}, EnvelopeHeader{}},
		"query":             {testQuery, nil, EnvelopeHeader{}},
		"envelope dsn":      {"", nil, EnvelopeHeader{DSN: dsn}},
		"all agree":         {testQuery, map[string]string{"X-Sentry-Auth": "Sentry sentry_key=" + testKey}, EnvelopeHeader{DSN: dsn}},
		"version 7 header":  {"", map[string]string{"X-Sentry-Auth": "Sentry sentry_version=7,sentry_key=" + testKey}, EnvelopeHeader{}},
		"sentry_secret too": {"?sentry_key=" + testKey + "&sentry_secret=s&sentry_timestamp=1", nil, EnvelopeHeader{}},
	} {
		site, key, err := Authenticate(authRequest(c.query, c.headers), c.hdr, testSite, reg, testNow)
		if err != nil || site.ID != testSite || key.PublicKey != testKey {
			t.Errorf("%s: site=%d err=%v", name, site.ID, err)
		}
	}
}

func TestAuthenticateRejects(t *testing.T) {
	expired := testNow.Add(-time.Hour)
	reg := newRegistry(
		appSite(testSite, sites.DSNKey{PublicKey: testKey}, sites.DSNKey{PublicKey: otherKey, ExpiresAt: &expired}),
		appSite(7, sites.DSNKey{PublicKey: "11111111111111111111111111111111"}),
	)
	for name, c := range map[string]struct {
		query   string
		headers map[string]string
		hdr     EnvelopeHeader
		pathID  int32
		want    error
	}{
		"none":              {"", nil, EnvelopeHeader{}, testSite, ErrAuthMissing},
		"two keys":          {testQuery, map[string]string{"X-Sentry-Auth": "Sentry sentry_key=" + otherKey}, EnvelopeHeader{}, testSite, ErrAuthMultiple},
		"query vs dsn":      {testQuery, nil, EnvelopeHeader{DSN: "https://" + otherKey + "@h/42"}, testSite, ErrAuthMultiple},
		"path id mismatch":  {testQuery, nil, EnvelopeHeader{}, 7, ErrAuthMismatch},
		"unknown key":       {"?sentry_key=22222222222222222222222222222222", nil, EnvelopeHeader{}, testSite, ErrAuthMismatch},
		"malformed key":     {"?sentry_key=ABC", nil, EnvelopeHeader{}, testSite, ErrAuthMismatch},
		"expired key":       {"?sentry_key=" + otherKey, nil, EnvelopeHeader{}, testSite, ErrAuthMismatch},
		"version 8":         {"?sentry_version=8&sentry_key=" + testKey, nil, EnvelopeHeader{}, testSite, ErrVersion},
		"version 8 in auth": {"", map[string]string{"X-Sentry-Auth": "Sentry sentry_version=8, sentry_key=" + testKey}, EnvelopeHeader{}, testSite, ErrVersion},
		"non sentry scheme": {"", map[string]string{"Authorization": "Bearer " + testKey}, EnvelopeHeader{}, testSite, ErrAuthMissing},
	} {
		_, _, err := Authenticate(authRequest(c.query, c.headers), c.hdr, c.pathID, reg, testNow)
		if !errors.Is(err, c.want) {
			t.Errorf("%s: err = %v, want %v", name, err, c.want)
		}
	}
	// A non-app site is never reachable, whatever its key.
	web := sites.Site{ID: 9, Kind: sites.KindWeb, TZ: time.UTC, App: &sites.AppConfig{DSNKeys: []sites.DSNKey{{PublicKey: "33333333333333333333333333333333"}}}}
	if _, _, err := Authenticate(authRequest("?sentry_key=33333333333333333333333333333333", nil), EnvelopeHeader{}, 9, newRegistry(web), testNow); !errors.Is(err, ErrAuthMismatch) {
		t.Errorf("web site: %v", err)
	}
}
