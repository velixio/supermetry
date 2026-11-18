package sentryapi

import (
	"context"
	"errors"
	"net/http"
	"slices"
	"strconv"
	"time"

	"github.com/jackc/pgx/v5"

	"supermetry/internal/platform/authz"
	"supermetry/internal/sites"
)

type releaseBody struct {
	Version      string        `json:"version"`
	Projects     projectList   `json:"projects"`
	URL          *string       `json:"url"`
	Ref          *string       `json:"ref"`
	DateReleased *time.Time    `json:"dateReleased"`
	DateStarted  *time.Time    `json:"dateStarted"`
	Status       *string       `json:"status"`
	Commits      *[]commitBody `json:"commits"`
	Refs         *[]refBody    `json:"refs"`
}

// validate checks the optional fields shared by create and update.
func (b releaseBody) validate() string {
	if b.Status != nil && *b.Status != "open" && *b.Status != "archived" {
		return "status must be open or archived"
	}
	if b.Ref != nil && len(*b.Ref) > 200 {
		return "ref is too long"
	}
	return ""
}

type releaseRow struct {
	id           int64
	version      string
	siteIDs      []int32
	url          string
	ref          string
	status       string
	dateCreated  time.Time
	dateReleased *time.Time
	dateStarted  *time.Time
}

const releaseColumns = `id, version, site_ids, url, ref, status, date_created, date_released, date_started`

func (r *releaseRow) scan(row pgx.Row) error {
	return row.Scan(&r.id, &r.version, &r.siteIDs, &r.url, &r.ref, &r.status, &r.dateCreated, &r.dateReleased, &r.dateStarted)
}

func formatTime(t *time.Time) any {
	if t == nil {
		return nil
	}
	return t.UTC().Format(time.RFC3339Nano)
}

// visibleSites is the release's own sites, or every app site of the org when
// it is org-wide, narrowed to the key's site restriction.
func (h *Handler) visibleSites(c *call, rel releaseRow) []int32 {
	ids := slices.Clone(rel.siteIDs)
	if len(ids) == 0 && h.deps.Sites != nil {
		for _, s := range h.deps.Sites.All() {
			if s.OrgID == c.orgID && s.Kind == sites.KindApp {
				ids = append(ids, s.ID)
			}
		}
	}
	if c.principal.SiteIDs != nil {
		ids = slices.DeleteFunc(ids, func(id int32) bool { return !slices.Contains(c.principal.SiteIDs, id) })
	}
	return ids
}

