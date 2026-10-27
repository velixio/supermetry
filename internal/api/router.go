package api

import (
	"context"
	"crypto/rand"
	"crypto/sha256"
	"crypto/subtle"
	"embed"
	"encoding/hex"
	"encoding/json"
	"math"
	"net/http"
	"net/netip"
	"path"
	"sort"
	"strconv"
	"strings"
	"sync"
	"time"

	"go.yaml.in/yaml/v3"

	"supermetry/internal/clientip"
	"supermetry/internal/config"
	"supermetry/internal/dict"
	"supermetry/internal/dropstats"
	"supermetry/internal/ingest"
	"supermetry/internal/links"
	"supermetry/internal/opencounters"
	"supermetry/internal/privacy"
	"supermetry/internal/realtime"
	"supermetry/internal/replay"
	"supermetry/internal/routehits"
	"supermetry/internal/session"
	"supermetry/internal/sites"
	"supermetry/internal/tracker"
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

// RollupBackfillProgressFunc loads cluster-wide progress from PostgreSQL.
type RollupBackfillProgressFunc func(context.Context) (running bool, daysDone, totalDays int, err error)

// FailedProfileErasuresFunc reports durable erasure requests that exhausted
// their retries and need an administrator's attention.
type FailedProfileErasuresFunc func(context.Context) (int64, error)

type DropStatsReader interface {
	Read(context.Context, []dropstats.SiteDay) ([]dropstats.SiteSummary, error)
}

// Deps contains the services exposed by the analytics HTTP router.
type Deps struct {
	Config         config.Config
	Version        string
	SchemaVersion  int64
	Query          queryService
	SessionExplore sessionExploreService
	Revenue        revenueService
	Cohorts        cohortService
	Heatmaps       heatmapService
	Ingest         http.Handler
	// Links serves the public short-link redirect and tracking pixel. Optional.
	Links  *links.Handler
	Pixels http.Handler
	// GitHubWebhook receives repository star webhooks, one URL per tracked repository. Nil leaves the route off.
	GitHubWebhook http.Handler
	// GitHubStarCount serves the public star count of a tracked repository. Nil leaves the route off.
	GitHubStarCount     http.Handler
	OpenCounters        *opencounters.Handler
	OpenCountersEnabled bool
	Replay              http.Handler
	ReplayCSS           http.Handler
	PublicAuth          Authenticator
	PrivateAuth         Authenticator
	Sites               sites.Registry // resolves site ownership for every site-scoped request
	UI                  http.Handler   // the embedded dashboard; serves every GET that no API route claims
	RateLimiter         ingest.RateLimiter
	Sessions            session.Store
	MinuteSource        realtime.MinuteSource
	Dictionary          dictionaryResolver
	// Platform serves the application routes implemented under
	// internal/platform/http. Those handlers resolve the complete platform
	// principal required by authz.Can and transactional audit records.
	Platform               http.Handler
	Now                    func() time.Time
	RateLimitNow           func() time.Time
	Ready                  func() bool
	RollupBackfillProgress RollupBackfillProgressFunc
	FailedProfileErasures  FailedProfileErasuresFunc
	DropStats              DropStatsReader
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
	now := deps.Now
	if now == nil {
		now = time.Now
	}
	rateLimitNow := deps.RateLimitNow
	if rateLimitNow == nil {
		// Security throttling uses wall time rather than the injectable analytics
		// clock, which may intentionally be fixed for historical queries.
		rateLimitNow = time.Now
	}
	apiLimiter := newAPITokenBuckets(2, 30, rateLimitNow)

	queries := deps.Query
	if deps.Config.Tracking.Mode != privacy.Identified && queries != nil {
		queries = anonymousQueries{queryService: queries, paths: deps.Config.TrackingPaths}
	}
	h := &handlers{query: queries, sessions: deps.SessionExplore, revenue: deps.Revenue, cohorts: deps.Cohorts, heatmaps: deps.Heatmaps, now: deps.Now}
	explore, hasExplore := deps.Query.(exploreService)
	mux := http.NewServeMux()

	public := http.NewServeMux()
	registerStats(public, publicPrefix, h)
	if hasExplore {
		registerExplore(public, publicPrefix, explore)
	}
	registerSessionExplore(public, publicPrefix, h)
	mcp := NewMCPHandler(MCPDeps{
		Query: queries, TrackingMode: deps.Config.Tracking.Mode, TrackingPaths: deps.Config.TrackingPaths, Resources: deps.Platform, Sessions: deps.Sessions, Now: deps.Now,
		Dictionary: deps.Dictionary, MinuteSource: deps.MinuteSource,
	})
	mux.Handle("POST "+publicPrefix+"/mcp", authenticateRateLimited(deps.PublicAuth, deps.Sites, deps.RateLimiter, apiLimiter, deps.Config.TrustedProxies, rateLimitNow, true, mcp))
	public.HandleFunc("POST "+publicPrefix+"/query", h.queryRequest)
	public.HandleFunc("POST "+publicPrefix+"/heatmaps/query", h.heatmapRequest)
	public.HandleFunc("POST "+publicPrefix+"/vitals/query", h.vitalsRequest)
	public.HandleFunc("POST "+publicPrefix+"/funnels/query", h.funnelRequest)
	public.HandleFunc("POST "+publicPrefix+"/journeys/query", h.journeyRequest)
	public.HandleFunc("POST "+publicPrefix+"/retention/query", h.retentionRequest)
	public.HandleFunc("GET "+publicPrefix+"/realtime", realtimeHandler(deps.Sessions, deps.MinuteSource, deps.Now, deps.Dictionary))
	public.HandleFunc("GET "+publicPrefix+"/realtime/summary", realtimeSummaryHandler(deps.Sessions, deps.Now))
	public.HandleFunc(publicPrefix+"/", func(w http.ResponseWriter, _ *http.Request) { writeProblem(w, notFound("API route not found")) })
	mux.Handle(publicPrefix+"/", authenticateRateLimited(deps.PublicAuth, deps.Sites, deps.RateLimiter, apiLimiter, deps.Config.TrustedProxies, rateLimitNow, false, trackingReads(deps.Config.Tracking.Mode, routehits.WrapFile(public, routehits.File(deps.Config.RouteHitsFile)))))

	private := http.NewServeMux()
	registerStats(private, privatePrefix, h)
	if hasExplore {
		registerExplore(private, privatePrefix, explore)
	}
	registerSessionExplore(private, privatePrefix, h)
	private.HandleFunc("GET "+privatePrefix+"/sites/{site}/revenue/report", h.revenueReport)
	private.HandleFunc("GET "+privatePrefix+"/sites/{site}/revenue/customers", h.revenueCustomers)
	private.HandleFunc("POST "+privatePrefix+"/sites/{site}/cohorts/evaluate", h.evaluateCohort)
	private.HandleFunc("GET "+privatePrefix+"/sites/{site}/power-users", h.listPowerUsers)
	private.HandleFunc("POST "+privatePrefix+"/query", h.queryRequest)
	private.HandleFunc("POST "+privatePrefix+"/heatmaps/query", h.heatmapRequest)
	private.HandleFunc("POST "+privatePrefix+"/vitals/query", h.vitalsRequest)
	private.HandleFunc("POST "+privatePrefix+"/funnels/query", h.funnelRequest)
	private.HandleFunc("POST "+privatePrefix+"/journeys/query", h.journeyRequest)
	private.HandleFunc("POST "+privatePrefix+"/retention/query", h.retentionRequest)
	private.HandleFunc("GET "+privatePrefix+"/realtime", realtimeHandler(deps.Sessions, deps.MinuteSource, deps.Now, deps.Dictionary))
	private.HandleFunc("GET "+privatePrefix+"/realtime/summary", realtimeSummaryHandler(deps.Sessions, deps.Now))
	private.HandleFunc("GET "+privatePrefix+"/health", dataHealthHandler(deps.RollupBackfillProgress, deps.FailedProfileErasures, deps.DropStats, deps.Sites, now))
	private.HandleFunc(privatePrefix+"/", func(w http.ResponseWriter, _ *http.Request) { writeProblem(w, notFound("API route not found")) })
	mux.Handle(privatePrefix+"/", authenticate(deps.PrivateAuth, deps.Sites, requireCSRF(trackingReads(deps.Config.Tracking.Mode, routehits.WrapFile(private, routehits.File(deps.Config.RouteHitsFile))))))
	registerPlatformRoutes(mux, deps.Platform, deps.PublicAuth, deps.Sites, deps.RateLimiter, apiLimiter, deps.Config.TrustedProxies, rateLimitNow)

	if deps.Ingest != nil && deps.Config.EventPath != "" {
		ingest := trackerCORS(deps.Ingest)
		mux.Handle("POST "+deps.Config.EventPath, ingest)
		mux.Handle("OPTIONS "+deps.Config.EventPath, ingest)
	}
	if deps.Links != nil {
		mux.HandleFunc("GET /l/{slug}", deps.Links.ServeRedirect)
		if deps.Pixels != nil {
			mux.Handle("GET /px/{file}", deps.Pixels)
		} else {
			mux.HandleFunc("GET /px/{file}", deps.Links.ServePixel)
		}
	}
	if deps.GitHubWebhook != nil {
		mux.Handle("POST /api/github/webhook/{public_id}", deps.GitHubWebhook)
	}
	if deps.GitHubStarCount != nil {
		mux.Handle("GET /api/github/stars/{owner}/{name}", deps.GitHubStarCount)
		mux.Handle("OPTIONS /api/github/stars/{owner}/{name}", deps.GitHubStarCount)
	}
	if deps.OpenCounters != nil && deps.OpenCountersEnabled {
		mux.Handle("GET /c/{rest...}", deps.OpenCounters)
	} else {
		mux.HandleFunc("GET /c/{rest...}", http.NotFound)
	}
	if deps.Config.Replay.Enabled && deps.Replay != nil {
		mux.Handle("POST /api/replay/start", deps.Replay)
		mux.Handle("OPTIONS /api/replay/start", deps.Replay)
		mux.Handle("POST /api/replay/chunk", deps.Replay)
		mux.Handle("OPTIONS /api/replay/chunk", deps.Replay)
	}
	if deps.Config.Replay.Enabled && deps.ReplayCSS != nil {
		mux.Handle("PUT /api/replay/css/{hash}", deps.ReplayCSS)
		mux.Handle("OPTIONS /api/replay/css/{hash}", deps.ReplayCSS)
	}
	if deps.Config.TrackerPath != "" {
		mux.Handle("GET "+deps.Config.TrackerPath, tracker.Handler())
		mux.Handle("GET "+tracker.FullPath(deps.Config.TrackerPath), tracker.FullHandler())
		if deps.Sites != nil {
			tracker.SetEventPath(deps.Config.EventPath)
			tracker.SetScriptDir(path.Dir(deps.Config.TrackerPath))
			mux.Handle("GET "+path.Dir(deps.Config.TrackerPath)+"/", tracker.SiteHandlerWithPolicy(deps.Sites, deps.Config.Replay, deps.Config.Tracking.Mode))
		}
	}
	mux.HandleFunc("GET /replay.js", func(w http.ResponseWriter, request *http.Request) {
		if !deps.Config.Replay.Enabled {
			http.NotFound(w, request)
			return
		}
		body, etag := replay.ReplayJS()
		w.Header().Set("Content-Type", "application/javascript")
		w.Header().Set("Cache-Control", "public, max-age=86400")
		w.Header().Set("ETag", etag)
		if request.Header.Get("If-None-Match") == etag {
			w.WriteHeader(http.StatusNotModified)
			return
		}
		w.Header().Set("Content-Length", strconv.Itoa(len(body)))
		if request.Method != http.MethodHead {
			_, _ = w.Write(body)
		}
	})
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
	healthResponse := func() HealthResponse {
		response := HealthResponse{Status: "ok"}
		if deps.Version != "" {
			response.Version = &deps.Version
			response.SchemaVersion = &deps.SchemaVersion
		}
		return response
	}
	mux.HandleFunc("GET /healthz", func(w http.ResponseWriter, _ *http.Request) {
		writeJSON(w, http.StatusOK, healthResponse())
	})
	mux.HandleFunc("GET /readyz", func(w http.ResponseWriter, _ *http.Request) {
		if deps.Ready != nil && !deps.Ready() {
			writeProblem(w, newProblem(http.StatusServiceUnavailable, "https://supermetry.com/problems/not-ready", "Service unavailable", "the service is not ready"))
			return
		}
		writeJSON(w, http.StatusOK, healthResponse())
	})
	mux.HandleFunc("GET /metrics", metricsHandler(deps.Config))

	return requestID(recoverProblems(routehits.WrapFile(mux, routehits.File(deps.Config.RouteHitsFile))))
}

func trackerCORS(next http.Handler) http.Handler {
	return http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		w.Header().Set("Access-Control-Allow-Origin", "*")
		w.Header().Set("Access-Control-Allow-Methods", "POST, OPTIONS")
		w.Header().Set("Access-Control-Allow-Headers", "Content-Type")
		if r.Method == http.MethodOptions {
			w.WriteHeader(http.StatusNoContent)
			return
		}
		next.ServeHTTP(w, r)
	})
}

