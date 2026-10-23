package ghstars

import (
	"context"
	"encoding/json"
	"errors"
	"io"
	"net/http"
	"net/http/httptest"
	"strings"
	"sync/atomic"
	"testing"
	"time"
)

// graphqlServer answers POST /graphql with the given handler.
func graphqlServer(t *testing.T, h http.HandlerFunc) *Client {
	t.Helper()
	mux := http.NewServeMux()
	mux.HandleFunc("POST /graphql", h)
	server := httptest.NewServer(mux)
	t.Cleanup(server.Close)
	c := &Client{BaseURL: server.URL, Token: "tok", HTTP: server.Client(), GraphQLPageSize: 2}
	c.retryDelay = time.Millisecond
	return c
}

func TestStargazerPageDecodesEdgesAndSendsTheCursor(t *testing.T) {
	var gotAuth string
	var gotVars map[string]any
	c := graphqlServer(t, func(w http.ResponseWriter, r *http.Request) {
		gotAuth = r.Header.Get("Authorization")
		var body struct {
			Query     string         `json:"query"`
			Variables map[string]any `json:"variables"`
		}
		_ = json.NewDecoder(r.Body).Decode(&body)
		gotVars = body.Variables
		if !strings.Contains(body.Query, "STARRED_AT") || !strings.Contains(body.Query, "databaseId") {
			t.Errorf("query = %s", body.Query)
		}
		_, _ = io.WriteString(w, `{"data":{"repository":{"stargazers":{"pageInfo":{"hasNextPage":true,"endCursor":"c2"},"edges":[
			{"starredAt":"2026-10-02T00:00:00Z","node":{"databaseId":7,"login":"g"}},
			{"starredAt":"2026-10-01T00:00:00Z","node":{"databaseId":6,"login":"f"}}]}}}}`)
	})
	page, err := c.StargazerPage(context.Background(), "Velixio/Supermetry", "c1")
	if err != nil {
		t.Fatal(err)
	}
	if gotAuth != "Bearer tok" || gotVars["owner"] != "Velixio" || gotVars["name"] != "Supermetry" || gotVars["after"] != "c1" || gotVars["first"] != float64(2) {
		t.Fatalf("auth=%q vars=%v", gotAuth, gotVars)
	}
	if !page.HasNext || page.EndCursor != "c2" || len(page.Stargazers) != 2 || page.Stargazers[0].UserID != 7 || page.Stargazers[1].Login != "f" || !page.Stargazers[0].StarredAt.Equal(at("2026-10-02T00:00:00Z")) {
		t.Fatalf("page = %+v", page)
	}
	// The first page sends a null cursor.
	if _, err := c.StargazerPage(context.Background(), "a/b", ""); err != nil || gotVars["after"] != nil {
		t.Fatalf("first page vars = %v, %v", gotVars, err)
	}
}

func TestStargazerPageNeedsAToken(t *testing.T) {
	var calls atomic.Int32
	c := graphqlServer(t, func(http.ResponseWriter, *http.Request) { calls.Add(1) })
	c.Token = ""
	if _, err := c.StargazerPage(context.Background(), "a/b", ""); !errors.Is(err, ErrTokenRequired) || calls.Load() != 0 {
		t.Fatalf("err = %v, calls = %d", err, calls.Load())
	}
}

func TestStargazerPageMapsErrors(t *testing.T) {
	long := strings.Repeat("x", 500)
	cases := map[string]struct {
		status int
		body   string
		want   error
		text   string
	}{
		"not found":    {200, `{"errors":[{"type":"NOT_FOUND","message":"Could not resolve to a Repository"}]}`, ErrRepoNotFound, ""},
		"rate limited": {200, `{"errors":[{"type":"RATE_LIMITED","message":"API rate limit exceeded"}]}`, ErrRateLimited, ""},
		"http 403":     {403, `{"message":"forbidden"}`, ErrRateLimited, ""},
		"http 429":     {429, `{}`, ErrRateLimited, ""},
		"http 401":     {401, `{"message":"Bad credentials"}`, ErrBadToken, ""},
		"other":        {200, `{"errors":[{"type":"SOMETHING","message":"` + long + `"}]}`, nil, "xxxx"},
		"no data":      {200, `{"data":{"repository":null}}`, ErrRepoNotFound, ""},
	}
	for name, tc := range cases {
		c := graphqlServer(t, func(w http.ResponseWriter, _ *http.Request) {
			w.WriteHeader(tc.status)
			_, _ = io.WriteString(w, tc.body)
		})
		_, err := c.StargazerPage(context.Background(), "a/b", "")
		if err == nil {
			t.Errorf("%s: no error", name)
			continue
		}
		if tc.want != nil && !errors.Is(err, tc.want) {
			t.Errorf("%s: err = %v", name, err)
		}
		if tc.want == nil && (!strings.Contains(err.Error(), tc.text) || len(err.Error()) > 400 || errors.Is(err, ErrRateLimited) || errors.Is(err, ErrRepoNotFound)) {
			t.Errorf("%s: err = %v (len %d)", name, err, len(err.Error()))
		}
	}
}

func TestStargazerPageRetriesTimeoutsAndServerErrors(t *testing.T) {
	var calls atomic.Int32
	c := graphqlServer(t, func(w http.ResponseWriter, _ *http.Request) {
		if calls.Add(1) <= 2 {
			w.WriteHeader(http.StatusBadGateway)
			return
		}
		_, _ = io.WriteString(w, `{"data":{"repository":{"stargazers":{"pageInfo":{"hasNextPage":false,"endCursor":""},"edges":[]}}}}`)
	})
	if _, err := c.StargazerPage(context.Background(), "a/b", ""); err != nil || calls.Load() != 3 {
		t.Fatalf("err = %v, calls = %d", err, calls.Load())
	}
	calls.Store(-100) // always 502 from here on
	failing := graphqlServer(t, func(w http.ResponseWriter, _ *http.Request) { calls.Add(1); w.WriteHeader(http.StatusBadGateway) })
	calls.Store(0)
	if _, err := failing.StargazerPage(context.Background(), "a/b", ""); err == nil || calls.Load() != 3 {
		t.Fatalf("persistent 502: err = %v, calls = %d (want 1 try + 2 retries)", err, calls.Load())
	}

	// A request that exceeds the per-request timeout is retried too.
	var slow atomic.Int32
	timed := graphqlServer(t, func(w http.ResponseWriter, r *http.Request) {
		_, _ = io.Copy(io.Discard, r.Body) // lets the server notice the client giving up
		if slow.Add(1) == 1 {
			<-r.Context().Done()
			return
		}
		_, _ = io.WriteString(w, `{"data":{"repository":{"stargazers":{"pageInfo":{"hasNextPage":false,"endCursor":""},"edges":[]}}}}`)
	})
	timed.graphqlTimeout = 50 * time.Millisecond
	if _, err := timed.StargazerPage(context.Background(), "a/b", ""); err != nil || slow.Load() != 2 {
		t.Fatalf("timeout retry: err = %v, calls = %d", err, slow.Load())
	}
}

func TestStargazerPageStopsOnCancel(t *testing.T) {
	var calls atomic.Int32
	c := graphqlServer(t, func(w http.ResponseWriter, _ *http.Request) { calls.Add(1); w.WriteHeader(http.StatusBadGateway) })
	ctx, cancel := context.WithCancel(context.Background())
	cancel()
	if _, err := c.StargazerPage(ctx, "a/b", ""); err == nil || calls.Load() > 1 {
		t.Fatalf("err = %v, calls = %d", err, calls.Load())
	}
}
