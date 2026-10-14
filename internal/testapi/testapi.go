// Package testapi builds a production-shaped HTTP API backed by an isolated
// PostgreSQL database. It is shared by black-box endpoint matrix tests.
package testapi

import (
	"bytes"
	"context"
	"encoding/json"
	"fmt"
	"io"
	"net/http"
	"net/http/httptest"
	"strconv"
	"strings"
	"sync/atomic"
	"testing"
	"time"

	"github.com/jackc/pgx/v5"
	"github.com/jackc/pgx/v5/pgxpool"

	"supermetry/internal/api"
	"supermetry/internal/config"
	"supermetry/internal/db"
	"supermetry/internal/dict"
	"supermetry/internal/heatmap"
	"supermetry/internal/ingest"
	"supermetry/internal/minutes"
	platformauth "supermetry/internal/platform/auth"
	"supermetry/internal/platform/authn"
	"supermetry/internal/platform/authz"
	platformcrypto "supermetry/internal/platform/crypto"
	platformgoals "supermetry/internal/platform/goals"
	platformhttp "supermetry/internal/platform/http"
	"supermetry/internal/platform/keys"
	"supermetry/internal/platform/mail"
	"supermetry/internal/platform/orgs"
	"supermetry/internal/platform/shares"
	"supermetry/internal/platform/sitesvc"
	"supermetry/internal/platform/store"
	"supermetry/internal/privacy"
	analyticsquery "supermetry/internal/query"
	"supermetry/internal/routehits"
	"supermetry/internal/session"
	"supermetry/internal/testpg"
)

var sequence atomic.Uint64

type disabledMail struct{}

func (disabledMail) Send(context.Context, mail.Message) error { return nil }
func (disabledMail) Enabled() bool                            { return false }

type clockFunc func() time.Time

func (f clockFunc) Now() time.Time { return f() }

// User identifies a seeded platform user.
type User struct{ ID int64 }

// Org identifies a seeded organization and its owner.
type Org struct {
	ID    int64
	Owner User
}

// Site identifies a seeded site.
type Site struct {
	ID       int32
	PublicID string
	OrgID    int64
}

func (s Site) String() string { return strconv.FormatInt(int64(s.ID), 10) }

// Env owns one isolated database and a real application router.
type Env struct {
	Pool        *pgxpool.Pool
	Handler     http.Handler
	Now         time.Time
	data        *store.Store
	registry    *sitesvc.Registry
	sites       *sitesvc.Sites
	keys        *keys.Keys
	auth        *platformauth.Auth
	orgs        *orgs.Orgs
	definitions *platformgoals.Goals
}

// New constructs a Redis-free environment with production services.
func New(t testing.TB) *Env {
	return newEnv(t, 2, 30)
}

// NewWithRateLimit constructs an environment with a deterministic keyed
// limiter configuration for rate-limit contract tests.
func NewWithRateLimit(t testing.TB, rate float64, burst int) *Env {
	return newEnv(t, rate, burst)
}