func registerPlatformRoutes(mux *http.ServeMux, platform http.Handler, auth Authenticator, registry sites.Registry, keyedLimiter ingest.RateLimiter, anonymousLimiter *apiTokenBuckets, trustedProxies []netip.Prefix, now func() time.Time) {
	if platform == nil {
		return
	}
	mux.Handle("GET "+privatePrefix+"/setup/status", anonymousRateLimited(anonymousLimiter, trustedProxies, platform))
	public := authenticateRateLimited(auth, registry, keyedLimiter, anonymousLimiter, trustedProxies, now, false, platform)
	for _, pattern := range []string{
		"PUT " + privatePrefix + "/sites/{site}/replays/{session}/saved",
		"DELETE " + privatePrefix + "/sites/{site}/replays/{session}/saved",
	} {
		mux.Handle(pattern, anonymousRateLimited(anonymousLimiter, trustedProxies, platform))
	}
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
		"GET " + publicPrefix + "/erasures/{erasure}",
	} {
		mux.Handle(pattern, public)
	}
	for _, pattern := range []string{
		"GET " + privatePrefix + "/admin/users",
		"POST " + privatePrefix + "/admin/users",
		"PATCH " + privatePrefix + "/admin/users/{user}",
		"POST " + privatePrefix + "/admin/users/{user}/impersonate",
		"GET " + privatePrefix + "/admin/organizations",
		"GET " + privatePrefix + "/admin/organizations/{org}",
		"DELETE " + privatePrefix + "/admin/organizations/{org}",
		"GET " + privatePrefix + "/admin/organizations/{org}/github-star-repo-limit",
		"PUT " + privatePrefix + "/admin/organizations/{org}/github-star-repo-limit",
		"DELETE " + privatePrefix + "/admin/organizations/{org}/github-star-repo-limit",
		"POST " + privatePrefix + "/admin/organizations/{org}/restore",
		"GET " + privatePrefix + "/admin/sites",
		"PATCH " + privatePrefix + "/admin/sites/{site_id}",
		"GET " + privatePrefix + "/admin/settings",
		"PATCH " + privatePrefix + "/admin/settings",
		"GET " + privatePrefix + "/admin/security/2fa-policy",
		"PUT " + privatePrefix + "/admin/security/2fa-policy",
		"PUT " + privatePrefix + "/admin/security/2fa-exemptions/{user}",
		"DELETE " + privatePrefix + "/admin/security/2fa-exemptions/{user}",
		"GET " + privatePrefix + "/admin/audit",
		"GET " + privatePrefix + "/admin/update",
		"POST " + privatePrefix + "/admin/update/check",
		"POST " + privatePrefix + "/admin/update/apply",
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
		"GET " + privatePrefix + "/me/orgs/deleted",
		"POST " + privatePrefix + "/me/orgs/{org}/restore",
		"POST " + privatePrefix + "/me/email",
		"POST " + privatePrefix + "/me/password",
		"POST " + privatePrefix + "/me/demo-prompt/dismiss",
		"GET " + privatePrefix + "/me/ip",
		"GET " + privatePrefix + "/me/sessions",
		"DELETE " + privatePrefix + "/me/sessions",
		"DELETE " + privatePrefix + "/me/sessions/{session}",
		"POST " + privatePrefix + "/me/totp",
		"PATCH " + privatePrefix + "/me/totp",
		"DELETE " + privatePrefix + "/me/totp",
		"POST " + privatePrefix + "/me/totp/recovery-codes",
		"GET " + privatePrefix + "/sites/{site}/purge-requests",
		"POST " + privatePrefix + "/sites/{site}/purge-requests",
		"GET " + privatePrefix + "/orgs",
		"POST " + privatePrefix + "/orgs",
		"POST " + privatePrefix + "/orgs/{org}/demo",
		"POST " + privatePrefix + "/sites/{site}/demo/heartbeat",
		"GET " + privatePrefix + "/orgs/{org}/github-stars/repos",
		"POST " + privatePrefix + "/orgs/{org}/github-stars/repos",
		"PATCH " + privatePrefix + "/orgs/{org}/github-stars/repos/{id}",
		"DELETE " + privatePrefix + "/orgs/{org}/github-stars/repos/{id}",
		"POST " + privatePrefix + "/orgs/{org}/github-stars/repos/{id}/rotate-secret",
		"POST " + privatePrefix + "/orgs/{org}/github-stars/repos/{id}/sync",
		"GET " + privatePrefix + "/orgs/{org}/github-stars/repos/{id}/trending",
		"GET " + privatePrefix + "/orgs/{org}/github-stars/repos/{id}/alerts",
		"PUT " + privatePrefix + "/orgs/{org}/github-stars/repos/{id}/alerts",
		"POST " + privatePrefix + "/orgs/{org}/github-stars/repos/{id}/alerts/test",
		"GET " + privatePrefix + "/orgs/{org}/github-stars/summary",
		"GET " + privatePrefix + "/orgs/{org}/github-stars/series",
		"GET " + privatePrefix + "/orgs/{org}/github-stars/events",
		"GET " + privatePrefix + "/orgs/{org}/github-stars/annotations",
		"POST " + privatePrefix + "/orgs/{org}/github-stars/annotations",
		"DELETE " + privatePrefix + "/orgs/{org}/github-stars/annotations/{annotation}",
		"DELETE " + privatePrefix + "/orgs/{org}/github-stars/users/{login}",
		"GET " + privatePrefix + "/orgs/{org}",
		"PATCH " + privatePrefix + "/orgs/{org}",
		"GET " + privatePrefix + "/orgs/{org}/sites/deleted",
		"GET " + privatePrefix + "/orgs/{org}/members",
		"PATCH " + privatePrefix + "/orgs/{org}/members/{user}",
		"DELETE " + privatePrefix + "/orgs/{org}/members/{user}",
		"DELETE " + privatePrefix + "/orgs/{org}/membership",
		"POST " + privatePrefix + "/orgs/{org}/invitations",
		"GET " + privatePrefix + "/orgs/{org}/invitations",
		"DELETE " + privatePrefix + "/orgs/{org}/invitations/{invitation}",
		"GET " + privatePrefix + "/orgs/{org}/api-keys",
		"DELETE " + privatePrefix + "/orgs/{org}/api-keys/{key}",
		"POST " + privatePrefix + "/sites/{site}/shares",
		"GET " + privatePrefix + "/sites/{site}/shares",
		"PATCH " + privatePrefix + "/sites/{site}/shares/{share}",
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
		"GET " + privatePrefix + "/sites/{site}/notification-history",
		"POST " + privatePrefix + "/sites/{site}/notification-history/{delivery}/retry",
		"GET " + privatePrefix + "/orgs/{org}/notification-templates",
		"PUT " + privatePrefix + "/orgs/{org}/notification-templates/{kind}",
		"DELETE " + privatePrefix + "/orgs/{org}/notification-templates/{kind}",
		"POST " + privatePrefix + "/orgs/{org}/notification-templates/{kind}/preview",
		"PUT " + privatePrefix + "/orgs/{org}/notification-settings",
		"GET " + privatePrefix + "/sites/{site}/integrations",
		"POST " + privatePrefix + "/sites/{site}/integrations/{provider}/connect",
		"POST " + privatePrefix + "/sites/{site}/integrations/{provider}/backfill",
		"DELETE " + privatePrefix + "/sites/{site}/integrations/{provider}",
		"GET " + privatePrefix + "/integrations/{provider}/callback",
		"GET " + privatePrefix + "/sites/{site}/search-console/top",
		"GET " + privatePrefix + "/invitations/{token}",
		"POST " + privatePrefix + "/invitations/{token}/accept",
		"POST " + privatePrefix + "/share/{slug}/open",
		"GET " + privatePrefix + "/sites",
		"GET " + privatePrefix + "/sites/overview",
		"POST " + privatePrefix + "/sites",
		"GET " + privatePrefix + "/sites/{site}",
		"GET " + privatePrefix + "/sites/{site}/snippet",
		"POST " + privatePrefix + "/sites/{site}/icon/refetch",
		"PATCH " + privatePrefix + "/sites/{site}",
		"DELETE " + privatePrefix + "/sites/{site}",
		"POST " + privatePrefix + "/sites/{site}/restore",
		"POST " + privatePrefix + "/orgs/{org}/api-keys",
		"GET " + privatePrefix + "/orgs/{org}/audit",
		"DELETE " + privatePrefix + "/orgs/{org}",
		"GET " + privatePrefix + "/share/{slug}/stats/summary",
		"POST " + privatePrefix + "/share/{slug}/query",
		"GET " + privatePrefix + "/sites/{site}/goals",
		"POST " + privatePrefix + "/sites/{site}/goals",
		"PATCH " + privatePrefix + "/goals/{goal}",
		"DELETE " + privatePrefix + "/goals/{goal}",
		"GET " + privatePrefix + "/sites/{site}/annotations",
		"POST " + privatePrefix + "/sites/{site}/annotations",
		"PATCH " + privatePrefix + "/annotations/{annotation}",
		"DELETE " + privatePrefix + "/annotations/{annotation}",
		"GET " + privatePrefix + "/sites/{site}/links",
		"POST " + privatePrefix + "/sites/{site}/links",
		"GET " + privatePrefix + "/links/{link}",
		"PATCH " + privatePrefix + "/links/{link}",
		"DELETE " + privatePrefix + "/links/{link}",
		"GET " + privatePrefix + "/links/{link}/stats",
		"GET " + privatePrefix + "/sites/{site}/tracking-pixels",
		"POST " + privatePrefix + "/sites/{site}/tracking-pixels",
		"GET " + privatePrefix + "/tracking-pixels/{id}",
		"PATCH " + privatePrefix + "/tracking-pixels/{id}",
		"DELETE " + privatePrefix + "/tracking-pixels/{id}",
		"GET " + privatePrefix + "/sites/{site}/pixels",
		"POST " + privatePrefix + "/sites/{site}/pixels",
		"GET " + privatePrefix + "/sites/{site}/pixels/stats",
		"PATCH " + privatePrefix + "/sites/{site}/pixels/{id}",
		"DELETE " + privatePrefix + "/sites/{site}/pixels/{id}",
		"GET " + privatePrefix + "/sites/{site}/pixel",
		"GET " + privatePrefix + "/sites/{site}/pixel/stats",
		"GET " + privatePrefix + "/sites/{site}/funnels",
		"POST " + privatePrefix + "/sites/{site}/funnels",
		"PATCH " + privatePrefix + "/funnels/{funnel}",
		"DELETE " + privatePrefix + "/funnels/{funnel}",
		"GET " + privatePrefix + "/sites/{site}/segments",
		"POST " + privatePrefix + "/sites/{site}/segments",
		"PATCH " + privatePrefix + "/segments/{segment}",
		"DELETE " + privatePrefix + "/segments/{segment}",
		"GET " + privatePrefix + "/sites/{site}/cohorts",
		"POST " + privatePrefix + "/sites/{site}/cohorts",
		"PATCH " + privatePrefix + "/cohorts/{cohort}",
		"DELETE " + privatePrefix + "/cohorts/{cohort}",
		"POST " + privatePrefix + "/cohorts/{cohort}/duplicate",
		"GET " + privatePrefix + "/sites/{site}/dashboards",
		"POST " + privatePrefix + "/sites/{site}/dashboards",
		"GET " + privatePrefix + "/sites/{site}/dashboards/default",
		"DELETE " + privatePrefix + "/sites/{site}/dashboards/default",
		"GET " + privatePrefix + "/sites/{site}/dashboards/{dashboard}",
		"PATCH " + privatePrefix + "/sites/{site}/dashboards/{dashboard}",
		"DELETE " + privatePrefix + "/sites/{site}/dashboards/{dashboard}",
		"POST " + privatePrefix + "/sites/{site}/dashboards/{dashboard}/duplicate",
		"POST " + privatePrefix + "/sites/{site}/dashboards/{dashboard}/default",
		"POST " + privatePrefix + "/sites/{site}/imports",
		"GET " + privatePrefix + "/sites/{site}/imports",
		"POST " + privatePrefix + "/sites/{site}/imports/preview",
		"DELETE " + privatePrefix + "/imports/{import}",
		"GET " + privatePrefix + "/sites/{site}/export",
		"GET " + privatePrefix + "/sites/{site}/profiles",
		"GET " + privatePrefix + "/profiles/{profile}",
		"DELETE " + privatePrefix + "/profiles/{profile}",
		"GET " + privatePrefix + "/erasures/{erasure}",
		"POST " + privatePrefix + "/erasures/{erasure}/retry",
		"GET " + privatePrefix + "/sites/{site}/replays",
		"GET " + privatePrefix + "/sites/{site}/replays/{replay_path...}",
		"GET " + privatePrefix + "/sites/{site}/replay-settings",
		"PUT " + privatePrefix + "/sites/{site}/replay-settings",
		"GET " + privatePrefix + "/instance/replay-storage",
		"PUT " + privatePrefix + "/instance/replay-storage",
	} {
		mux.Handle(pattern, platform)
	}
}

