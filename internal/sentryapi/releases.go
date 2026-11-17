package sentryapi

import (
	"context"
	"errors"
	"net/http"
	"slices"
	"strconv"
	"time"

	"github.com/jackc/pgx/v5"
)

type releaseBody struct {
	Version      string     `json:"version"`
	Projects     []string   `json:"projects"`
	URL          *string    `json:"url"`
	DateReleased *time.Time `json:"dateReleased"`
}

type releaseRow struct {
	id           int64
	version      string
	siteIDs      []int32
	url          string
	dateCreated  time.Time
	dateReleased *time.Time
}

func (h *Handler) releaseJSON(ctx context.Context, c *call, rel releaseRow) map[string]any {
	var deploys int
	_ = h.deps.Pool.QueryRow(ctx, `select count(*) from app_deploys where release_id=$1`, rel.id).Scan(&deploys)
	projects := []map[string]any{}
	for _, id := range rel.siteIDs {
		if s, ok := h.deps.Sites.Site(id); ok && s.OrgID == c.orgID {
			projects = append(projects, map[string]any{"id": id, "slug": s.PublicID, "name": s.PublicID})
		}
	}
	var released any
	if rel.dateReleased != nil {
		released = rel.dateReleased.UTC().Format(time.RFC3339Nano)
	}
	return map[string]any{
		"version": rel.version, "shortVersion": rel.version, "ref": nil, "url": nilIfEmpty(rel.url),
		"dateCreated": rel.dateCreated.UTC().Format(time.RFC3339Nano), "dateReleased": released,
		"status": "open", "commitCount": 0, "deployCount": deploys, "newGroups": 0,
		"authors": []any{}, "projects": projects,
	}
}

func nilIfEmpty(s string) any {
	if s == "" {
		return nil
	}
	return s
}

// canUseRelease reports whether the principal may see and change a release.
// Callers answer 404 when it is false so a foreign release is not revealed.
func (h *Handler) canUseRelease(c *call, rel releaseRow) bool { return c.canUseSites(rel.siteIDs) }

func (h *Handler) loadRelease(ctx context.Context, orgID int64, version string) (releaseRow, error) {
	var rel releaseRow
	err := h.deps.Pool.QueryRow(ctx, `select id, version, site_ids, url, date_created, date_released from app_releases where org_id=$1 and version=$2`, orgID, version).
		Scan(&rel.id, &rel.version, &rel.siteIDs, &rel.url, &rel.dateCreated, &rel.dateReleased)
	return rel, err
}

func (h *Handler) createRelease(w http.ResponseWriter, r *http.Request, c *call) {
	var body releaseBody
	if !readJSON(w, r, &body) {
		return
	}
	if body.Version == "" || len(body.Version) > 200 {
		writeDetail(w, http.StatusBadRequest, "version is required")
		return
	}
	siteIDs, perr := h.resolveProjects(c, body.Projects)
	if perr != nil {
		writeDetail(w, perr.status, perr.detail)
		return
	}
	if siteIDs == nil {
		siteIDs = []int32{}
	}
	url := ""
	if body.URL != nil {
		url = *body.URL
	}
	status := http.StatusCreated
	var inserted bool
	err := h.deps.Pool.QueryRow(r.Context(), `
		insert into app_releases(org_id, version, site_ids, url, date_released) values ($1,$2,$3,$4,$5)
		on conflict (org_id, version) do update set version = app_releases.version
		returning (xmax = 0)`, c.orgID, body.Version, siteIDs, url, body.DateReleased).Scan(&inserted)
	if err != nil {
		serverError(w)
		return
	}
	if !inserted {
		status = http.StatusAlreadyReported // 208: the release already existed
	}
	rel, err := h.loadRelease(r.Context(), c.orgID, body.Version)
	if err != nil {
		serverError(w)
		return
	}
	if !inserted && !h.canUseRelease(c, rel) {
		notFound(w)
		return
	}
	writeJSON(w, status, h.releaseJSON(r.Context(), c, rel))
}

