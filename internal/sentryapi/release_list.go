package sentryapi

import (
	"errors"
	"net/http"
	"net/url"
	"strconv"
	"strings"

	"github.com/jackc/pgx/v5"
)

const (
	defaultReleasePage = 100
	maxReleasePage     = 100
)

// listOrgReleases serves GET /organizations/{org}/releases/.
func (h *Handler) listOrgReleases(w http.ResponseWriter, r *http.Request, c *call) {
	h.listReleases(w, r, c, 0)
}

// listProjectReleases serves GET /projects/{org}/{project}/releases/: the
// project's releases plus the org-wide ones.
func (h *Handler) listProjectReleases(w http.ResponseWriter, r *http.Request, c *call) {
	site, ok := h.findSite(c.orgID, r.PathValue("project"))
	if !ok || !c.canUseSites([]int32{site.ID}) {
		notFound(w)
		return
	}
	h.listReleases(w, r, c, site.ID)
}

// listReleases pages releases newest-created first. The cursor is the id of
// the last row of the previous page. siteID filters to one project (0: none).
func (h *Handler) listReleases(w http.ResponseWriter, r *http.Request, c *call, siteID int32) {
	q := r.URL.Query()
	perPage := defaultReleasePage
	if v := q.Get("per_page"); v != "" {
		n, err := strconv.Atoi(v)
		if err != nil {
			writeDetail(w, http.StatusBadRequest, "invalid per_page")
			return
		}
		perPage = min(max(n, 1), maxReleasePage)
	}
	var cursor int64 // 0: first page
	if v := q.Get("cursor"); v != "" {
		n, err := strconv.ParseInt(v, 10, 64)
		if err != nil || n < 1 {
			writeDetail(w, http.StatusBadRequest, "invalid cursor")
			return
		}
		cursor = n
	}
	var restrict any // nil: no key restriction
	if c.principal.SiteIDs != nil {
		restrict = c.principal.SiteIDs
	}
	var project any
	if siteID != 0 {
		project = siteID
	}
	rows, err := h.deps.Pool.Query(r.Context(), `
		select `+releaseColumns+` from app_releases
		where org_id = $1
		  and ($2::bigint = 0 or id < $2::bigint)
		  and ($3::text = '' or position(lower($3) in lower(version)) > 0)
		  and ($4::int is null or site_ids = '{}' or $4 = any(site_ids))
		  and ($5::int[] is null or (site_ids <> '{}' and site_ids <@ $5))
		order by id desc limit $6`,
		c.orgID, cursor, q.Get("query"), project, restrict, perPage+1)
	if err != nil {
		serverError(w)
		return
	}
	defer rows.Close()
	var rels []releaseRow
	for rows.Next() {
		var rel releaseRow
		if err := rel.scan(rows); err != nil {
			serverError(w)
			return
		}
		rels = append(rels, rel)
	}
	if rows.Err() != nil {
		serverError(w)
		return
	}
	more := len(rels) > perPage
	if more {
		rels = rels[:perPage]
	}
	out := make([]map[string]any, 0, len(rels))
	for _, rel := range rels {
		out = append(out, h.releaseJSON(r.Context(), c, rel))
	}
	next := ""
	if len(rels) > 0 {
		next = strconv.FormatInt(rels[len(rels)-1].id, 10)
	}
	w.Header().Set("Link", releaseLink(r, "previous", false, "")+", "+releaseLink(r, "next", more, next))
	writeJSON(w, http.StatusOK, out)
}

// releaseLink formats one Sentry-style pagination Link entry.
func releaseLink(r *http.Request, rel string, results bool, cursor string) string {
	u := *r.URL
	q := u.Query()
	if cursor != "" {
		q.Set("cursor", cursor)
	} else {
		q.Del("cursor")
	}
	u.RawQuery = q.Encode()
	var b strings.Builder
	b.WriteString("<" + (&url.URL{Path: u.Path, RawQuery: u.RawQuery}).String() + ">; rel=\"" + rel + "\"; results=\"" + strconv.FormatBool(results) + "\"")
	b.WriteString("; cursor=\"" + cursor + "\"")
	return b.String()
}

// deleteRelease removes a release and, by cascade, its deploys. Events keep
// their release text (decision 2b-D3).
func (h *Handler) deleteRelease(w http.ResponseWriter, r *http.Request, c *call) {
	rel, err := h.loadRelease(r.Context(), c.orgID, r.PathValue("version"))
	if errors.Is(err, pgx.ErrNoRows) {
		notFound(w)
		return
	}
	if err != nil {
		serverError(w)
		return
	}
	if !h.canUseRelease(c, rel) {
		notFound(w)
		return
	}
	if _, err := h.deps.Pool.Exec(r.Context(), `delete from app_releases where id=$1 and org_id=$2`, rel.id, c.orgID); err != nil {
		serverError(w)
		return
	}
	w.WriteHeader(http.StatusNoContent)
}
