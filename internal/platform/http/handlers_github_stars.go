package platformhttp

import (
	"encoding/json"
	"errors"
	"net/http"
	"regexp"
	"strconv"
	"time"

	"supermetry/internal/ghstars"
	"supermetry/internal/platform/authz"
)

// githubLogin is the shape GitHub allows for user names.
var githubLogin = regexp.MustCompile(`^[A-Za-z0-9](?:[A-Za-z0-9-]{0,38})$`)

// starsService authenticates an organization member for the {org} in the path
// and returns the star service. manage additionally requires an owner or admin.
// A member of another organization, or a missing organization, gets the same
// answers every other organization route gives.
func (h *handler) starsService(w http.ResponseWriter, r *http.Request, manage bool) (*ghstars.Service, authz.Principal, int64, bool) {
	action := authz.ActionGitHubStarsRead
	if manage {
		action = authz.ActionGitHubStarsManage
	}
	principal, ok := h.orgManager(w, r, action)
	if !ok {
		return nil, authz.Principal{}, 0, false
	}
	if h.deps.GitHubStars == nil {
		writeProblem(w, http.StatusNotFound, "Not found", "GitHub star tracking is unavailable")
		return nil, authz.Principal{}, 0, false
	}
	return h.deps.GitHubStars, principal, principal.OrgID, true
}

// starsStore additionally resolves the required repo_id query parameter to
// that repository's data store, which must belong to the organization.
func (h *handler) starsStore(w http.ResponseWriter, r *http.Request, manage bool) (*ghstars.Store, authz.Principal, bool) {
	svc, principal, orgID, ok := h.starsService(w, r, manage)
	if !ok {
		return nil, authz.Principal{}, false
	}
	id, err := strconv.ParseInt(r.URL.Query().Get("repo_id"), 10, 64)
	if err != nil || id < 1 {
		writeProblem(w, http.StatusBadRequest, "Bad request", "repo_id is required")
		return nil, authz.Principal{}, false
	}
	store, _, err := svc.StoreFor(r.Context(), orgID, id)
	if err != nil {
		starsError(w, err)
		return nil, authz.Principal{}, false
	}
	return store, principal, true
}

func starsError(w http.ResponseWriter, err error) {
	switch {
	case errors.Is(err, ghstars.ErrInvalid):
		writeProblem(w, http.StatusBadRequest, "Bad request", err.Error())
	case errors.Is(err, ghstars.ErrNotFound):
		writeProblem(w, http.StatusNotFound, "Not found", "resource not found")
	case errors.Is(err, ghstars.ErrExists), errors.Is(err, ghstars.ErrLimit), errors.Is(err, ghstars.ErrBusy):
		writeProblem(w, http.StatusConflict, "Conflict", err.Error())
	case errors.Is(err, ghstars.ErrTooSoon):
		w.Header().Set("Retry-After", "60")
		writeProblem(w, http.StatusTooManyRequests, "Rate limit exceeded", err.Error())
	default:
		serviceError(w, err)
	}
}

type starsRepoSecret struct {
	Repo          ghstars.Repo `json:"repo"`
	WebhookSecret string       `json:"webhook_secret"`
	WebhookURL    string       `json:"webhook_url"`
}

func (h *handler) orgStarsRepos(w http.ResponseWriter, r *http.Request) {
	svc, principal, orgID, ok := h.starsService(w, r, false)
	if !ok {
		return
	}
	_ = principal
	repos, err := svc.List(r.Context(), orgID)
	if err != nil {
		starsError(w, err)
		return
	}
	writeJSON(w, http.StatusOK, map[string]any{"data": repos})
}

