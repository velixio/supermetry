package platformhttp

import (
	"errors"
	"net/http"
	"regexp"
	"strconv"
	"time"

	"supermetry/internal/ghstars"
)

// githubLogin is the shape GitHub allows for user names.
var githubLogin = regexp.MustCompile(`^[A-Za-z0-9](?:[A-Za-z0-9-]{0,38})$`)

// starsService authenticates the instance admin and returns the star service.
func (h *handler) starsService(w http.ResponseWriter, r *http.Request) (*ghstars.Service, int64, bool) {
	principal, ok := h.adminPrincipal(w, r)
	if !ok {
		return nil, 0, false
	}
	if h.deps.GitHubStars == nil {
		writeProblem(w, http.StatusNotFound, "Not found", "GitHub star tracking is unavailable")
		return nil, 0, false
	}
	return h.deps.GitHubStars, principal.UserID, true
}

// starsStore additionally resolves the required repo_id query parameter to
// that repository's data store.
func (h *handler) starsStore(w http.ResponseWriter, r *http.Request) (*ghstars.Store, int64, bool) {
	svc, actor, ok := h.starsService(w, r)
	if !ok {
		return nil, 0, false
	}
	id, err := strconv.ParseInt(r.URL.Query().Get("repo_id"), 10, 64)
	if err != nil || id < 1 {
		writeProblem(w, http.StatusBadRequest, "Bad request", "repo_id is required")
		return nil, 0, false
	}
	store, _, err := svc.StoreFor(r.Context(), id)
	if err != nil {
		starsError(w, err)
		return nil, 0, false
	}
	return store, actor, true
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

func (h *handler) adminStarsRepos(w http.ResponseWriter, r *http.Request) {
	svc, _, ok := h.starsService(w, r)
	if !ok {
		return
	}
	repos, err := svc.List(r.Context())
	if err != nil {
		starsError(w, err)
		return
	}
	writeJSON(w, http.StatusOK, map[string]any{"data": repos})
}

func (h *handler) adminStarsAddRepo(w http.ResponseWriter, r *http.Request) {
	svc, actor, ok := h.starsService(w, r)
	if !ok {
		return
	}
	var request struct {
		Repo     string `json:"repo"`
		Token    string `json:"token"`
		TimeZone string `json:"time_zone"`
	}
	if !decodeJSON(w, r, &request) {
		return
	}
	repo, secret, err := svc.Create(r.Context(), ghstars.CreateInput{Repo: request.Repo, Token: request.Token, TimeZone: request.TimeZone}, actor)
	if err != nil {
		starsError(w, err)
		return
	}
	writeJSON(w, http.StatusCreated, starsRepoSecret{Repo: repo, WebhookSecret: secret, WebhookURL: repo.WebhookURL})
}

func (h *handler) adminStarsUpdateRepo(w http.ResponseWriter, r *http.Request) {
	svc, actor, ok := h.starsService(w, r)
	if !ok {
		return
	}
	id, valid := adminID(r, "id")
	if !valid {
		writeProblem(w, http.StatusBadRequest, "Bad request", "repository id is invalid")
		return
	}
	var request struct {
		Enabled    *bool   `json:"enabled"`
		TimeZone   *string `json:"time_zone"`
		Token      *string `json:"token"`
		ClearToken bool    `json:"clear_token"`
	}
	if !decodeJSON(w, r, &request) {
		return
	}
	repo, err := svc.Update(r.Context(), id, ghstars.UpdateInput{Enabled: request.Enabled, TimeZone: request.TimeZone, Token: request.Token, ClearToken: request.ClearToken}, actor)
	if err != nil {
		starsError(w, err)
		return
	}
	writeJSON(w, http.StatusOK, repo)
}

func (h *handler) adminStarsDeleteRepo(w http.ResponseWriter, r *http.Request) {
	svc, actor, ok := h.starsService(w, r)
	if !ok {
		return
	}
	id, valid := adminID(r, "id")
	deleteData, err := strconv.ParseBool(r.URL.Query().Get("delete_data"))
	if !valid || err != nil {
		writeProblem(w, http.StatusBadRequest, "Bad request", "repository id and delete_data (true or false) are required")
		return
	}
	removed, err := svc.Delete(r.Context(), id, deleteData, actor)
	if err != nil {
		starsError(w, err)
		return
	}
	if removed {
		w.WriteHeader(http.StatusNoContent)
		return
	}
	repo, err := svc.Get(r.Context(), id)
	if err != nil {
		starsError(w, err)
		return
	}
	writeJSON(w, http.StatusOK, repo)
}

func (h *handler) adminStarsRotateSecret(w http.ResponseWriter, r *http.Request) {
	svc, actor, ok := h.starsService(w, r)
	if !ok {
		return
	}
	id, valid := adminID(r, "id")
	if !valid {
		writeProblem(w, http.StatusBadRequest, "Bad request", "repository id is invalid")
		return
	}
	repo, secret, err := svc.RotateSecret(r.Context(), id, actor)
	if err != nil {
		starsError(w, err)
		return
	}
	writeJSON(w, http.StatusOK, starsRepoSecret{Repo: repo, WebhookSecret: secret, WebhookURL: repo.WebhookURL})
}

func (h *handler) adminStarsSync(w http.ResponseWriter, r *http.Request) {
	svc, _, ok := h.starsService(w, r)
	if !ok {
		return
	}
	id, valid := adminID(r, "id")
	if !valid {
		writeProblem(w, http.StatusBadRequest, "Bad request", "repository id is invalid")
		return
	}
	if err := svc.StartSync(r.Context(), id); err != nil {
		starsError(w, err)
		return
	}
	w.WriteHeader(http.StatusAccepted)
}

func (h *handler) adminStarsSummary(w http.ResponseWriter, r *http.Request) {
	stars, _, ok := h.starsStore(w, r)
	if !ok {
		return
	}
	summary, err := stars.Summary(r.Context(), h.deps.Now())
	if err != nil {
		starsError(w, err)
		return
	}
	writeJSON(w, http.StatusOK, summary)
}

func (h *handler) adminStarsSeries(w http.ResponseWriter, r *http.Request) {
	stars, _, ok := h.starsStore(w, r)
	if !ok {
		return
	}
	query := r.URL.Query()
	granularity := query.Get("granularity")
	now := h.deps.Now()
	to, from := now, now.Add(-24*time.Hour)
	if granularity == "day" {
		from = now.AddDate(0, 0, -30)
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

func (h *handler) adminStarsEvents(w http.ResponseWriter, r *http.Request) {
	stars, _, ok := h.starsStore(w, r)
	if !ok {
		return
	}
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

func (h *handler) adminStarsAnnotations(w http.ResponseWriter, r *http.Request) {
	stars, _, ok := h.starsStore(w, r)
	if !ok {
		return
	}
	items, err := stars.Annotations(r.Context())
	if err != nil {
		starsError(w, err)
		return
	}
	writeJSON(w, http.StatusOK, map[string]any{"data": items})
}

func (h *handler) adminStarsAddAnnotation(w http.ResponseWriter, r *http.Request) {
	stars, actor, ok := h.starsStore(w, r)
	if !ok {
		return
	}
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

func (h *handler) adminStarsDeleteAnnotation(w http.ResponseWriter, r *http.Request) {
	stars, _, ok := h.starsStore(w, r)
	if !ok {
		return
	}
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

func (h *handler) adminStarsEraseUser(w http.ResponseWriter, r *http.Request) {
	stars, actor, ok := h.starsStore(w, r)
	if !ok {
		return
	}
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
