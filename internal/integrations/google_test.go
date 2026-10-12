package integrations

import (
	"context"
	"crypto/sha256"
	"encoding/base64"
	"encoding/json"
	"errors"
	"io"
	"net/http"
	"net/http/httptest"
	"net/url"
	"strings"
	"testing"
	"time"
)

// These tests talk to a loopback httptest server only.
func newGoogleForTest(t *testing.T, handler http.Handler, allowPrivate bool) *Google {
	t.Helper()
	server := httptest.NewServer(handler)
	t.Cleanup(server.Close)
	return NewGoogle(GoogleOptions{
		ClientID: "client-id", ClientSecret: "client-secret", AllowPrivate: allowPrivate,
		AuthURL: server.URL + "/auth", TokenURL: server.URL + "/token", RevokeURL: server.URL + "/revoke", APIBase: server.URL + "/api",
		Now: func() time.Time { return testNow },
	})
}

func TestGoogleAuthURLUsesPKCEAndOfflineAccess(t *testing.T) {
	g := NewGoogle(GoogleOptions{ClientID: "client-id", ClientSecret: "s"})
	raw := g.AuthURL(AuthParams{State: "st", RedirectURI: "https://app.example.test/cb", CodeChallenge: "chal"})
	parsed, err := url.Parse(raw)
	if err != nil || parsed.Host != "accounts.google.com" {
		t.Fatalf("auth URL = %q, %v", raw, err)
	}
	q := parsed.Query()
	if q.Get("code_challenge_method") != "S256" || q.Get("code_challenge") != "chal" || q.Get("state") != "st" ||
		q.Get("access_type") != "offline" || q.Get("redirect_uri") != "https://app.example.test/cb" ||
		q.Get("scope") != "https://www.googleapis.com/auth/webmasters.readonly" || strings.Contains(raw, "client-secret") {
		t.Fatalf("unexpected query %v", q)
	}
	if NewGoogle(GoogleOptions{ClientID: "x"}).Configured() || !g.Configured() {
		t.Fatal("Configured() must require both client id and secret")
	}
}

func TestGoogleExchangeRefreshAndFetch(t *testing.T) {
	verifier := "verifier-value"
	sum := sha256.Sum256([]byte(verifier))
	_ = base64.RawURLEncoding.EncodeToString(sum[:])
	mux := http.NewServeMux()
	mux.HandleFunc("/token", func(w http.ResponseWriter, r *http.Request) {
		body, _ := io.ReadAll(r.Body)
		form, _ := url.ParseQuery(string(body))
		switch form.Get("grant_type") {
		case "authorization_code":
			if form.Get("code_verifier") != verifier || form.Get("client_secret") != "client-secret" {
				http.Error(w, `{"error":"invalid_request"}`, http.StatusBadRequest)
				return
			}
			_, _ = w.Write([]byte(`{"access_token":"a1","refresh_token":"r1","expires_in":3600}`))
		case "refresh_token":
			if form.Get("refresh_token") == "revoked" {
				http.Error(w, `{"error":"invalid_grant"}`, http.StatusBadRequest)
				return
			}
			_, _ = w.Write([]byte(`{"access_token":"a2","expires_in":1800}`))
		}
	})
	mux.HandleFunc("/api/sites/", func(w http.ResponseWriter, r *http.Request) {
		if r.Header.Get("Authorization") != "Bearer a1" {
			http.Error(w, "no", http.StatusUnauthorized)
			return
		}
		if !strings.Contains(r.URL.EscapedPath(), "sc-domain%3Aexample.test") {
			t.Errorf("property not escaped in %q", r.URL.EscapedPath())
		}
		var req map[string]any
		_ = json.NewDecoder(r.Body).Decode(&req)
		if req["startDate"] != "2026-10-10" || req["endDate"] != "2026-10-10" {
			t.Errorf("dates = %v %v", req["startDate"], req["endDate"])
		}
		_, _ = w.Write([]byte(`{"rows":[{"keys":["go","/a","usa","DESKTOP"],"clicks":3,"impressions":30,"ctr":0.1,"position":2.5},{"keys":["short"],"clicks":1}]}`))
	})
	g := newGoogleForTest(t, mux, true)
	ctx := context.Background()

	tokens, err := g.Exchange(ctx, "code", "https://app.example.test/cb", verifier)
	if err != nil || tokens.AccessToken != "a1" || tokens.RefreshToken != "r1" || !tokens.Expiry.Equal(testNow.Add(time.Hour)) {
		t.Fatalf("Exchange() = %+v, %v", tokens, err)
	}
	refreshed, err := g.Refresh(ctx, "r1")
	if err != nil || refreshed.AccessToken != "a2" || refreshed.RefreshToken != "" {
		t.Fatalf("Refresh() = %+v, %v", refreshed, err)
	}
	var perr *ProviderError
	if _, err := g.Refresh(ctx, "revoked"); !errors.As(err, &perr) || perr.Kind != KindAuth {
		t.Fatalf("revoked refresh err = %v", err)
	}
	rows, err := g.Fetch(ctx, "a1", "sc-domain:example.test", day("2026-10-10"), 100)
	if err != nil || len(rows) != 1 || rows[0].Query != "go" || rows[0].Clicks != 3 || rows[0].Country != "usa" {
		t.Fatalf("Fetch() = %+v, %v", rows, err)
	}
	if _, err := g.Fetch(ctx, "wrong", "sc-domain:example.test", day("2026-10-10"), 100); !errors.As(err, &perr) || perr.Kind != KindAuth {
		t.Fatalf("unauthorized fetch err = %v", err)
	}
}

func TestGoogleErrorsNeverContainTokens(t *testing.T) {
	g := newGoogleForTest(t, http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		http.Error(w, "boom access-token-xyz", http.StatusInternalServerError)
	}), true)
	_, err := g.Fetch(context.Background(), "access-token-xyz", "sc-domain:example.test", day("2026-10-10"), 10)
	var perr *ProviderError
	if !errors.As(err, &perr) || perr.Kind != KindTransient || strings.Contains(err.Error(), "access-token-xyz") {
		t.Fatalf("err = %v", err)
	}
}

func TestGoogleClientIsSSRFGuardedAndDoesNotFollowRedirects(t *testing.T) {
	// Loopback is blocked unless AllowPrivate is set (it never is in production).
	g := newGoogleForTest(t, http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		t.Error("guarded client reached a loopback server")
	}), false)
	if _, err := g.Fetch(context.Background(), "t", "sc-domain:example.test", day("2026-10-10"), 10); err == nil {
		t.Fatal("expected a blocked destination error")
	}
	redirects := newGoogleForTest(t, http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		http.Redirect(w, r, "http://169.254.169.254/latest/meta-data", http.StatusFound)
	}), true)
	_, err := redirects.Refresh(context.Background(), "r")
	var perr *ProviderError
	if !errors.As(err, &perr) {
		t.Fatalf("redirect should surface as a provider error, got %v", err)
	}
}