func (h *handler) orgStarsAddRepo(w http.ResponseWriter, r *http.Request) {
	svc, principal, orgID, ok := h.starsService(w, r, true)
	if !ok {
		return
	}
	actor := principal.UserID
	var request struct {
		Repo     string `json:"repo"`
		Token    string `json:"token"`
		TimeZone string `json:"time_zone"`
		// PublicCounter publishes the star count at /api/github/stars/{owner}/{name}.
		PublicCounter bool `json:"public_counter"`
	}
	if !decodeJSON(w, r, &request) {
		return
	}
	repo, secret, err := svc.Create(r.Context(), orgID, ghstars.CreateInput{Repo: request.Repo, Token: request.Token, TimeZone: request.TimeZone, PublicCounter: request.PublicCounter}, actor)
	if err != nil {
		starsError(w, err)
		return
	}
	writeJSON(w, http.StatusCreated, starsRepoSecret{Repo: repo, WebhookSecret: secret, WebhookURL: repo.WebhookURL})
}

func (h *handler) orgStarsUpdateRepo(w http.ResponseWriter, r *http.Request) {
	svc, principal, orgID, ok := h.starsService(w, r, true)
	if !ok {
		return
	}
	actor := principal.UserID
	id, valid := adminID(r, "id")
	if !valid {
		writeProblem(w, http.StatusBadRequest, "Bad request", "repository id is invalid")
		return
	}
	var request struct {
		Enabled       *bool   `json:"enabled"`
		TimeZone      *string `json:"time_zone"`
		Token         *string `json:"token"`
		PublicCounter *bool   `json:"public_counter"`
		ClearToken    bool    `json:"clear_token"`
	}
	if !decodeJSON(w, r, &request) {
		return
	}
	repo, err := svc.Update(r.Context(), orgID, id, ghstars.UpdateInput{Enabled: request.Enabled, TimeZone: request.TimeZone, Token: request.Token, PublicCounter: request.PublicCounter, ClearToken: request.ClearToken}, actor)
	if err != nil {
		starsError(w, err)
		return
	}
	writeJSON(w, http.StatusOK, repo)
}

func (h *handler) orgStarsDeleteRepo(w http.ResponseWriter, r *http.Request) {
	svc, principal, orgID, ok := h.starsService(w, r, true)
	if !ok {
		return
	}
	actor := principal.UserID
	id, valid := adminID(r, "id")
	deleteData, err := strconv.ParseBool(r.URL.Query().Get("delete_data"))
	if !valid || err != nil {
		writeProblem(w, http.StatusBadRequest, "Bad request", "repository id and delete_data (true or false) are required")
		return
	}
	removed, err := svc.Delete(r.Context(), orgID, id, deleteData, actor)
	if err != nil {
		starsError(w, err)
		return
	}
	if removed {
		w.WriteHeader(http.StatusNoContent)
		return
	}
	repo, err := svc.Get(r.Context(), orgID, id)
	if err != nil {
		starsError(w, err)
		return
	}
	writeJSON(w, http.StatusOK, repo)
}

func (h *handler) orgStarsRotateSecret(w http.ResponseWriter, r *http.Request) {
	svc, principal, orgID, ok := h.starsService(w, r, true)
	if !ok {
		return
	}
	actor := principal.UserID
	id, valid := adminID(r, "id")
	if !valid {
		writeProblem(w, http.StatusBadRequest, "Bad request", "repository id is invalid")
		return
	}
	repo, secret, err := svc.RotateSecret(r.Context(), orgID, id, actor)
	if err != nil {
		starsError(w, err)
		return
	}
	writeJSON(w, http.StatusOK, starsRepoSecret{Repo: repo, WebhookSecret: secret, WebhookURL: repo.WebhookURL})
}

func (h *handler) orgStarsSync(w http.ResponseWriter, r *http.Request) {
	svc, principal, orgID, ok := h.starsService(w, r, true)
	if !ok {
		return
	}
	_ = principal
	id, valid := adminID(r, "id")
	if !valid {
		writeProblem(w, http.StatusBadRequest, "Bad request", "repository id is invalid")
		return
	}
	if err := svc.StartSync(r.Context(), orgID, id); err != nil {
		starsError(w, err)
		return
	}
	w.WriteHeader(http.StatusAccepted)
}

