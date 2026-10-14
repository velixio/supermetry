package api

import (
	"context"
	"encoding/json"
	"net/http"
	"net/http/httptest"
	"strings"
	"testing"
	"time"

	"supermetry/internal/dropstats"
	"supermetry/internal/sites"
)

type allSitesRegistry []sites.Site

func (r allSitesRegistry) ByPublicID(string) (sites.Site, bool) { return sites.Site{}, false }
func (r allSitesRegistry) Site(int32) (sites.Site, bool)        { return sites.Site{}, false }
func (r allSitesRegistry) All() []sites.Site                    { return r }

type stubDropReader []dropstats.SiteSummary

func (s stubDropReader) Read(context.Context, []dropstats.SiteDay) ([]dropstats.SiteSummary, error) {
	return s, nil
}

// A site with no bot rule hits must encode top_bot_rules as [] (the dashboard reads .length).
func TestAuthorizedSiteDropsEncodesEmptyBotRulesAsArray(t *testing.T) {
	registry := allSitesRegistry{{ID: 1, OrgID: 7, PublicID: "site1", TZ: time.UTC}}
	reader := stubDropReader{{SiteID: 1}}
	r := httptest.NewRequest(http.MethodGet, "/", nil)
	r = r.WithContext(context.WithValue(r.Context(), principalContextKey{}, Principal{OrgID: 7, AllSites: true}))

	rows, err := authorizedSiteDrops(r, reader, registry, time.Now())
	if err != nil {
		t.Fatal(err)
	}
	body, err := json.Marshal(rows)
	if err != nil {
		t.Fatal(err)
	}
	if !strings.Contains(string(body), `"top_bot_rules":[]`) {
		t.Fatalf("top_bot_rules not an empty array: %s", body)
	}
}
