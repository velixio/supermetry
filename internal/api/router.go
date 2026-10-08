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
	"path"
	"strconv"
	"time"

	"go.yaml.in/yaml/v3"

	"panel/internal/config"
	"panel/internal/dict"
	"panel/internal/ingest"
	"panel/internal/session"
	"panel/internal/sites"
	"panel/internal/tracker"
)

const (
	publicPrefix  = "/api/public/v1"
	privatePrefix = "/api/private"
)

//go:embed openapi.yaml
var contractFS embed.FS

type principalContextKey struct{}

type dictionaryResolver interface {
	Value(dict.Kind, int32) (string, bool)
}

// Deps contains the services exposed by the analytics HTTP router.
type Deps struct {
	Config         config.Config
	Query          queryService
	SessionExplore sessionExploreService
	Heatmaps       heatmapService
	Ingest         http.Handler
	PublicAuth     Authenticator
	PrivateAuth    Authenticator
	Sites          sites.Registry // resolves site ownership for every site-scoped request
	UI             http.Handler   // the embedded dashboard; serves every GET that no API route claims
	RateLimiter    ingest.RateLimiter
	Sessions       session.Store
	Dictionary     dictionaryResolver
	// Platform serves the application routes implemented under
	// internal/platform/http. Those handlers resolve the complete platform
	// principal required by authz.Can and transactional audit records.
	Platform http.Handler
	Now      func() time.Time
	Ready    func() bool
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

	h := &handlers{query: deps.Query, sessions: deps.SessionExplore, heatmaps: deps.Heatmaps, now: deps.Now}
	explore, hasExplore := deps.Query.(exploreService)
	mux := http.NewServeMux()

	public := http.NewServeMux()
	registerStats(public, publicPrefix, h)
	if hasExplore {
		registerExplore(public, publicPrefix, explore)
	}
	registerSessionExplore(public, publicPrefix, h)
	public.Handle("POST "+publicPrefix+"/mcp", NewMCPHandler(MCPDeps{
		Query: deps.Query, Resources: deps.Platform, Sessions: deps.Sessions, Now: deps.Now,
		Dictionary: deps.Dictionary,
	}))
	public.HandleFunc("POST "+publicPrefix+"/query", h.queryRequest)
	public.HandleFunc("POST "+publicPrefix+"/heatmaps/query", h.heatmapRequest)
	public.HandleFunc("POST "+publicPrefix+"/vitals/query", h.vitalsRequest)
	public.HandleFunc("POST "+publicPrefix+"/funnels/query", h.funnelRequest)
	public.HandleFunc("POST "+publicPrefix+"/journeys/query", h.journeyRequest)
	public.HandleFunc("POST "+publicPrefix+"/retention/query", h.retentionRequest)
	public.HandleFunc("GET "+publicPrefix+"/realtime", realtimeHandler(deps.Sessions, deps.Now, deps.Dictionary))
	public.HandleFunc(publicPrefix+"/", func(w http.ResponseWriter, _ *http.Request) { writeProblem(w, notFound("API route not found")) })
	mux.Handle(publicPrefix+"/", authenticate(deps.PublicAuth, deps.Sites, rateLimit(deps.RateLimiter, public)))

	private := http.NewServeMux()
	registerStats(private, privatePrefix, h)
	if hasExplore {
		registerExplore(private, privatePrefix, explore)
	}
	registerSessionExplore(private, privatePrefix, h)
	private.HandleFunc("POST "+privatePrefix+"/query", h.queryRequest)
	private.HandleFunc("POST "+privatePrefix+"/heatmaps/query", h.heatmapRequest)
	private.HandleFunc("POST "+privatePrefix+"/vitals/query", h.vitalsRequest)
	private.HandleFunc("POST "+privatePrefix+"/funnels/query", h.funnelRequest)
	private.HandleFunc("POST "+privatePrefix+"/journeys/query", h.journeyRequest)
	private.HandleFunc("POST "+privatePrefix+"/retention/query", h.retentionRequest)
	private.HandleFunc("GET "+privatePrefix+"/realtime", realtimeHandler(deps.Sessions, deps.Now, deps.Dictionary))
	private.HandleFunc("GET "+privatePrefix+"/health", dataHealthHandler)
	private.HandleFunc(privatePrefix+"/", func(w http.ResponseWriter, _ *http.Request) { writeProblem(w, notFound("API route not found")) })
	mux.Handle(privatePrefix+"/", authenticate(deps.PrivateAuth, deps.Sites, requireCSRF(private)))
	registerPlatformRoutes(mux, deps.Platform, deps.RateLimiter)

	if deps.Ingest != nil && deps.Config.EventPath != "" {
		mux.Handle("POST "+deps.Config.EventPath, deps.Ingest)
	}
	if deps.Config.TrackerPath != "" {
		mux.Handle("GET "+deps.Config.TrackerPath, tracker.Handler())
		mux.Handle("GET "+tracker.FullPath(deps.Config.TrackerPath), tracker.FullHandler())
		if deps.Sites != nil {
			mux.Handle("GET "+path.Dir(deps.Config.TrackerPath)+"/", tracker.SiteHandler(deps.Sites))
		}
	}
	mux.HandleFunc("GET /api/public/openapi.json", serveOpenAPI)
	if deps.UI != nil {
		// Least specific pattern: API, intake, tracker and ops routes above take precedence.
		mux.Handle("/", http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
			if r.Method != http.MethodGet && r.Method != http.MethodHead {
				http.NotFound(w, r)
				return
			}
			deps.UI.ServeHTTP(w, r)
		}))
	}
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
	mux.HandleFunc("GET /metrics", metricsHandler(deps.Config))

	return requestID(recoverProblems(mux))
}