func (h *Handler) releaseJSON(ctx context.Context, c *call, rel releaseRow) map[string]any {
	var deploys int
	var lastDeploy any
	_ = h.deps.Pool.QueryRow(ctx, `select count(*) from app_deploys where release_id=$1`, rel.id).Scan(&deploys)
	if deploys > 0 {
		var id int64
		var env, name string
		var started, finished *time.Time
		err := h.deps.Pool.QueryRow(ctx, `select id, environment, name, date_started, date_finished from app_deploys where release_id=$1 order by id desc limit 1`, rel.id).Scan(&id, &env, &name, &started, &finished)
		if err == nil {
			lastDeploy = map[string]any{"id": strconv.FormatInt(id, 10), "environment": env, "name": nilIfEmpty(name), "dateStarted": formatTime(started), "dateFinished": formatTime(finished)}
		}
	}
	// One aggregate over the release's visible sites; the (site_id, first_release) index backs it.
	var newGroups int
	var firstEvent, lastEvent *time.Time
	if visible := h.visibleSites(c, rel); len(visible) > 0 {
		_ = h.deps.Pool.QueryRow(ctx, `select count(*), min(first_seen), max(last_seen) from app_issues where first_release=$1 and site_id = any($2)`, rel.version, visible).Scan(&newGroups, &firstEvent, &lastEvent)
	}
	commitCount, lastCommit, _ := h.releaseCommitSummary(ctx, rel.id)
	projects := []map[string]any{}
	for _, id := range rel.siteIDs {
		if s, ok := h.deps.Sites.Site(id); ok && s.OrgID == c.orgID {
			projects = append(projects, map[string]any{"id": id, "slug": s.PublicID, "name": s.PublicID})
		}
	}
	out := map[string]any{
		"version": rel.version, "shortVersion": rel.version, "ref": nilIfEmpty(rel.ref), "url": nilIfEmpty(rel.url),
		"dateCreated": rel.dateCreated.UTC().Format(time.RFC3339Nano), "dateReleased": formatTime(rel.dateReleased), "dateStarted": formatTime(rel.dateStarted),
		"status": rel.status, "commitCount": commitCount, "deployCount": deploys, "newGroups": newGroups,
		"firstEvent": formatTime(firstEvent), "lastEvent": formatTime(lastEvent), "lastDeploy": lastDeploy,
		"authors": []any{}, "projects": projects,
	}
	if lastCommit != nil {
		out["lastCommit"] = lastCommit
	}
	return out
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
	err := rel.scan(h.deps.Pool.QueryRow(ctx, `select `+releaseColumns+` from app_releases where org_id=$1 and version=$2`, orgID, version))
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
	if msg := body.validate(); msg != "" {
		writeDetail(w, http.StatusBadRequest, msg)
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
	ref, relStatus := "", "open"
	if body.Ref != nil {
		ref = *body.Ref
	}
	if body.Status != nil {
		relStatus = *body.Status
	}
	status := http.StatusCreated
	var inserted bool
	err := h.deps.Pool.QueryRow(r.Context(), `
		insert into app_releases(org_id, version, site_ids, url, ref, status, date_released, date_started) values ($1,$2,$3,$4,$5,$6,$7,$8)
		on conflict (org_id, version) do update set version = app_releases.version
		returning (xmax = 0)`, c.orgID, body.Version, siteIDs, url, ref, relStatus, body.DateReleased, body.DateStarted).Scan(&inserted)
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
	if !inserted && body.Status != nil && *body.Status != rel.status {
		// sentry-cli archive and restore re-POST the release with a status.
		if _, err := h.deps.Pool.Exec(r.Context(), `update app_releases set status=$3 where org_id=$1 and version=$2`, c.orgID, body.Version, *body.Status); err != nil {
			serverError(w)
			return
		}
		if rel, err = h.loadRelease(r.Context(), c.orgID, body.Version); err != nil {
			serverError(w)
			return
		}
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
	if !readJSONMax(w, r, &body, maxReleaseBody) {
		return
	}
	if msg := body.validate(); msg != "" {
		writeDetail(w, http.StatusBadRequest, msg)
		return
	}
	var commits []commitBody
	var refs []refBody
	if body.Commits != nil {
		var msg string
		if commits, msg = normalizeCommits(*body.Commits); msg != "" {
			writeDetail(w, http.StatusBadRequest, msg)
			return
		}
	}
	if body.Refs != nil {
		var msg string
		if refs, msg = normalizeRefs(*body.Refs); msg != "" {
			writeDetail(w, http.StatusBadRequest, msg)
			return
		}
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
	if body.DateStarted != nil {
		rel.dateStarted = body.DateStarted
	}
	if body.Ref != nil {
		rel.ref = *body.Ref
	}
	if body.Status != nil {
		rel.status = *body.Status
	}
	if _, err := h.deps.Pool.Exec(r.Context(), `update app_releases set site_ids=$2, url=$3, date_released=$4, date_started=$5, ref=$6, status=$7 where id=$1`, rel.id, rel.siteIDs, rel.url, rel.dateReleased, rel.dateStarted, rel.ref, rel.status); err != nil {
		serverError(w)
		return
	}
	if body.Commits != nil || body.Refs != nil {
		if err := replaceReleaseCommits(r.Context(), h.deps.Pool, c.orgID, rel.id, commits, body.Commits != nil, refs, body.Refs != nil); err != nil {
			serverError(w)
			return
		}
	}
	writeJSON(w, http.StatusOK, h.releaseJSON(r.Context(), c, rel))
}

type deployBody struct {
	Environment  string      `json:"environment"`
	Name         string      `json:"name"`
	URL          string      `json:"url"`
	DateStarted  *time.Time  `json:"dateStarted"`
	DateFinished *time.Time  `json:"dateFinished"`
	Projects     projectList `json:"projects"`
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

// canUseDeploys reports whether the principal may read and write the deploys
// of a release: unrestricted keys always, a restricted key when the release is
// bound to at least one of its sites (an org-wide release is out of reach).
func (c *call) canUseDeploys(rel releaseRow) bool {
	if c.principal.SiteIDs == nil {
		return true
	}
	return slices.ContainsFunc(rel.siteIDs, func(id int32) bool { return authz.Can(c.principal, authz.ActionReleasesWrite, id) })
}

// deploySites is the project list stored with a deploy. Named projects win;
// otherwise the release's projects, narrowed to the key's restriction, so a
// restricted key never creates an org-wide deploy.
func (h *Handler) deploySites(c *call, rel releaseRow, slugs []string) ([]int32, *projectError) {
	if len(slugs) > 0 {
		return h.resolveProjects(c, slugs)
	}
	ids := slices.Clone(rel.siteIDs)
	if c.principal.SiteIDs == nil {
		return ids, nil
	}
	if len(ids) == 0 {
		return slices.Clone(c.principal.SiteIDs), nil
	}
	return slices.DeleteFunc(ids, func(id int32) bool { return !authz.Can(c.principal, authz.ActionReleasesWrite, id) }), nil
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
	if !c.canUseDeploys(rel) {
		notFound(w)
		return
	}
	siteIDs, perr := h.deploySites(c, rel, body.Projects)
	if perr != nil {
		writeDetail(w, perr.status, perr.detail)
		return
	}
	if siteIDs == nil {
		siteIDs = []int32{}
	}
	var id int64
	var created time.Time
	err = h.deps.Pool.QueryRow(r.Context(), `insert into app_deploys(release_id, org_id, site_ids, environment, name, url, date_started, date_finished) values ($1,$2,$3,$4,$5,$6,$7,$8) returning id, date_created`,
		rel.id, c.orgID, siteIDs, body.Environment, body.Name, body.URL, body.DateStarted, body.DateFinished).Scan(&id, &created)
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
	if !c.canUseDeploys(rel) {
		notFound(w)
		return
	}
	var restrict any // nil: no key restriction
	if c.principal.SiteIDs != nil {
		restrict = c.principal.SiteIDs
	}
	rows, err := h.deps.Pool.Query(r.Context(), `select d.id, d.environment, d.name, d.url, d.date_started, d.date_finished, d.date_created
from app_deploys d join app_releases r on r.id=d.release_id
where d.release_id=$1 and ($2::int[] is null or case when cardinality(d.site_ids)>0 then d.site_ids else r.site_ids end && $2::int[])
order by d.id desc limit 100`, rel.id, restrict)
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
