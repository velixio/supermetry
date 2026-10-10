package platformhttp

import (
	"errors"
	"net/http"
	"strings"
	"time"
	"unicode/utf8"

	"github.com/jackc/pgx/v5"

	"supermetry/internal/platform/audit"
	"supermetry/internal/platform/authz"
	"supermetry/internal/platform/store"
)

const annotationDateLayout = "2006-01-02"

type annotationInput struct {
	Day  string `json:"day"`
	Text string `json:"text"`
}

type annotationPatch struct {
	Day  *string `json:"day"`
	Text *string `json:"text"`
}

type annotationResponse struct {
	ID        int64     `json:"id"`
	SiteID    int32     `json:"site_id"`
	Day       string    `json:"day"`
	Text      string    `json:"text"`
	CreatedBy *int64    `json:"created_by"`
	CreatedAt time.Time `json:"created_at"`
}

func (h *handler) listAnnotations(w http.ResponseWriter, r *http.Request) {
	_, site, ok := h.sitePrincipal(w, r, authz.ActionStatsRead, h.resourceRoles(r)...)
	if !ok {
		return
	}
	from, to, ok := annotationRange(w, r)
	if !ok {
		return
	}
	if h.deps.Store == nil {
		unavailable(w, "annotation")
		return
	}
	rows, err := h.deps.Store.Pool.Query(r.Context(), `
		select id, site_id, day::text, text, created_by, created_at
		from annotations
		where site_id=$1 and day between $2 and $3
		order by day, id`, site.ID, from, to)
	if err != nil {
		serviceError(w, err)
		return
	}
	defer rows.Close()
	result := make([]annotationResponse, 0)
	for rows.Next() {
		var annotation annotationResponse
		if err := rows.Scan(&annotation.ID, &annotation.SiteID, &annotation.Day, &annotation.Text, &annotation.CreatedBy, &annotation.CreatedAt); err != nil {
			serviceError(w, err)
			return
		}
		result = append(result, annotation)
	}
	if err := rows.Err(); err != nil {
		serviceError(w, err)
		return
	}
	writeJSON(w, http.StatusOK, result)
}

func (h *handler) createAnnotation(w http.ResponseWriter, r *http.Request) {
	principal, site, ok := h.sitePrincipal(w, r, authz.ActionAnnotationsEdit, h.resourceRoles(r)...)
	if !ok {
		return
	}
	var input annotationInput
	if !decodeJSON(w, r, &input) {
		return
	}
	day, text, valid := validAnnotation(input.Day, input.Text)
	if !valid {
		writeProblem(w, http.StatusBadRequest, "Bad request", "annotation day or text is invalid")
		return
	}
	if h.deps.Store == nil {
		unavailable(w, "annotation")
		return
	}
	created := annotationResponse{SiteID: site.ID, Day: day, Text: text}
	err := h.deps.Store.Tx(r.Context(), func(tx pgx.Tx) error {
		var err error
		parsedDay, err := time.Parse(annotationDateLayout, day)
		if err != nil {
			return err
		}
		created.ID, err = store.InsertAnnotation(r.Context(), tx, site.ID, parsedDay, text, principal.UserID)
		if err != nil {
			return err
		}
		if err := tx.QueryRow(r.Context(), `select created_by,created_at from annotations where id=$1`, created.ID).
			Scan(&created.CreatedBy, &created.CreatedAt); err != nil {
			return err
		}
		return audit.Write(r.Context(), tx, principal, "annotation.created", "annotation", created.ID, map[string]any{"site_id": site.ID})
	})
	if err != nil {
		serviceError(w, err)
		return
	}
	h.invalidateDefinitions()
	writeJSON(w, http.StatusCreated, created)
}

