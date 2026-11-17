package platformhttp

import (
	"errors"
	"mime"
	"net/http"
	"strconv"
	"strings"
	"time"

	"github.com/jackc/pgx/v5"

	"supermetry/internal/appattach"
	"supermetry/internal/appquery"
	"supermetry/internal/platform/audit"
	"supermetry/internal/platform/authz"
	coresites "supermetry/internal/sites"
)

const (
	maxReleaseParam     = 64
	maxEnvironmentParam = 32
	maxPlatformParam    = 32
	maxIssueQuery       = 100
)

// appHealthSite resolves an app project the caller may use. Reads need
// stats.read (every member role); a change needs site.edit (owner, admin and
// editor, the "member" of the product language).
func (h *handler) appHealthSite(w http.ResponseWriter, r *http.Request, action authz.Action, roles ...authz.Role) (authz.Principal, coresites.Site, bool) {
	principal, site, ok := h.sitePrincipal(w, r, action, roles...)
	if !ok {
		return principal, site, false
	}
	if site.Kind != coresites.KindApp || h.deps.Store == nil {
		writeProblem(w, http.StatusNotFound, "Not found", "not an app project")
		return principal, site, false
	}
	return principal, site, true
}

func (h *handler) appReadSite(w http.ResponseWriter, r *http.Request) (coresites.Site, bool) {
	_, site, ok := h.appHealthSite(w, r, authz.ActionStatsRead, linkRoles()...)
	return site, ok
}

func (h *handler) appQuerier() *appquery.Querier {
	return appquery.New(h.deps.Store.Pool, h.deps.Now)
}

func appScope(site coresites.Site) appquery.Scope {
	return appquery.Scope{SiteID: site.ID, TZ: siteLocation(site), RetentionDays: int(site.Retention)}
}

// appFilter reads from, to, release, environment and platform.
func appFilter(w http.ResponseWriter, r *http.Request, site coresites.Site, now time.Time) (appquery.Filter, bool) {
	from, to, ok := statsRange(w, r, site, now)
	if !ok {
		return appquery.Filter{}, false
	}
	var f appquery.Filter
	var err error
	if f.From, err = time.Parse(time.DateOnly, from); err == nil {
		f.To, err = time.Parse(time.DateOnly, to)
	}
	if err != nil {
		writeProblem(w, http.StatusBadRequest, "Bad request", "from and to must be dates as YYYY-MM-DD")
		return f, false
	}
	query := r.URL.Query()
	for _, p := range []struct {
		name string
		max  int
		dst  *string
	}{
		{"release", maxReleaseParam, &f.Release}, {"environment", maxEnvironmentParam, &f.Environment}, {"platform", maxPlatformParam, &f.Platform},
	} {
		v := query.Get(p.name)
		if len(v) > p.max {
			writeProblem(w, http.StatusBadRequest, "Bad request", p.name+" is too long")
			return f, false
		}
		*p.dst = v
	}
	return f, true
}

// appQueryError maps appquery errors onto problem responses.
func appQueryError(w http.ResponseWriter, err error) {
	switch {
	case errors.Is(err, appquery.ErrNotFound):
		writeProblem(w, http.StatusNotFound, "Not found", "issue not found")
	case errors.Is(err, appquery.ErrRange), errors.Is(err, appquery.ErrInvalid):
		writeProblem(w, http.StatusBadRequest, "Bad request", strings.TrimPrefix(err.Error(), appquery.ErrInvalid.Error()+": "))
	default:
		serviceError(w, err)
	}
}

func (h *handler) getAppOverview(w http.ResponseWriter, r *http.Request) {
	site, ok := h.appReadSite(w, r)
	if !ok {
		return
	}
	f, ok := appFilter(w, r, site, h.deps.Now())
	if !ok {
		return
	}
	out, err := h.appQuerier().Overview(r.Context(), appScope(site), f)
	if err != nil {
		appQueryError(w, err)
		return
	}
	writeJSON(w, http.StatusOK, out)
}

func (h *handler) listAppIssues(w http.ResponseWriter, r *http.Request) {
	site, ok := h.appReadSite(w, r)
	if !ok {
		return
	}
	f, ok := appFilter(w, r, site, h.deps.Now())
	if !ok {
		return
	}
	query := r.URL.Query()
	in := appquery.IssueQuery{Filter: f, Status: query.Get("status"), Kind: query.Get("kind"), Q: query.Get("q"), Sort: query.Get("sort")}
	if len(in.Kind) > 32 || len(in.Q) > maxIssueQuery {
		writeProblem(w, http.StatusBadRequest, "Bad request", "kind or q is too long")
		return
	}
	var err error
	if raw := query.Get("limit"); raw != "" {
		if in.Limit, err = strconv.Atoi(raw); err != nil || in.Limit < 1 {
			writeProblem(w, http.StatusBadRequest, "Bad request", "limit must be a positive integer")
			return
		}
	}
	if raw := query.Get("offset"); raw != "" {
		if in.Offset, err = strconv.Atoi(raw); err != nil || in.Offset < 0 {
			writeProblem(w, http.StatusBadRequest, "Bad request", "offset must be a non-negative integer")
			return
		}
	}
	out, err := h.appQuerier().Issues(r.Context(), appScope(site), in)
	if err != nil {
		appQueryError(w, err)
		return
	}
	writeJSON(w, http.StatusOK, out)
}

func (h *handler) getAppIssue(w http.ResponseWriter, r *http.Request) {
	site, ok := h.appReadSite(w, r)
	if !ok {
		return
	}
	id, err := parseInt64(r.PathValue("issue"))
	if err != nil {
		writeProblem(w, http.StatusNotFound, "Not found", "issue not found")
		return
	}
	f, ok := appFilter(w, r, site, h.deps.Now())
	if !ok {
		return
	}
	out, err := h.appQuerier().Issue(r.Context(), appScope(site), id, f)
	if err != nil {
		appQueryError(w, err)
		return
	}
	writeJSON(w, http.StatusOK, out)
}

