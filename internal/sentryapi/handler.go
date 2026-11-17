// Package sentryapi serves the subset of the Sentry web API that sentry-cli
// and the JavaScript bundler plugins call to upload source maps and manage
// releases. Only the base URL and a token change on the client side.
package sentryapi

import (
	"context"
	"encoding/json"
	"errors"
	"net/http"
	"net/netip"
	"slices"
	"strconv"
	"strings"
	"time"

	"github.com/jackc/pgx/v5"
	"github.com/jackc/pgx/v5/pgxpool"

	"supermetry/internal/platform/authz"
	"supermetry/internal/replay"
	"supermetry/internal/sites"
	"supermetry/internal/sourcemap"
)

const (
	// Scope is the API key scope that grants every endpoint of this package.
	Scope = "releases:write"

	chunkSize        = 8 << 20
	chunksPerRequest = 64
	maxFileSize      = 2 << 30
	maxRequestSize   = 32 << 20
	concurrency      = 4
	maxChunksPerFile = 256

	defaultOrgCap   = 5 << 30
	assembleWorkers = 2
	assembleQueue   = 32
	maxJSONBody     = 1 << 20
)

// KeyVerifier resolves a plain API key into its principal.
type KeyVerifier interface {
	Verify(ctx context.Context, plain string) (authz.Principal, bool)
}

// Deps wires the handler to the platform.
type Deps struct {
	Pool  *pgxpool.Pool
	Keys  KeyVerifier
	Sites sites.Registry
	Blobs replay.BlobStore
	Now   func() time.Time
	// BaseURL is the public origin, used for the absolute chunk-upload URL the
	// TS CLI requires; empty derives it from each request.
	BaseURL string
	// TempDir holds bundles while they are assembled; empty uses the OS default.
	TempDir string
	// OrgCapBytes limits the stored bundle bytes per organization; zero means 5 GiB.
	OrgCapBytes int64
	// IPPerMin and TokenPerMin cap requests per client address and per API
	// token; zero selects the defaults, a negative value disables the limit.
	IPPerMin, TokenPerMin int
	// TrustedProxies are the proxies whose forwarding headers are believed
	// when finding the client address.
	TrustedProxies []netip.Prefix
	// Caps bounds a bundle; the zero value means sourcemap.DefaultCaps.
	Caps sourcemap.Caps
	// Builders convert debug objects to caches, keyed by object kind. Kinds
	// without a builder keep their objects pending (the raw file is stored).
	Builders map[string]CacheBuilder
	// DIFMaxBytes caps one uploaded debug file; zero means 512 MiB.
	DIFMaxBytes int64
}

// Handler serves /api/0/.
type Handler struct {
	deps  Deps
	lim   *limits
	mux   *http.ServeMux
	queue chan int64
	// difQueue feeds the single debug file worker, so only one file is held
	// and converted at a time.
	difQueue chan int64
	done     chan struct{}
	stop     context.CancelFunc
	ctx      context.Context
}

// New builds the handler. Call Start to run assembly workers and Close to stop them.
func New(deps Deps) *Handler {
	if deps.Now == nil {
		deps.Now = time.Now
	}
	if deps.OrgCapBytes <= 0 {
		deps.OrgCapBytes = defaultOrgCap
	}
	if deps.Caps == (sourcemap.Caps{}) {
		deps.Caps = sourcemap.DefaultCaps()
	}
	h := &Handler{deps: deps, lim: newLimits(deps.IPPerMin, deps.TokenPerMin, deps.TrustedProxies), mux: http.NewServeMux(), queue: make(chan int64, assembleQueue), difQueue: make(chan int64, difQueueSize), done: make(chan struct{})}
	h.routes()
	return h
}