func newEnv(t testing.TB, rate float64, burst int) *Env {
	t.Helper()
	pool, _ := testpg.New(t)
	ctx := context.Background()
	if err := db.Migrate(ctx, pool); err != nil {
		t.Fatalf("migrate test database: %v", err)
	}
	now := time.Date(2026, 10, 12, 12, 0, 0, 0, time.UTC)
	if err := db.EnsurePartitions(ctx, pool, now.AddDate(0, 0, -40), now.AddDate(0, 1, 0)); err != nil {
		t.Fatalf("ensure test partitions: %v", err)
	}
	data := &store.Store{Pool: pool}
	registry := sitesvc.NewRegistry(data, 0)
	keyService := keys.New(data)
	definitions := platformgoals.New(data, 0)
	orgService := orgs.New(data, registry)
	siteService := sitesvc.New(data, registry, privacy.Identified)
	siteService.SetClock(func() time.Time { return now })
	box, err := platformcrypto.NewSecretBox([]byte("0123456789abcdef0123456789abcdef"), "supermetry/totp/v1")
	if err != nil {
		t.Fatalf("create secret box: %v", err)
	}
	authService := platformauth.New(data, disabledMail{}, box, "https://supermetry.example", platformauth.WithClock(clockFunc(func() time.Time { return now })))
	shareService := shares.New(data)
	analyticsAuthenticator := authn.New(authService, keyService, shareService, orgService)
	platformAuthenticator := platformhttp.NewAuthenticator(authService, keyService, shareService, orgService)
	dictionary := dict.New(pool)
	engine := analyticsquery.NewEngine(pool, dictionary, registry, definitions)
	engine.SetTrackingMode(privacy.Identified)
	live := session.NewMemory(4, clockFunc(func() time.Time { return now }))
	platform := platformhttp.New(platformhttp.Deps{
		Now: func() time.Time { return now }, TrackingMode: privacy.Identified,
		BaseURL: "https://supermetry.example", TrackerPath: "/js/script.js",
		Authenticator: platformAuthenticator, Sites: registry, Store: data, Auth: authService,
		Orgs: orgService, Keys: keyService, Definitions: definitions,
		Mutations: platformhttp.Services{Sites: siteService, Keys: keyService, Store: data},
	})
	router := api.NewRouter(api.Deps{
		Config: config.Config{EventPath: "/api/public/v1/events", TrackerPath: "/js/script.js", Tracking: config.TrackingMode{Mode: privacy.Identified}},
		Query:  engine, SessionExplore: engine, Heatmaps: heatmap.New(pool, registry),
		PublicAuth: analyticsAuthenticator, PrivateAuth: analyticsAuthenticator, Sites: registry,
		Platform: platform, Sessions: live, MinuteSource: minutes.NewStore(pool), Dictionary: dictionary,
		Now: func() time.Time { return now }, RateLimitNow: func() time.Time { return now },
		RateLimiter: ingest.NewTokenBuckets(rate, burst),
		Ingest:      http.HandlerFunc(func(w http.ResponseWriter, _ *http.Request) { w.WriteHeader(http.StatusAccepted) }),
	})
	env := &Env{Pool: pool, Handler: router, Now: now, data: data, registry: registry, sites: siteService, keys: keyService, auth: authService, orgs: orgService, definitions: definitions}
	t.Cleanup(func() {
		shutdownCtx, cancel := context.WithTimeout(context.Background(), time.Second)
		defer cancel()
		if err := authService.Shutdown(shutdownCtx); err != nil {
			t.Errorf("shutdown auth service: %v", err)
		}
		definitions.Close()
		keyService.Close()
		registry.Close()
	})
	return env
}

// Org creates an owner and organization.
func (e *Env) Org(t testing.TB) Org {
	t.Helper()
	n := sequence.Add(1)
	ctx := context.Background()
	var userID, orgID int64
	email := fmt.Sprintf("api-matrix-%d@example.test", n)
	if err := e.Pool.QueryRow(ctx, `insert into users(email,name,password_hash) values ($1,'API Matrix','hash') returning id`, email).Scan(&userID); err != nil {
		t.Fatalf("seed user: %v", err)
	}
	if err := e.data.Tx(ctx, func(tx pgx.Tx) error {
		var err error
		orgID, err = store.CreateOrg(ctx, tx, fmt.Sprintf("API Matrix %d", n), userID)
		return err
	}); err != nil {
		t.Fatalf("seed organization: %v", err)
	}
	return Org{ID: orgID, Owner: User{ID: userID}}
}

// Site creates an active UTC site for org.
func (e *Env) Site(t testing.TB, org Org) Site {
	return e.SiteInTimezone(t, org, "UTC")
}

// SiteInTimezone creates an active site in an IANA time zone.
func (e *Env) SiteInTimezone(t testing.TB, org Org, timezone string) Site {
	t.Helper()
	n := sequence.Add(1)
	created, err := e.sites.Create(context.Background(), authz.Principal{UserID: org.Owner.ID, OrgID: org.ID, Role: authz.RoleOwner}, sitesvc.CreateInput{
		Name: fmt.Sprintf("Site %d", n), Hosts: []string{fmt.Sprintf("site-%d.example.test", n)}, Timezone: timezone, RetentionDays: 30, RevenueCurrency: "USD",
	})
	if err != nil {
		t.Fatalf("seed site: %v", err)
	}
	return Site{ID: created.ID, PublicID: created.PublicID, OrgID: org.ID}
}

// APIKey creates an unrestricted organization key with scopes.
func (e *Env) APIKey(t testing.TB, org Org, scopes ...string) string {
	t.Helper()
	plain, _, err := e.keys.Create(context.Background(), authz.Principal{UserID: org.Owner.ID, OrgID: org.ID, Role: authz.RoleOwner}, "API matrix", scopes, nil, nil)
	if err != nil {
		t.Fatalf("seed API key: %v", err)
	}
	return plain
}

// Session creates an authenticated browser session.
func (e *Env) Session(t testing.TB, user User) *http.Cookie {
	t.Helper()
	token := fmt.Sprintf("api-matrix-session-%d-%d", user.ID, sequence.Add(1))
	if err := store.CreateSession(context.Background(), e.Pool, store.UserSession{
		IDHash: platformcrypto.HashToken(token), UserID: user.ID,
		CreatedAt: e.Now, ExpiresAt: e.Now.Add(time.Hour), DeviceLabel: "API matrix",
	}); err != nil {
		t.Fatalf("seed session: %v", err)
	}
	return &http.Cookie{Name: platformauth.CookieName, Value: token}
}

