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

// starsStore authenticates the instance admin and returns the star store. It
// answers 404 once signed in when star tracking is not configured.
func (h *handler) starsStore(w http.ResponseWriter, r *http.Request) (*ghstars.Store, int64, bool) {
	principal, ok := h.adminPrincipal(w, r)
	if !ok {
		return nil, 0, false
	}
	if h.deps.GitHubStars == nil {
		writeProblem(w, http.StatusNotFound, "Not found", "GitHub star tracking is not configured")
		return nil, 0, false
	}
	return h.deps.GitHubStars, principal.UserID, true
}

func starsError(w http.ResponseWriter, err error) {
	switch {
	case errors.Is(err, ghstars.ErrInvalid):
		writeProblem(w, http.StatusBadRequest, "Bad request", err.Error())
	case errors.Is(err, ghstars.ErrNotFound):
		writeProblem(w, http.StatusNotFound, "Not found", "resource not found")
	default:
		serviceError(w, err)
	}
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