func anonymousRateLimited(limiter *apiTokenBuckets, trustedProxies []netip.Prefix, next http.Handler) http.Handler {
	return http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		if allowAnonymousAPIRequest(w, r, limiter, trustedProxies) {
			next.ServeHTTP(w, r)
		}
	})
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

type apiRateLimitResult struct {
	allowed    bool
	limit      int
	remaining  int
	reset      time.Time
	retryAfter time.Duration
}

type apiTokenBucket struct {
	tokens float64
	last   time.Time
}

type apiTokenBuckets struct {
	mu      sync.Mutex
	rate    float64
	burst   int
	now     func() time.Time
	buckets map[string]apiTokenBucket
	attempt uint64
}

const (
	maxAPITokenBuckets         = 10_000
	apiTokenBucketPruneTarget  = 9_000
	apiTokenBucketIdleDuration = time.Minute
)

// newAPITokenBuckets creates a process-local limiter. Multi-process installs
// enforce an independent quota in each Supermetry process.
func newAPITokenBuckets(rate float64, burst int, now func() time.Time) *apiTokenBuckets {
	return &apiTokenBuckets{rate: rate, burst: burst, now: now, buckets: make(map[string]apiTokenBucket)}
}

func (l *apiTokenBuckets) take(keys ...string) apiRateLimitResult {
	now := l.now()
	l.mu.Lock()
	defer l.mu.Unlock()
	l.attempt++
	if l.attempt%256 == 0 {
		l.prune(now)
	}

	result := apiRateLimitResult{allowed: true, limit: l.burst, remaining: l.burst, reset: now}
	for _, key := range keys {
		bucket, ok := l.buckets[key]
		if !ok {
			if len(l.buckets) >= maxAPITokenBuckets {
				l.prune(now)
			}
			bucket = apiTokenBucket{tokens: float64(l.burst), last: now}
		} else if l.rate > 0 {
			bucket.tokens = min(float64(l.burst), bucket.tokens+now.Sub(bucket.last).Seconds()*l.rate)
		}
		bucket.last = now
		if bucket.tokens >= 1 {
			bucket.tokens--
		} else {
			result.allowed = false
		}
		l.buckets[key] = bucket

		remaining := max(0, int(math.Floor(bucket.tokens)))
		if remaining < result.remaining {
			result.remaining = remaining
		}
		if l.rate > 0 {
			untilFull := time.Duration((float64(l.burst) - bucket.tokens) / l.rate * float64(time.Second))
			if candidate := now.Add(untilFull); candidate.After(result.reset) {
				result.reset = candidate
			}
			if !result.allowed && bucket.tokens < 1 {
				untilToken := time.Duration((1 - bucket.tokens) / l.rate * float64(time.Second))
				if untilToken > result.retryAfter {
					result.retryAfter = untilToken
				}
			}
		}
	}
	return result
}