func (h *handler) orgStarsSummary(w http.ResponseWriter, r *http.Request) {
	stars, principal, ok := h.starsStore(w, r, false)
	if !ok {
		return
	}
	_ = principal
	summary, err := stars.Summary(r.Context(), h.deps.Now())
	if err != nil {
		starsError(w, err)
		return
	}
	writeJSON(w, http.StatusOK, summary)
}

func (h *handler) orgStarsSeries(w http.ResponseWriter, r *http.Request) {
	stars, principal, ok := h.starsStore(w, r, false)
	if !ok {
		return
	}
	_ = principal
	query := r.URL.Query()
	granularity := query.Get("granularity")
	now := h.deps.Now()
	to, from := now, now.Add(-24*time.Hour)
	switch granularity {
	case "day":
		from = now.AddDate(0, 0, -30)
	case "month":
		// The last 12 calendar months in the repository's time zone, but not before
		// the first event or snapshot on record.
		local := now.In(stars.Location())
		from = time.Date(local.Year(), local.Month()-11, 1, 0, 0, 0, 0, stars.Location())
		if first, ok, err := stars.FirstActivity(r.Context()); err == nil && ok && first.After(from) {
			from = first
		}
	}
	for name, target := range map[string]*time.Time{"from": &from, "to": &to} {
		if raw := query.Get(name); raw != "" {
			parsed, err := time.Parse(time.RFC3339, raw)
			if err != nil {
				writeProblem(w, http.StatusBadRequest, "Bad request", name+" must be an RFC 3339 time")
				return
			}
			*target = parsed
		}
	}
	points, err := stars.Series(r.Context(), granularity, from, to)
	if err != nil {
		starsError(w, err)
		return
	}
	writeJSON(w, http.StatusOK, map[string]any{"time_zone": stars.Location().String(), "data": points})
}

func (h *handler) orgStarsEvents(w http.ResponseWriter, r *http.Request) {
	stars, principal, ok := h.starsStore(w, r, false)
	if !ok {
		return
	}
	_ = principal
	limit := 50
	if raw := r.URL.Query().Get("limit"); raw != "" {
		parsed, err := strconv.Atoi(raw)
		if err != nil {
			writeProblem(w, http.StatusBadRequest, "Bad request", "limit is invalid")
			return
		}
		limit = parsed
	}
	events, next, err := stars.Events(r.Context(), r.URL.Query().Get("cursor"), limit)
	if err != nil {
		starsError(w, err)
		return
	}
	var cursor *string
	if next != "" {
		cursor = &next
	}
	writeJSON(w, http.StatusOK, map[string]any{"data": events, "next_cursor": cursor})
}

func (h *handler) orgStarsAnnotations(w http.ResponseWriter, r *http.Request) {
	stars, principal, ok := h.starsStore(w, r, false)
	if !ok {
		return
	}
	_ = principal
	items, err := stars.Annotations(r.Context())
	if err != nil {
		starsError(w, err)
		return
	}
	writeJSON(w, http.StatusOK, map[string]any{"data": items})
}

func (h *handler) orgStarsAddAnnotation(w http.ResponseWriter, r *http.Request) {
	stars, principal, ok := h.starsStore(w, r, true)
	if !ok {
		return
	}
	actor := principal.UserID
	var request struct {
		At    time.Time `json:"at"`
		Label string    `json:"label"`
	}
	if !decodeJSON(w, r, &request) {
		return
	}
	if request.At.IsZero() {
		writeProblem(w, http.StatusBadRequest, "Bad request", "at is required")
		return
	}
	item, err := stars.AddAnnotation(r.Context(), request.At, request.Label, actor)
	if err != nil {
		starsError(w, err)
		return
	}
	writeJSON(w, http.StatusCreated, item)
}

func (h *handler) orgStarsDeleteAnnotation(w http.ResponseWriter, r *http.Request) {
	stars, principal, ok := h.starsStore(w, r, true)
	if !ok {
		return
	}
	_ = principal
	id, valid := adminID(r, "annotation")
	if !valid {
		writeProblem(w, http.StatusBadRequest, "Bad request", "annotation id is invalid")
		return
	}
	if err := stars.DeleteAnnotation(r.Context(), id); err != nil {
		starsError(w, err)
		return
	}
	w.WriteHeader(http.StatusNoContent)
}