func (h *handler) updateAnnotation(w http.ResponseWriter, r *http.Request) {
	principal, annotation, ok := h.annotationPrincipal(w, r)
	if !ok {
		return
	}
	var patch annotationPatch
	if !decodeJSON(w, r, &patch) {
		return
	}
	if patch.Day == nil && patch.Text == nil {
		writeProblem(w, http.StatusBadRequest, "Bad request", "annotation patch must include day or text")
		return
	}
	day, text := annotation.Day, annotation.Text
	if patch.Day != nil {
		day = *patch.Day
	}
	if patch.Text != nil {
		text = *patch.Text
	}
	day, text, valid := validAnnotation(day, text)
	if !valid {
		writeProblem(w, http.StatusBadRequest, "Bad request", "annotation day or text is invalid")
		return
	}
	err := h.deps.Store.Tx(r.Context(), func(tx pgx.Tx) error {
		if _, err := tx.Exec(r.Context(), `update annotations set day=$2,text=$3 where id=$1`, annotation.ID, day, text); err != nil {
			return err
		}
		return audit.Write(r.Context(), tx, principal, "annotation.updated", "annotation", annotation.ID, map[string]any{"site_id": annotation.SiteID})
	})
	if err != nil {
		serviceError(w, err)
		return
	}
	h.invalidateDefinitions()
	annotation.Day, annotation.Text = day, text
	writeJSON(w, http.StatusOK, annotation)
}

func (h *handler) deleteAnnotation(w http.ResponseWriter, r *http.Request) {
	principal, annotation, ok := h.annotationPrincipal(w, r)
	if !ok {
		return
	}
	err := h.deps.Store.Tx(r.Context(), func(tx pgx.Tx) error {
		if _, err := tx.Exec(r.Context(), `delete from annotations where id=$1`, annotation.ID); err != nil {
			return err
		}
		return audit.Write(r.Context(), tx, principal, "annotation.deleted", "annotation", annotation.ID, map[string]any{"site_id": annotation.SiteID})
	})
	if err != nil {
		serviceError(w, err)
		return
	}
	h.invalidateDefinitions()
	w.WriteHeader(http.StatusNoContent)
}

func (h *handler) annotationPrincipal(w http.ResponseWriter, r *http.Request) (authz.Principal, annotationResponse, bool) {
	principal, ok := h.principal(w, r)
	if !ok {
		return authz.Principal{}, annotationResponse{}, false
	}
	id, err := parseInt64(r.PathValue("annotation"))
	if err != nil || h.deps.Store == nil {
		writeProblem(w, http.StatusNotFound, "Not found", "annotation not found")
		return authz.Principal{}, annotationResponse{}, false
	}
	var annotation annotationResponse
	err = h.deps.Store.Pool.QueryRow(r.Context(), `
		select id,site_id,day::text,text,created_by,created_at
		from annotations where id=$1`, id).
		Scan(&annotation.ID, &annotation.SiteID, &annotation.Day, &annotation.Text, &annotation.CreatedBy, &annotation.CreatedAt)
	if errors.Is(err, pgx.ErrNoRows) {
		writeProblem(w, http.StatusNotFound, "Not found", "annotation not found")
		return authz.Principal{}, annotationResponse{}, false
	}
	if err != nil {
		serviceError(w, err)
		return authz.Principal{}, annotationResponse{}, false
	}
	if _, _, ok := h.visibleSite(w, principal, annotation.SiteID); !ok {
		return authz.Principal{}, annotationResponse{}, false
	}
	if !roleAllowed(principal.Role, h.resourceRoles(r)) || !authz.Can(principal, authz.ActionAnnotationsEdit, annotation.SiteID) {
		writeProblem(w, http.StatusForbidden, "Forbidden", "the principal cannot perform this action")
		return authz.Principal{}, annotationResponse{}, false
	}
	return principal, annotation, true
}

func annotationRange(w http.ResponseWriter, r *http.Request) (string, string, bool) {
	from := r.URL.Query().Get("from")
	to := r.URL.Query().Get("to")
	fromTime, fromErr := time.Parse(annotationDateLayout, from)
	toTime, toErr := time.Parse(annotationDateLayout, to)
	if fromErr != nil || toErr != nil || fromTime.After(toTime) {
		writeProblem(w, http.StatusBadRequest, "Bad request", "from and to must be valid dates in ascending order")
		return "", "", false
	}
	return from, to, true
}

func validAnnotation(dayValue, textValue string) (string, string, bool) {
	dayValue = strings.TrimSpace(dayValue)
	textValue = strings.TrimSpace(textValue)
	parsed, err := time.Parse(annotationDateLayout, dayValue)
	if err != nil || parsed.Format(annotationDateLayout) != dayValue || textValue == "" || utf8.RuneCountInString(textValue) > 200 {
		return "", "", false
	}
	return dayValue, textValue, true
}