func (h *Handler) routes() {
	m := h.mux
	m.HandleFunc("GET /api/0/{$}", h.root)
	m.HandleFunc("GET /api/0/organizations/{org}/{$}", h.org(h.orgInfo))
	m.HandleFunc("GET /api/0/organizations/{org}/chunk-upload/{$}", h.org(h.chunkOptions))
	m.HandleFunc("POST /api/0/organizations/{org}/chunk-upload/{$}", h.org(h.chunkUpload))
	m.HandleFunc("POST /api/0/organizations/{org}/artifactbundle/assemble/{$}", h.org(h.assemble))
	m.HandleFunc("POST /api/0/projects/{org}/{project}/files/difs/assemble/{$}", h.org(h.difsAssemble))
	m.HandleFunc("POST /api/0/organizations/{org}/releases/{$}", h.org(h.createRelease))
	m.HandleFunc("GET /api/0/organizations/{org}/releases/{$}", h.org(h.listOrgReleases))
	m.HandleFunc("GET /api/0/projects/{org}/{project}/releases/{$}", h.org(h.listProjectReleases))
	m.HandleFunc("DELETE /api/0/organizations/{org}/releases/{version}/{$}", h.org(h.deleteRelease))
	m.HandleFunc("GET /api/0/organizations/{org}/releases/{version}/{$}", h.org(h.getRelease))
	m.HandleFunc("PUT /api/0/organizations/{org}/releases/{version}/{$}", h.org(h.updateRelease))
	m.HandleFunc("POST /api/0/organizations/{org}/releases/{version}/deploys/{$}", h.org(h.createDeploy))
	m.HandleFunc("GET /api/0/organizations/{org}/releases/{version}/deploys/{$}", h.org(h.listDeploys))
	m.HandleFunc("GET /api/0/organizations/{org}/releases/{version}/previous-with-commits/{$}", h.org(h.previousWithCommits))
	m.HandleFunc("GET /api/0/organizations/{org}/releases/{version}/commits/{$}", h.org(h.listCommits))
	m.HandleFunc("GET /api/0/organizations/{org}/repos/{$}", h.org(func(w http.ResponseWriter, _ *http.Request, _ *call) { writeJSON(w, http.StatusOK, []any{}) }))
	m.HandleFunc("/api/0/", func(w http.ResponseWriter, _ *http.Request) { notFound(w) })
}

// ServeHTTP implements http.Handler.
func (h *Handler) ServeHTTP(w http.ResponseWriter, r *http.Request) {
	w.Header().Set("Cache-Control", "no-store")
	if retry, limited := h.lim.check(r); limited {
		tooManyRequests(w, retry)
		return
	}
	h.mux.ServeHTTP(w, r)
}

// call is the authenticated context of one request.
type call struct {
	principal authz.Principal
	orgID     int64
	orgSlug   string
	orgName   string
}

func (h *Handler) root(w http.ResponseWriter, r *http.Request) {
	if _, ok := h.authenticate(r); !ok {
		unauthorized(w)
		return
	}
	writeJSON(w, http.StatusOK, map[string]any{})
}

// org authenticates the key, hides organizations the key does not belong to
// behind a 404, then requires the releases:write scope.
func (h *Handler) org(next func(http.ResponseWriter, *http.Request, *call)) http.HandlerFunc {
	return func(w http.ResponseWriter, r *http.Request) {
		principal, ok := h.authenticate(r)
		if !ok {
			unauthorized(w)
			return
		}
		slug := r.PathValue("org")
		var c call
		err := h.deps.Pool.QueryRow(r.Context(), `select id, slug, name from orgs where slug=$1 and deleted_at is null`, slug).Scan(&c.orgID, &c.orgSlug, &c.orgName)
		if errors.Is(err, pgx.ErrNoRows) || err == nil && c.orgID != principal.OrgID {
			notFound(w)
			return
		}
		if err != nil {
			serverError(w)
			return
		}
		if !slices.Contains(principal.Scopes, Scope) {
			writeDetail(w, http.StatusForbidden, "the token does not grant the "+Scope+" scope")
			return
		}
		c.principal = principal
		next(w, r, &c)
	}
}

// ParseToken extracts the API key from a bearer credential: a plain key, or a
// Sentry organization token, sntrys_<base64 payload>_<secret>, whose payload
// the server ignores. Sentry clients split the token on underscores and need a
// 43 character secret, so the secret is an upload key (see keys.Create)
// without its underscores: the 8 character prefix and the secret part.
func ParseToken(credential string) string {
	rest, ok := strings.CutPrefix(credential, "sntrys_")
	if !ok {
		return credential
	}
	i := strings.LastIndex(rest, "_")
	if i < 0 {
		return ""
	}
	secret := rest[i+1:]
	if len(secret) != 43 {
		return ""
	}
	return "pnl_" + secret[:keyPrefixLen] + "_" + secret[keyPrefixLen:]
}

const keyPrefixLen = 8

func (h *Handler) authenticate(r *http.Request) (authz.Principal, bool) {
	scheme, credential, ok := strings.Cut(r.Header.Get("Authorization"), " ")
	if !ok || !strings.EqualFold(scheme, "Bearer") || credential == "" || h.deps.Keys == nil {
		return authz.Principal{}, false
	}
	key := ParseToken(strings.TrimSpace(credential))
	if key == "" {
		return authz.Principal{}, false
	}
	return h.deps.Keys.Verify(r.Context(), key)
}

type projectError struct {
	status int
	detail string
}