func (l *apiTokenBuckets) wouldAllow(key string) bool {
	now := l.now()
	l.mu.Lock()
	defer l.mu.Unlock()
	bucket, ok := l.buckets[key]
	if !ok {
		return l.burst >= 1
	}
	tokens := bucket.tokens
	if l.rate > 0 {
		tokens = min(float64(l.burst), tokens+now.Sub(bucket.last).Seconds()*l.rate)
	}
	return tokens >= 1
}

func (l *apiTokenBuckets) prune(now time.Time) {
	for key, bucket := range l.buckets {
		if now.Sub(bucket.last) >= apiTokenBucketIdleDuration {
			delete(l.buckets, key)
		}
	}
	if len(l.buckets) < maxAPITokenBuckets {
		return
	}
	type entry struct {
		key  string
		last time.Time
	}
	entries := make([]entry, 0, len(l.buckets))
	for key, bucket := range l.buckets {
		entries = append(entries, entry{key: key, last: bucket.last})
	}
	sort.Slice(entries, func(i, j int) bool {
		if entries[i].last.Equal(entries[j].last) {
			return entries[i].key < entries[j].key
		}
		return entries[i].last.Before(entries[j].last)
	})
	for i := 0; len(l.buckets) > apiTokenBucketPruneTarget; i++ {
		delete(l.buckets, entries[i].key)
	}
}