// RefreshSites publishes database changes made directly by a test fixture.
func (e *Env) RefreshSites(t testing.TB) {
	t.Helper()
	if err := e.registry.Invalidate(); err != nil {
		t.Fatalf("refresh site registry: %v", err)
	}
}

// Req describes one request to the in-process server.
type Req struct {
	Name       string
	Method     string
	Path       string
	Body       any
	APIKey     string
	Cookie     *http.Cookie
	Headers    http.Header
	Values     map[string]string
	RemoteAddr string
}

// Resp captures the observable HTTP contract.
type Resp struct {
	Status  int
	Headers http.Header
	Body    []byte
	Problem *api.Problem
	JSON    any
}

// Do expands placeholders, executes req, and decodes JSON/problem responses.
func (e *Env) Do(t testing.TB, req Req) Resp {
	t.Helper()
	path := expand(req.Path, req.Values)
	var body io.Reader
	switch value := req.Body.(type) {
	case nil:
	case string:
		body = strings.NewReader(value)
	case []byte:
		body = bytes.NewReader(value)
	default:
		encoded, err := json.Marshal(value)
		if err != nil {
			t.Fatalf("encode request body: %v", err)
		}
		body = bytes.NewReader(encoded)
	}
	request := httptest.NewRequest(req.Method, path, body)
	if req.RemoteAddr != "" {
		request.RemoteAddr = req.RemoteAddr
	}
	request = routehits.WithTestName(request, req.Name)
	if req.Body != nil {
		request.Header.Set("Content-Type", "application/json")
	}
	if req.APIKey != "" {
		request.Header.Set("Authorization", "Bearer "+req.APIKey)
	}
	if req.Cookie != nil {
		request.AddCookie(req.Cookie)
	}
	for name, values := range req.Headers {
		request.Header[name] = append([]string(nil), values...)
	}
	recorder := httptest.NewRecorder()
	e.Handler.ServeHTTP(recorder, request)
	result := recorder.Result()
	defer result.Body.Close()
	responseBody, err := io.ReadAll(result.Body)
	if err != nil {
		t.Fatalf("read response: %v", err)
	}
	response := Resp{Status: result.StatusCode, Headers: result.Header.Clone(), Body: responseBody}
	contentType := result.Header.Get("Content-Type")
	if strings.HasPrefix(contentType, "application/problem+json") {
		var problem api.Problem
		if err := json.Unmarshal(responseBody, &problem); err != nil {
			t.Fatalf("decode problem response: %v; body=%s", err, responseBody)
		}
		response.Problem = &problem
	} else if strings.HasPrefix(contentType, "application/json") && len(responseBody) != 0 {
		if err := json.Unmarshal(responseBody, &response.JSON); err != nil {
			t.Fatalf("decode JSON response: %v; body=%s", err, responseBody)
		}
	}
	return response
}

func expand(path string, values map[string]string) string {
	for key, value := range values {
		path = strings.ReplaceAll(path, "{"+key+"}", value)
	}
	return path
}

// AuthKind selects credentials for a table case.
type AuthKind int

const (
	AuthNone AuthKind = iota
	AuthAPIKey
	AuthSession
)

// Case is the common endpoint-matrix row.
type Case struct {
	Name        string
	Method      string
	Path        string
	Auth        AuthKind
	Body        any
	Want        int
	WantProblem string
	APIKey      string
	Cookie      *http.Cookie
	Values      map[string]string
}

// RunCases executes table rows and validates status, media type, and problems.
func (e *Env) RunCases(t *testing.T, cases []Case) {
	t.Helper()
	for _, test := range cases {
		t.Run(test.Name, func(t *testing.T) {
			request := Req{Name: t.Name(), Method: test.Method, Path: test.Path, Body: test.Body, Values: test.Values}
			switch test.Auth {
			case AuthAPIKey:
				request.APIKey = test.APIKey
			case AuthSession:
				request.Cookie = test.Cookie
			}
			response := e.Do(t, request)
			if response.Status != test.Want {
				t.Fatalf("status=%d body=%s, want %d", response.Status, response.Body, test.Want)
			}
			if test.Want >= 400 {
				if response.Problem == nil {
					t.Fatalf("content-type=%q body=%s, want problem+json", response.Headers.Get("Content-Type"), response.Body)
				}
				if test.WantProblem != "" && !strings.HasSuffix(response.Problem.Type, test.WantProblem) {
					t.Fatalf("problem type=%q, want suffix %q", response.Problem.Type, test.WantProblem)
				}
			} else if response.Status != http.StatusNoContent && response.Headers.Get("Content-Type") == "" {
				t.Fatal("successful response has no Content-Type")
			}
		})
	}
}