func (h *handler) orgStarsEraseUser(w http.ResponseWriter, r *http.Request) {
	stars, principal, ok := h.starsStore(w, r, true)
	if !ok {
		return
	}
	actor := principal.UserID
	login := r.PathValue("login")
	if !githubLogin.MatchString(login) {
		writeProblem(w, http.StatusBadRequest, "Bad request", "login is invalid")
		return
	}
	deleted, err := stars.EraseUser(r.Context(), login, actor)
	if err != nil {
		starsError(w, err)
		return
	}
	writeJSON(w, http.StatusOK, map[string]any{"deleted_events": deleted})
}

// adminGitHubStarRepoLimit shows how many GitHub repositories an organization
// may track and how many it tracks now.
func (h *handler) adminGitHubStarRepoLimit(w http.ResponseWriter, r *http.Request) {
	if _, ok := h.adminPrincipal(w, r); !ok {
		return
	}
	orgID, ok := adminID(r, "org")
	if !ok || h.deps.GitHubStars == nil {
		writeProblem(w, http.StatusBadRequest, "Bad request", "organization is invalid")
		return
	}
	limit, err := h.deps.GitHubStars.GetOrgLimit(r.Context(), orgID)
	if err != nil {
		starsError(w, err)
		return
	}
	writeJSON(w, http.StatusOK, limit)
}

// adminSetGitHubStarRepoLimit sets an organization's own limit: a number, 0 for
// none, or null for unlimited. Lowering it never removes repositories.
func (h *handler) adminSetGitHubStarRepoLimit(w http.ResponseWriter, r *http.Request) {
	principal, ok := h.adminPrincipal(w, r)
	if !ok {
		return
	}
	orgID, valid := adminID(r, "org")
	if !valid || h.deps.GitHubStars == nil {
		writeProblem(w, http.StatusBadRequest, "Bad request", "organization is invalid")
		return
	}
	var request struct {
		Limit json.RawMessage `json:"limit"`
	}
	if !decodeJSON(w, r, &request) {
		return
	}
	var limit *int
	switch raw := string(request.Limit); {
	case raw == "":
		writeProblem(w, http.StatusBadRequest, "Bad request", "limit is required: a number, 0 for none, or null for unlimited")
		return
	case raw != "null":
		var n int
		if err := json.Unmarshal(request.Limit, &n); err != nil || n < 0 {
			writeProblem(w, http.StatusBadRequest, "Bad request", "limit must be a whole number of 0 or more, or null")
			return
		}
		limit = &n
	}
	if err := h.deps.GitHubStars.SetOrgLimit(r.Context(), orgID, limit, principal.UserID); err != nil {
		starsError(w, err)
		return
	}
	h.adminGitHubStarRepoLimitResponse(w, r, orgID)
}

// adminResetGitHubStarRepoLimit removes an organization's own limit so the
// instance default applies again.
func (h *handler) adminResetGitHubStarRepoLimit(w http.ResponseWriter, r *http.Request) {
	principal, ok := h.adminPrincipal(w, r)
	if !ok {
		return
	}
	orgID, valid := adminID(r, "org")
	if !valid || h.deps.GitHubStars == nil {
		writeProblem(w, http.StatusBadRequest, "Bad request", "organization is invalid")
		return
	}
	if err := h.deps.GitHubStars.ResetOrgLimit(r.Context(), orgID, principal.UserID); err != nil {
		starsError(w, err)
		return
	}
	h.adminGitHubStarRepoLimitResponse(w, r, orgID)
}

func (h *handler) adminGitHubStarRepoLimitResponse(w http.ResponseWriter, r *http.Request, orgID int64) {
	limit, err := h.deps.GitHubStars.GetOrgLimit(r.Context(), orgID)
	if err != nil {
		starsError(w, err)
		return
	}
	writeJSON(w, http.StatusOK, limit)
}