func authenticateRateLimited(auth Authenticator, registry sites.Registry, keyedLimiter ingest.RateLimiter, anonymousLimiter *apiTokenBuckets, trustedProxies []netip.Prefix, now func() time.Time, mcp bool, next http.Handler) http.Handler {
	return http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		if r.Header.Get("Authorization") == "" && !anonymousLimiter.wouldAllow(anonymousAPIKey(r, trustedProxies)) {
			if !allowAnonymousAPIRequest(w, r, anonymousLimiter, trustedProxies) {
				return
			}
		}
		principal, authenticated := Principal{}, false
		if auth != nil {
			principal, authenticated = auth.Authenticate(r)
		}
		if !authenticated {
			if !allowAnonymousAPIRequest(w, r, anonymousLimiter, trustedProxies) {
				return
			}
			if mcp {
				writeMCPAuthorizationError(w, nil, "")
			} else {
				writeProblem(w, unauthorized())
			}
			return
		}

		key := principal.CredentialID
		if key == "" {
			key = canonicalPrincipalRateLimitKey(principal)
		}
		allowed := keyedLimiter == nil || keyedLimiter.Allow(key)
		w.Header().Set("RateLimit-Limit", "120")
		w.Header().Set("RateLimit-Reset", now().Add(time.Minute).UTC().Format(time.RFC3339Nano))
		if !allowed {
			w.Header().Set("RateLimit-Remaining", "0")
			w.Header().Set("Retry-After", "1")
			writeProblem(w, rateLimited())
			return
		}
		w.Header().Set("RateLimit-Remaining", "1")
		ctx := context.WithValue(r.Context(), principalContextKey{}, principal)
		ctx = context.WithValue(ctx, registryContextKey{}, registry)
		next.ServeHTTP(w, r.WithContext(ctx))
	})
}