func registerPlatformRoutes(mux *http.ServeMux, platform http.Handler, limiter ingest.RateLimiter) {
	if platform == nil {
		return
	}
	public := rateLimit(limiter, platform)
	for _, pattern := range []string{
		"GET " + publicPrefix + "/api-keys",
		"POST " + publicPrefix + "/api-keys",
		"DELETE " + publicPrefix + "/api-keys/{key}",
		"GET " + publicPrefix + "/sites",
		"POST " + publicPrefix + "/sites",
		"GET " + publicPrefix + "/sites/{site}",
		"PATCH " + publicPrefix + "/sites/{site}",
		"DELETE " + publicPrefix + "/sites/{site}",
		"GET " + publicPrefix + "/sites/{site}/goals",
		"POST " + publicPrefix + "/sites/{site}/goals",
		"PATCH " + publicPrefix + "/goals/{goal}",
		"DELETE " + publicPrefix + "/goals/{goal}",
		"GET " + publicPrefix + "/sites/{site}/annotations",
		"POST " + publicPrefix + "/sites/{site}/annotations",
		"PATCH " + publicPrefix + "/annotations/{annotation}",
		"DELETE " + publicPrefix + "/annotations/{annotation}",
		"GET " + publicPrefix + "/sites/{site}/funnels",
		"POST " + publicPrefix + "/sites/{site}/funnels",
		"PATCH " + publicPrefix + "/funnels/{funnel}",
		"DELETE " + publicPrefix + "/funnels/{funnel}",
		"GET " + publicPrefix + "/sites/{site}/segments",
		"POST " + publicPrefix + "/sites/{site}/segments",
		"PATCH " + publicPrefix + "/segments/{segment}",
		"DELETE " + publicPrefix + "/segments/{segment}",
		"GET " + publicPrefix + "/sites/{site}/profiles",
		"GET " + publicPrefix + "/profiles/{profile}",
	} {
		mux.Handle(pattern, public)
	}
	for _, pattern := range []string{
		"GET " + privatePrefix + "/auth/config",
		"POST " + privatePrefix + "/auth/login",
		"POST " + privatePrefix + "/auth/signup",
		"POST " + privatePrefix + "/auth/logout",
		"POST " + privatePrefix + "/auth/password-reset",
		"POST " + privatePrefix + "/auth/password-reset/confirm",
		"GET " + privatePrefix + "/auth/email-change/confirm",
		"POST " + privatePrefix + "/auth/email-change/confirm",
		"POST " + privatePrefix + "/setup",
		"GET " + privatePrefix + "/me",
		"PATCH " + privatePrefix + "/me",
		"DELETE " + privatePrefix + "/me",
		"POST " + privatePrefix + "/me/email",
		"POST " + privatePrefix + "/me/password",
		"GET " + privatePrefix + "/me/ip",
		"GET " + privatePrefix + "/me/sessions",
		"DELETE " + privatePrefix + "/me/sessions/{session}",
		"POST " + privatePrefix + "/me/totp",
		"PATCH " + privatePrefix + "/me/totp",
		"GET " + privatePrefix + "/orgs",
		"GET " + privatePrefix + "/orgs/{org}",
		"GET " + privatePrefix + "/orgs/{org}/members",
		"PATCH " + privatePrefix + "/orgs/{org}/members/{user}",
		"DELETE " + privatePrefix + "/orgs/{org}/members/{user}",
		"POST " + privatePrefix + "/orgs/{org}/invitations",
		"GET " + privatePrefix + "/orgs/{org}/invitations",
		"DELETE " + privatePrefix + "/orgs/{org}/invitations/{invitation}",
		"GET " + privatePrefix + "/orgs/{org}/api-keys",
		"DELETE " + privatePrefix + "/orgs/{org}/api-keys/{key}",
		"POST " + privatePrefix + "/sites/{site}/shares",
		"GET " + privatePrefix + "/sites/{site}/shares",
		"DELETE " + privatePrefix + "/sites/{site}/shares/{share}",
		"POST " + privatePrefix + "/sites/{site}/reports",
		"GET " + privatePrefix + "/sites/{site}/reports",
		"DELETE " + privatePrefix + "/sites/{site}/reports/{report}",
		"GET " + privatePrefix + "/orgs/{org}/channels",
		"POST " + privatePrefix + "/orgs/{org}/channels",
		"PATCH " + privatePrefix + "/orgs/{org}/channels/{channel}",
		"DELETE " + privatePrefix + "/orgs/{org}/channels/{channel}",
		"POST " + privatePrefix + "/orgs/{org}/channels/{channel}/test",
		"GET " + privatePrefix + "/sites/{site}/alerts",
		"POST " + privatePrefix + "/sites/{site}/alerts",
		"PATCH " + privatePrefix + "/sites/{site}/alerts/{alert}",
		"DELETE " + privatePrefix + "/sites/{site}/alerts/{alert}",
		"POST " + privatePrefix + "/invitations/{token}/accept",
		"POST " + privatePrefix + "/share/{slug}/open",
		"GET " + privatePrefix + "/sites",
		"POST " + privatePrefix + "/sites",
		"GET " + privatePrefix + "/sites/{site}",
		"GET " + privatePrefix + "/sites/{site}/snippet",
		"POST " + privatePrefix + "/sites/{site}/icon/refetch",
		"PATCH " + privatePrefix + "/sites/{site}",
		"DELETE " + privatePrefix + "/sites/{site}",
		"POST " + privatePrefix + "/orgs/{org}/api-keys",
		"GET " + privatePrefix + "/orgs/{org}/audit",
		"DELETE " + privatePrefix + "/orgs/{org}",
		"GET " + privatePrefix + "/share/{slug}/stats/summary",
		"GET " + privatePrefix + "/sites/{site}/goals",
		"POST " + privatePrefix + "/sites/{site}/goals",
		"PATCH " + privatePrefix + "/goals/{goal}",
		"DELETE " + privatePrefix + "/goals/{goal}",
		"GET " + privatePrefix + "/sites/{site}/annotations",
		"POST " + privatePrefix + "/sites/{site}/annotations",
		"PATCH " + privatePrefix + "/annotations/{annotation}",
		"DELETE " + privatePrefix + "/annotations/{annotation}",
		"GET " + privatePrefix + "/sites/{site}/funnels",
		"POST " + privatePrefix + "/sites/{site}/funnels",
		"PATCH " + privatePrefix + "/funnels/{funnel}",
		"DELETE " + privatePrefix + "/funnels/{funnel}",
		"GET " + privatePrefix + "/sites/{site}/segments",
		"POST " + privatePrefix + "/sites/{site}/segments",
		"PATCH " + privatePrefix + "/segments/{segment}",
		"DELETE " + privatePrefix + "/segments/{segment}",
		"POST " + privatePrefix + "/sites/{site}/imports",
		"GET " + privatePrefix + "/sites/{site}/imports",
		"DELETE " + privatePrefix + "/imports/{import}",
		"GET " + privatePrefix + "/sites/{site}/export",
		"GET " + privatePrefix + "/sites/{site}/profiles",
		"GET " + privatePrefix + "/profiles/{profile}",
		"DELETE " + privatePrefix + "/profiles/{profile}",
	} {
		mux.Handle(pattern, platform)
	}
}

