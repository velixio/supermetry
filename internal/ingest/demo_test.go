package ingest_test

import (
	"context"
	"testing"

	"supermetry/internal/sites"
)

func TestDemoSiteRejectedLikeUnknownSite(t *testing.T) {
	fixture := newHandlerFixture(t, 1, nil)
	normal, ok := fixture.h.Sites.ByPublicID("abc123")
	if !ok {
		t.Fatal("fixture site missing")
	}
	normal.Demo = true
	fixture.h.Sites = siteRegistry{site: normal}

	unknown := post(t, fixture.h, `{"site":"unknown","name":"pageview","url":"https://example.com/"}`, testUA, "198.51.100.7:5555")
	demo := post(t, fixture.h, okBody, testUA, "198.51.100.7:5555")
	if demo.Code != unknown.Code || demo.Body.String() != unknown.Body.String() || demo.Header().Get("Content-Type") != unknown.Header().Get("Content-Type") {
		t.Fatalf("demo response=(%d,%q,%q), unknown=(%d,%q,%q)", demo.Code, demo.Body.String(), demo.Header().Get("Content-Type"), unknown.Code, unknown.Body.String(), unknown.Header().Get("Content-Type"))
	}

	fixture.stop(t)
	var events int
	if err := fixture.pool.QueryRow(context.Background(), `select count(*) from events`).Scan(&events); err != nil {
		t.Fatal(err)
	}
	if events != 0 {
		t.Fatalf("events=%d, want 0", events)
	}
}

var _ sites.Registry = siteRegistry{}