func canonicalPrincipalRateLimitKey(principal Principal) string {
	encoded, _ := json.Marshal(principal)
	digest := sha256.Sum256(encoded)
	return "principal:" + hex.EncodeToString(digest[:])
}

func allowAnonymousAPIRequest(w http.ResponseWriter, r *http.Request, limiter *apiTokenBuckets, trustedProxies []netip.Prefix) bool {
	result := limiter.take(anonymousAPIKey(r, trustedProxies))
	w.Header().Set("RateLimit-Limit", strconv.Itoa(result.limit))
	w.Header().Set("RateLimit-Remaining", strconv.Itoa(result.remaining))
	w.Header().Set("RateLimit-Reset", result.reset.UTC().Format(time.RFC3339Nano))
	if result.allowed {
		return true
	}
	retrySeconds := max(1, int(math.Ceil(result.retryAfter.Seconds())))
	w.Header().Set("Retry-After", strconv.Itoa(retrySeconds))
	writeProblem(w, rateLimited())
	return false
}

func anonymousAPIKey(r *http.Request, trustedProxies []netip.Prefix) string {
	client := "unknown"
	if address, ok := clientip.ResolveAccessClient(r, trustedProxies); ok {
		client = address.String()
	}
	return "ip:" + client
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
	// Hide foreign and unassigned sites before reporting any missing capability.
	if siteID != 0 && !siteAllowed(r, principal, siteID) {
		writeProblem(w, notFound("site not found"))
		return false
	}
	if !principal.hasScope(scope) {
		writeProblem(w, forbidden("the credential does not grant the required scope"))
		return false
	}
	// Negotiation and the streaming variant must not turn an ordinary read
	// credential (including a shared dashboard) into an export credential.
	if scope == "stats:read" && (wantsCSV(r) || r.URL.Query().Get("limit") == "all") && !principal.hasScope("data:export") {
		writeProblem(w, forbidden("the credential does not grant the data:export scope"))
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
	// The source contract also generates the dashboard client. Keep private
	// dashboard operations out of the public API document served to integrators.
	if root, ok := document.(map[string]any); ok {
		if paths, ok := root["paths"].(map[string]any); ok {
			for route := range paths {
				if strings.HasPrefix(route, privatePrefix+"/") {
					delete(paths, route)
				}
			}
		}
	}
	w.Header().Set("Content-Type", "application/json")
	_ = json.NewEncoder(w).Encode(document)
}

type registryContextKey struct{}

// validCSRF implements the double-submit check: the X-CSRF-Token header must
// equal the supermetry_csrf cookie, compared in constant time.
func validCSRF(r *http.Request) bool {
	header := r.Header.Get("X-CSRF-Token")
	cookie, err := r.Cookie("supermetry_csrf")
	if header == "" || err != nil || cookie.Value == "" {
		return false
	}
	return subtle.ConstantTimeCompare([]byte(header), []byte(cookie.Value)) == 1
}