// resolveProjects maps project slugs (public id, numeric id or readable slug)
// to site ids. Unknown slugs are 404, sites outside the key's restriction 403.
// With no slugs the key's own site restriction (nil for none) applies.
func (h *Handler) resolveProjects(c *call, slugs []string) ([]int32, *projectError) {
	if len(slugs) == 0 {
		return slices.Clone(c.principal.SiteIDs), nil
	}
	var ids []int32
	for _, slug := range slugs {
		site, ok := h.findSite(c.orgID, slug)
		if !ok {
			return nil, &projectError{http.StatusNotFound, "project not found"}
		}
		if !authz.Can(c.principal, authz.ActionReleasesWrite, site.ID) {
			return nil, &projectError{http.StatusForbidden, "the token may not use this project"}
		}
		if !slices.Contains(ids, site.ID) {
			ids = append(ids, site.ID)
		}
	}
	return ids, nil
}

// canUseSites reports whether the principal may act on a resource bound to
// siteIDs: an org-wide resource (no sites) needs an unrestricted key, a
// site-bound one needs releases:write on every site it is bound to.
func (c *call) canUseSites(siteIDs []int32) bool {
	if len(siteIDs) == 0 {
		return c.principal.SiteIDs == nil
	}
	for _, id := range siteIDs {
		if !authz.Can(c.principal, authz.ActionReleasesWrite, id) {
			return false
		}
	}
	return true
}

func (h *Handler) findSite(orgID int64, slug string) (sites.Site, bool) {
	if h.deps.Sites == nil {
		return sites.Site{}, false
	}
	if n, err := strconv.ParseInt(slug, 10, 32); err == nil {
		if s, ok := h.deps.Sites.Site(int32(n)); ok && s.OrgID == orgID {
			return s, true
		}
	}
	if s, ok := h.deps.Sites.ByPublicID(slug); ok && s.OrgID == orgID {
		return s, true
	}
	for _, s := range h.deps.Sites.All() {
		if s.OrgID == orgID && s.Slug != "" && s.Slug == slug {
			return s, true
		}
	}
	return sites.Site{}, false
}

func (h *Handler) orgInfo(w http.ResponseWriter, _ *http.Request, c *call) {
	writeJSON(w, http.StatusOK, map[string]any{"slug": c.orgSlug, "name": c.orgName})
}

// origin is the public base URL: the configured one, else the request's.
func (h *Handler) origin(r *http.Request) string {
	if h.deps.BaseURL != "" {
		return strings.TrimRight(h.deps.BaseURL, "/")
	}
	scheme := "http"
	if r.TLS != nil || strings.EqualFold(r.Header.Get("X-Forwarded-Proto"), "https") {
		scheme = "https"
	}
	return scheme + "://" + r.Host
}

func (h *Handler) chunkOptions(w http.ResponseWriter, r *http.Request, c *call) {
	writeJSON(w, http.StatusOK, map[string]any{
		"url":              h.origin(r) + "/api/0/organizations/" + c.orgSlug + "/chunk-upload/",
		"chunkSize":        chunkSize,
		"chunksPerRequest": chunksPerRequest,
		"maxFileSize":      int64(maxFileSize),
		"maxRequestSize":   maxRequestSize,
		"concurrency":      concurrency,
		"hashAlgorithm":    "sha1",
		"compression":      []string{"gzip"},
		"accept":           []string{"artifact_bundles", "release_files", "sources", "debug_files", "proguard"},
	})
}

func readJSON(w http.ResponseWriter, r *http.Request, v any) bool {
	return readJSONMax(w, r, v, maxJSONBody)
}

func readJSONMax(w http.ResponseWriter, r *http.Request, v any, limit int64) bool {
	r.Body = http.MaxBytesReader(w, r.Body, limit)
	dec := json.NewDecoder(r.Body)
	if err := dec.Decode(v); err != nil {
		writeDetail(w, http.StatusBadRequest, "invalid JSON body")
		return false
	}
	return true
}

func writeJSON(w http.ResponseWriter, status int, v any) {
	w.Header().Set("Content-Type", "application/json")
	w.WriteHeader(status)
	_ = json.NewEncoder(w).Encode(v)
}

func writeDetail(w http.ResponseWriter, status int, detail string) {
	writeJSON(w, status, map[string]string{"detail": detail})
}

func notFound(w http.ResponseWriter) { writeDetail(w, http.StatusNotFound, "not found") }
func serverError(w http.ResponseWriter) {
	writeDetail(w, http.StatusInternalServerError, "internal error")
}
func unauthorized(w http.ResponseWriter) {
	w.Header().Set("WWW-Authenticate", `Bearer realm="supermetry"`)
	writeDetail(w, http.StatusUnauthorized, "Invalid token")
}