func (h *Handler) getRelease(w http.ResponseWriter, r *http.Request, c *call) {
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
	writeJSON(w, http.StatusOK, h.releaseJSON(r.Context(), c, rel))
}

func (h *Handler) updateRelease(w http.ResponseWriter, r *http.Request, c *call) {
	var body releaseBody
	if !readJSON(w, r, &body) {
		return
	}
	version := r.PathValue("version")
	rel, err := h.loadRelease(r.Context(), c.orgID, version)
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
	if len(body.Projects) > 0 {
		siteIDs, perr := h.resolveProjects(c, body.Projects)
		if perr != nil {
			writeDetail(w, perr.status, perr.detail)
			return
		}
		for _, id := range siteIDs {
			if !slices.Contains(rel.siteIDs, id) {
				rel.siteIDs = append(rel.siteIDs, id)
			}
		}
	}
	if body.URL != nil {
		rel.url = *body.URL
	}
	if body.DateReleased != nil {
		rel.dateReleased = body.DateReleased
	}
	if _, err := h.deps.Pool.Exec(r.Context(), `update app_releases set site_ids=$2, url=$3, date_released=$4 where id=$1`, rel.id, rel.siteIDs, rel.url, rel.dateReleased); err != nil {
		serverError(w)
		return
	}
	writeJSON(w, http.StatusOK, h.releaseJSON(r.Context(), c, rel))
}

type deployBody struct {
	Environment  string     `json:"environment"`
	Name         string     `json:"name"`
	URL          string     `json:"url"`
	DateStarted  *time.Time `json:"dateStarted"`
	DateFinished *time.Time `json:"dateFinished"`
	Projects     []string   `json:"projects"`
}

func deployJSON(id int64, environment, name, url string, started, finished, created *time.Time) map[string]any {
	format := func(t *time.Time) any {
		if t == nil {
			return nil
		}
		return t.UTC().Format(time.RFC3339Nano)
	}
	return map[string]any{
		"id": strconv.FormatInt(id, 10), "environment": environment, "name": nilIfEmpty(name), "url": nilIfEmpty(url),
		"dateStarted": format(started), "dateFinished": format(finished), "dateCreated": format(created),
	}
}

func (h *Handler) createDeploy(w http.ResponseWriter, r *http.Request, c *call) {
	var body deployBody
	if !readJSON(w, r, &body) {
		return
	}
	if body.Environment == "" || len(body.Environment) > 64 || len(body.Name) > 200 || len(body.URL) > 2000 {
		writeDetail(w, http.StatusBadRequest, "environment is required")
		return
	}
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
	if _, perr := h.resolveProjects(c, body.Projects); perr != nil {
		writeDetail(w, perr.status, perr.detail)
		return
	}
	var id int64
	var created time.Time
	err = h.deps.Pool.QueryRow(r.Context(), `insert into app_deploys(release_id, environment, name, url, date_started, date_finished) values ($1,$2,$3,$4,$5,$6) returning id, date_created`,
		rel.id, body.Environment, body.Name, body.URL, body.DateStarted, body.DateFinished).Scan(&id, &created)
	if err != nil {
		serverError(w)
		return
	}
	writeJSON(w, http.StatusCreated, deployJSON(id, body.Environment, body.Name, body.URL, body.DateStarted, body.DateFinished, &created))
}

func (h *Handler) listDeploys(w http.ResponseWriter, r *http.Request, c *call) {
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
	rows, err := h.deps.Pool.Query(r.Context(), `select id, environment, name, url, date_started, date_finished, date_created from app_deploys where release_id=$1 order by id desc limit 100`, rel.id)
	if err != nil {
		serverError(w)
		return
	}
	defer rows.Close()
	out := []map[string]any{}
	for rows.Next() {
		var id int64
		var env, name, url string
		var started, finished *time.Time
		var created time.Time
		if err := rows.Scan(&id, &env, &name, &url, &started, &finished, &created); err != nil {
			serverError(w)
			return
		}
		out = append(out, deployJSON(id, env, name, url, started, finished, &created))
	}
	writeJSON(w, http.StatusOK, out)
}