func (h *handler) getAppEvent(w http.ResponseWriter, r *http.Request) {
	site, ok := h.appReadSite(w, r)
	if !ok {
		return
	}
	id := r.PathValue("event_id")
	if len(id) == 0 || len(id) > 64 {
		writeProblem(w, http.StatusNotFound, "Not found", "event not found")
		return
	}
	out, err := h.appQuerier().Event(r.Context(), appScope(site), id)
	if err != nil {
		appQueryError(w, err)
		return
	}
	writeJSON(w, http.StatusOK, out)
}

// getAppEventAttachment serves one stored attachment of an event. The bytes
// are attacker-controlled, so: only PNG, JPEG and WebP are shown inline, every
// other type goes out as an opaque download, and nosniff plus a sandboxing CSP
// keep a browser from ever treating the response as a page.
func (h *handler) getAppEventAttachment(w http.ResponseWriter, r *http.Request) {
	site, ok := h.appReadSite(w, r)
	if !ok {
		return
	}
	id := r.PathValue("event_id")
	idx, err := strconv.Atoi(r.PathValue("idx"))
	if len(id) == 0 || len(id) > 64 || err != nil {
		writeProblem(w, http.StatusNotFound, "Not found", "attachment not found")
		return
	}
	a, err := appattach.Get(r.Context(), h.deps.Store.Pool, site.ID, id, idx)
	if err != nil {
		if errors.Is(err, appattach.ErrNotFound) {
			writeProblem(w, http.StatusNotFound, "Not found", "attachment not found")
			return
		}
		serviceError(w, err)
		return
	}
	contentType, disposition := "application/octet-stream", "attachment"
	switch {
	case appattach.Inline(a.ContentType):
		contentType, disposition = a.ContentType, "inline"
	case a.ContentType == appattach.TypeText:
		contentType = "text/plain; charset=utf-8"
	case a.ContentType == appattach.TypeJSON:
		contentType = "application/json"
	}
	name := strings.Map(func(r rune) rune {
		if r < 0x20 || r == 0x7f || r == '"' || r == '\\' || r == '/' {
			return -1
		}
		return r
	}, a.Name)
	if name == "" {
		name = "attachment"
	}
	cd := mime.FormatMediaType(disposition, map[string]string{"filename": name})
	if cd == "" {
		cd = disposition
	}
	hdr := w.Header()
	hdr.Set("Content-Type", contentType)
	hdr.Set("Content-Disposition", cd)
	hdr.Set("Content-Length", strconv.Itoa(len(a.Data)))
	hdr.Set("X-Content-Type-Options", "nosniff")
	hdr.Set("Content-Security-Policy", "default-src 'none'; sandbox")
	hdr.Set("Cross-Origin-Resource-Policy", "same-origin")
	hdr.Set("Cache-Control", "private, no-store")
	w.WriteHeader(http.StatusOK)
	_, _ = w.Write(a.Data)
}

func (h *handler) getAppCompatibility(w http.ResponseWriter, r *http.Request) {
	site, ok := h.appReadSite(w, r)
	if !ok {
		return
	}
	f, ok := appFilter(w, r, site, h.deps.Now())
	if !ok {
		return
	}
	rows, err := h.appQuerier().Compatibility(r.Context(), appScope(site), f)
	if err != nil {
		appQueryError(w, err)
		return
	}
	writeJSON(w, http.StatusOK, map[string]any{"rows": rows})
}

func (h *handler) getAppReleases(w http.ResponseWriter, r *http.Request) {
	site, ok := h.appReadSite(w, r)
	if !ok {
		return
	}
	f, ok := appFilter(w, r, site, h.deps.Now())
	if !ok {
		return
	}
	rows, err := h.appQuerier().Releases(r.Context(), appScope(site), f)
	if err != nil {
		appQueryError(w, err)
		return
	}
	writeJSON(w, http.StatusOK, map[string]any{"releases": rows})
}

type appIssueStatusBody struct {
	Status            string  `json:"status"`
	ResolvedInRelease *string `json:"resolved_in_release"`
}

func (h *handler) updateAppIssueStatus(w http.ResponseWriter, r *http.Request) {
	principal, site, ok := h.appHealthSite(w, r, authz.ActionSiteEdit, authz.RoleOwner, authz.RoleAdmin, authz.RoleEditor)
	if !ok {
		return
	}
	id, err := parseInt64(r.PathValue("issue"))
	if err != nil {
		writeProblem(w, http.StatusNotFound, "Not found", "issue not found")
		return
	}
	var in appIssueStatusBody
	if !decodeJSON(w, r, &in) {
		return
	}
	var result appquery.StatusResult
	err = h.deps.Store.Tx(r.Context(), func(tx pgx.Tx) error {
		var err error
		result, err = h.appQuerier().SetStatus(r.Context(), tx, appScope(site), id, appquery.StatusInput{Status: in.Status, ResolvedInRelease: in.ResolvedInRelease})
		if err != nil {
			return err
		}
		meta := map[string]any{"site_id": site.ID, "from": result.Previous, "to": result.Status, "resolved_next": result.ResolvedNext}
		if result.ResolvedInRelease != nil {
			meta["resolved_in_release"] = *result.ResolvedInRelease
		}
		return audit.Write(r.Context(), tx, principal, "app.issue_status_changed", "app_issue", id, meta)
	})
	if err != nil {
		appQueryError(w, err)
		return
	}
	writeJSON(w, http.StatusOK, result)
}
