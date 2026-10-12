package api_test

import (
	"context"
	"net/http"
	"net/http/httptest"
	"testing"

	"supermetry/internal/api"
	"supermetry/internal/config"
	"supermetry/internal/enrich"
	"supermetry/internal/links"
	"supermetry/internal/sites"
)

type linkRegistry struct{}

func (linkRegistry) ByPublicID(id string) (sites.Site, bool) {
	return sites.Site{ID: 1, PublicID: id}, id == "linksite0001"
}
func (linkRegistry) Site(id int32) (sites.Site, bool) { return sites.Site{ID: id}, id == 1 }
func (linkRegistry) All() []sites.Site                { return nil }

func TestShortLinkAndPixelRoutesAreMountedWithoutAuthentication(t *testing.T) {
	enricher, err := enrich.New("")
	if err != nil {
		t.Fatal(err)
	}
	recorder := links.NewRecorder(nil, links.RecorderOptions{})
	handler := &links.Handler{
		Lookup: func(_ context.Context, slug string) (links.Link, error) {
			if slug == "go" {
				return links.Link{ID: 1, SiteID: 1, Slug: "go", Destination: "https://a.example/x", Enabled: true}, nil
			}
			return links.Link{}, links.ErrNotFound
		},
		Recorder: recorder, Sites: linkRegistry{}, Enricher: enricher,
	}
	router := api.NewRouter(api.Deps{Config: config.Config{AdminAPIKey: "test-key"}, Links: handler})

	do := func(method, target string) *httptest.ResponseRecorder {
		request := httptest.NewRequest(method, target, nil)
		request.Header.Set("User-Agent", "Mozilla/5.0 (X11; Linux x86_64) AppleWebKit/537.36 Chrome/124.0 Safari/537.36")
		response := httptest.NewRecorder()
		router.ServeHTTP(response, request)
		return response
	}
	if response := do(http.MethodGet, "/l/go"); response.Code != http.StatusFound || response.Header().Get("Location") != "https://a.example/x" {
		t.Fatalf("redirect status=%d location=%q", response.Code, response.Header().Get("Location"))
	}
	if response := do(http.MethodGet, "/l/missing"); response.Code != http.StatusNotFound {
		t.Fatalf("missing status=%d", response.Code)
	}
	if response := do(http.MethodPost, "/l/go"); response.Code == http.StatusFound {
		t.Fatal("POST must not redirect")
	}
	if response := do(http.MethodGet, "/px/linksite0001.gif?c=mail"); response.Code != http.StatusOK || response.Header().Get("Content-Type") != "image/gif" {
		t.Fatalf("pixel status=%d type=%q", response.Code, response.Header().Get("Content-Type"))
	}
}

func TestShortLinkRoutesAbsentWithoutHandler(t *testing.T) {
	router := api.NewRouter(api.Deps{Config: config.Config{AdminAPIKey: "test-key"}})
	response := httptest.NewRecorder()
	router.ServeHTTP(response, httptest.NewRequest(http.MethodGet, "/l/go", nil))
	if response.Code != http.StatusNotFound {
		t.Fatalf("status=%d", response.Code)
	}
}