func registerStats(mux *http.ServeMux, prefix string, h *handlers) {
	mux.HandleFunc("GET "+prefix+"/stats/summary", h.statsSummary)
	mux.HandleFunc("GET "+prefix+"/stats/timeseries", h.statsTimeseries)
	mux.HandleFunc("GET "+prefix+"/stats/breakdown", h.statsBreakdown)
}

func registerSessionExplore(mux *http.ServeMux, prefix string, h *handlers) {
	mux.HandleFunc("GET "+prefix+"/sites/{site}/sessions", h.listSessions)
	mux.HandleFunc("GET "+prefix+"/sessions/{session}", h.getSession)
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
	if siteID != 0 && !siteAllowed(r, principal, siteID) {
		writeProblem(w, notFound("site not found"))
		return false
	}
	return true
}

// siteAllowed reports whether principal may access siteID. It fails closed:
// without a registry, or for unknown or foreign sites, callers answer "not
// found" so the existence of other organizations' sites is not revealed.
func siteAllowed(r *http.Request, principal Principal, siteID int32) bool {
	registry, _ := r.Context().Value(registryContextKey{}).(sites.Registry)
	if registry == nil {
		return principal.AllSites
	}
	site, found := registry.Site(siteID)
	return found && principal.allowsSite(site)
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
