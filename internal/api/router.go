package api

import (
	"context"
	"crypto/rand"
	"crypto/sha256"
	"crypto/subtle"
	"embed"
	"encoding/hex"
	"encoding/json"
	"net/http"
	"panel/internal/sites"
	"strconv"
	"time"

	"go.yaml.in/yaml/v3"

	"panel/internal/config"
	"panel/internal/health"
	"panel/internal/ingest"
	"panel/internal/session"
)

const (
	publicPrefix  = "/api/public/v1"
	privatePrefix = "/api/private"
)

//go:embed openapi.yaml
var contractFS embed.FS

type principalContextKey struct{}

// Deps contains the services exposed by the analytics HTTP router.
type Deps struct {
	Config      config.Config
	Query       queryService
	Ingest      http.Handler
	PublicAuth  Authenticator
	PrivateAuth Authenticator
	Sites       sites.Registry // resolves site ownership for every site-scoped request
	RateLimiter ingest.RateLimiter
	Sessions    session.Store
	Now         func() time.Time
	Ready       func() bool
}

// NewRouter mounts the public integration API, private dashboard API and
// operational endpoints on a standard-library ServeMux.
func NewRouter(deps Deps) http.Handler {
	if deps.PublicAuth == nil {
		deps.PublicAuth = StaticKey(deps.Config.AdminAPIKey)
	}
	if deps.RateLimiter == nil {
		deps.RateLimiter = ingest.NewTokenBuckets(2, 30)
	}

	h := &handlers{query: deps.Query}
	mux := http.NewServeMux()

	public := http.NewServeMux()
	registerStats(public, publicPrefix, h)
	public.HandleFunc("POST "+publicPrefix+"/query", h.queryRequest)
	public.HandleFunc("POST "+publicPrefix+"/funnels/query", h.funnelRequest)
	public.HandleFunc("GET "+publicPrefix+"/realtime", realtimeHandler(deps.Sessions, deps.Now))
	mux.Handle(publicPrefix+"/", authenticate(deps.PublicAuth, deps.Sites, rateLimit(deps.RateLimiter, public)))

	private := http.NewServeMux()
	registerStats(private, privatePrefix, h)
	private.HandleFunc("POST "+privatePrefix+"/query", h.queryRequest)
	private.HandleFunc("POST "+privatePrefix+"/funnels/query", h.funnelRequest)
	private.HandleFunc("GET "+privatePrefix+"/realtime", realtimeHandler(deps.Sessions, deps.Now))
	private.HandleFunc("GET "+privatePrefix+"/health", dataHealthHandler)
	mux.Handle(privatePrefix+"/", authenticate(deps.PrivateAuth, deps.Sites, requireCSRF(private)))

	if deps.Ingest != nil && deps.Config.EventPath != "" {
		mux.Handle("POST "+deps.Config.EventPath, deps.Ingest)
	}
	if deps.Config.TrackerPath != "" {
		mux.HandleFunc("GET "+deps.Config.TrackerPath, http.NotFound)
	}
	mux.HandleFunc("GET /api/public/openapi.json", serveOpenAPI)
	mux.HandleFunc("GET /healthz", func(w http.ResponseWriter, _ *http.Request) {
		writeJSON(w, http.StatusOK, HealthResponse{Status: "ok"})
	})
	mux.HandleFunc("GET /readyz", func(w http.ResponseWriter, _ *http.Request) {
		if deps.Ready != nil && !deps.Ready() {
			writeProblem(w, newProblem(http.StatusServiceUnavailable, "https://panel.dev/problems/not-ready", "Service unavailable", "the service is not ready"))
			return
		}
		writeJSON(w, http.StatusOK, HealthResponse{Status: "ok"})
	})
	mux.HandleFunc("GET /metrics", func(w http.ResponseWriter, _ *http.Request) {
		w.Header().Set("Content-Type", "text/plain; version=0.0.4; charset=utf-8")
		health.WritePrometheus(w)
	})

	return requestID(recoverProblems(mux))
}

func registerStats(mux *http.ServeMux, prefix string, h *handlers) {
	mux.HandleFunc("GET "+prefix+"/stats/summary", h.statsSummary)
	mux.HandleFunc("GET "+prefix+"/stats/timeseries", h.statsTimeseries)
	mux.HandleFunc("GET "+prefix+"/stats/breakdown", h.statsBreakdown)
}

func requestID(next http.Handler) http.Handler {
	return http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		var id [16]byte
		if _, err := rand.Read(id[:]); err != nil {
			copy(id[:], []byte(strconv.FormatInt(time.Now().UnixNano(), 10)))
		}
		w.Header().Set("X-Request-ID", hex.EncodeToString(id[:]))
		next.ServeHTTP(w, r)
	})
}

func recoverProblems(next http.Handler) http.Handler {
	return http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		defer func() {
			if recover() != nil {
				writeProblem(w, internalProblem())
			}
		}()
		next.ServeHTTP(w, r)
	})
}

func authenticate(auth Authenticator, registry sites.Registry, next http.Handler) http.Handler {
	return http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		if auth == nil {
			writeProblem(w, unauthorized())
			return
		}
		principal, ok := auth.Authenticate(r)
		if !ok {
			writeProblem(w, unauthorized())
			return
		}
		ctx := context.WithValue(r.Context(), principalContextKey{}, principal)
		ctx = context.WithValue(ctx, registryContextKey{}, registry)
		next.ServeHTTP(w, r.WithContext(ctx))
	})
}

func rateLimit(limiter ingest.RateLimiter, next http.Handler) http.Handler {
	return http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		reset := time.Now().Add(time.Minute).UTC().Format(time.RFC3339)
		w.Header().Set("RateLimit-Limit", "120")
		w.Header().Set("RateLimit-Reset", reset)
		keyHash := sha256.Sum256([]byte(r.Header.Get("Authorization")))
		if limiter != nil && !limiter.Allow(hex.EncodeToString(keyHash[:])) {
			w.Header().Set("RateLimit-Remaining", "0")
			w.Header().Set("Retry-After", "1")
			writeProblem(w, rateLimited())
			return
		}
		w.Header().Set("RateLimit-Remaining", "1")
		next.ServeHTTP(w, r)
	})
}

func requireCSRF(next http.Handler) http.Handler {
	return http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		if r.Method != http.MethodGet && r.Method != http.MethodHead && r.Method != http.MethodOptions && !validCSRF(r) {
			writeProblem(w, forbidden("a CSRF token is required"))
			return
		}
		next.ServeHTTP(w, r)
	})
}

func authorizeRequest(w http.ResponseWriter, r *http.Request, siteID int32, scope string) bool {
	principal, ok := r.Context().Value(principalContextKey{}).(Principal)
	if !ok {
		writeProblem(w, unauthorized())
		return false
	}
	if !principal.hasScope(scope) {
		writeProblem(w, forbidden("the credential does not grant the required scope"))
		return false
	}
	if siteID != 0 {
		// Fail closed: without a registry, or for unknown or foreign sites, answer 404
		// so the existence of other organizations' sites is not revealed.
		registry, _ := r.Context().Value(registryContextKey{}).(sites.Registry)
		if registry == nil && !principal.AllSites {
			writeProblem(w, notFound("site not found"))
			return false
		}
		if registry != nil {
			site, found := registry.Site(siteID)
			if !found || !principal.allowsSite(site) {
				writeProblem(w, notFound("site not found"))
				return false
			}
		}
	}
	return true
}

func serveOpenAPI(w http.ResponseWriter, _ *http.Request) {
	source, err := contractFS.ReadFile("openapi.yaml")
	if err != nil {
		writeProblem(w, internalProblem())
		return
	}
	var document any
	if err := yaml.Unmarshal(source, &document); err != nil {
		writeProblem(w, internalProblem())
		return
	}
	w.Header().Set("Content-Type", "application/json")
	_ = json.NewEncoder(w).Encode(document)
}

type registryContextKey struct{}

// validCSRF implements the double-submit check: the X-CSRF-Token header must
// equal the panel_csrf cookie, compared in constant time.
func validCSRF(r *http.Request) bool {
	header := r.Header.Get("X-CSRF-Token")
	cookie, err := r.Cookie("panel_csrf")
	if header == "" || err != nil || cookie.Value == "" {
		return false
	}
	return subtle.ConstantTimeCompare([]byte(header), []byte(cookie.Value)) == 1
}
