package platformhttp

import (
	"encoding/json"
	"errors"
	"net/http"
	"strings"
	"time"
	"unicode/utf8"

	"github.com/jackc/pgx/v5"
	"github.com/jackc/pgx/v5/pgconn"

	"panel/internal/api"
	"panel/internal/platform/audit"
	"panel/internal/platform/authz"
	"panel/internal/query"
)

type segmentInput struct {
	Name    string       `json:"name"`
	Filters []api.Filter `json:"filters"`
}

type segmentResponse struct {
	ID        int64        `json:"id"`
	SiteID    int32        `json:"site_id"`
	Name      string       `json:"name"`
	Filters   []api.Filter `json:"filters"`
	CreatedBy *int64       `json:"created_by"`
	CreatedAt time.Time    `json:"created_at"`
	UpdatedAt time.Time    `json:"updated_at"`
}

func (h *handler) listSegments(w http.ResponseWriter, r *http.Request) {
	_, site, ok := h.sitePrincipal(w, r, authz.ActionStatsRead, h.resourceRoles(r)...)
	if !ok {
		return
	}
	if h.deps.Store == nil {
		unavailable(w, "segment")
		return
	}
	rows, err := h.deps.Store.Pool.Query(r.Context(), `select id,site_id,name,filters,created_by,created_at,updated_at from segments where site_id=$1 order by lower(name),id`, site.ID)
	if err != nil {
		serviceError(w, err)
		return
	}
	defer rows.Close()
	result := make([]segmentResponse, 0)
	for rows.Next() {
		segment, err := scanSegment(rows.Scan)
		if err != nil {
			serviceError(w, err)
			return
		}
		result = append(result, segment)
	}
	if err := rows.Err(); err != nil {
		serviceError(w, err)
		return
	}
	writeJSON(w, http.StatusOK, result)
}

func (h *handler) createSegment(w http.ResponseWriter, r *http.Request) {
	principal, site, ok := h.sitePrincipal(w, r, authz.ActionSegmentsEdit, h.resourceRoles(r)...)
	if !ok {
		return
	}
	input, filtersJSON, ok := decodeSegmentInput(w, r)
	if !ok {
		return
	}
	if h.deps.Store == nil {
		unavailable(w, "segment")
		return
	}
	var created segmentResponse
	err := h.deps.Store.Tx(r.Context(), func(tx pgx.Tx) error {
		created.SiteID, created.Name, created.Filters = site.ID, input.Name, input.Filters
		if err := tx.QueryRow(r.Context(), `insert into segments(site_id,name,filters,created_by) values($1,$2,$3,$4) returning id,created_by,created_at,updated_at`, site.ID, input.Name, filtersJSON, createdByUser(principal)).Scan(&created.ID, &created.CreatedBy, &created.CreatedAt, &created.UpdatedAt); err != nil {
			return err
		}
		return audit.Write(r.Context(), tx, principal, "segment.created", "segment", created.ID, map[string]any{"site_id": site.ID})
	})
	if err != nil {
		segmentMutationError(w, err)
		return
	}
	writeJSON(w, http.StatusCreated, created)
}

func (h *handler) updateSegment(w http.ResponseWriter, r *http.Request) {
	principal, segment, ok := h.segmentPrincipal(w, r)
	if !ok {
		return
	}
	input, filtersJSON, ok := decodeSegmentInput(w, r)
	if !ok {
		return
	}
	err := h.deps.Store.Tx(r.Context(), func(tx pgx.Tx) error {
		if err := tx.QueryRow(r.Context(), `update segments set name=$2,filters=$3,updated_at=now() where id=$1 returning updated_at`, segment.ID, input.Name, filtersJSON).Scan(&segment.UpdatedAt); err != nil {
			return err
		}
		return audit.Write(r.Context(), tx, principal, "segment.updated", "segment", segment.ID, map[string]any{"site_id": segment.SiteID})
	})
	if err != nil {
		segmentMutationError(w, err)
		return
	}
	segment.Name, segment.Filters = input.Name, input.Filters
	writeJSON(w, http.StatusOK, segment)
}

func (h *handler) deleteSegment(w http.ResponseWriter, r *http.Request) {
	principal, segment, ok := h.segmentPrincipal(w, r)
	if !ok {
		return
	}
	err := h.deps.Store.Tx(r.Context(), func(tx pgx.Tx) error {
		if _, err := tx.Exec(r.Context(), `delete from segments where id=$1`, segment.ID); err != nil {
			return err
		}
		return audit.Write(r.Context(), tx, principal, "segment.deleted", "segment", segment.ID, map[string]any{"site_id": segment.SiteID})
	})
	if err != nil {
		serviceError(w, err)
		return
	}
	w.WriteHeader(http.StatusNoContent)
}

func (h *handler) segmentPrincipal(w http.ResponseWriter, r *http.Request) (authz.Principal, segmentResponse, bool) {
	principal, ok := h.principal(w, r)
	if !ok {
		return authz.Principal{}, segmentResponse{}, false
	}
	id, err := parseInt64(r.PathValue("segment"))
	if err != nil || h.deps.Store == nil {
		writeProblem(w, http.StatusNotFound, "Not found", "segment not found")
		return authz.Principal{}, segmentResponse{}, false
	}
	segment, err := scanSegment(func(dest ...any) error {
		return h.deps.Store.Pool.QueryRow(r.Context(), `select id,site_id,name,filters,created_by,created_at,updated_at from segments where id=$1`, id).Scan(dest...)
	})
	if errors.Is(err, pgx.ErrNoRows) {
		writeProblem(w, http.StatusNotFound, "Not found", "segment not found")
		return authz.Principal{}, segmentResponse{}, false
	}
	if err != nil {
		serviceError(w, err)
		return authz.Principal{}, segmentResponse{}, false
	}
	if _, _, ok := h.visibleSite(w, principal, segment.SiteID); !ok {
		return authz.Principal{}, segmentResponse{}, false
	}
	if !roleAllowed(principal.Role, h.resourceRoles(r)) || !authz.Can(principal, authz.ActionSegmentsEdit, segment.SiteID) {
		writeProblem(w, http.StatusForbidden, "Forbidden", "the principal cannot perform this action")
		return authz.Principal{}, segmentResponse{}, false
	}
	return principal, segment, true
}

func decodeSegmentInput(w http.ResponseWriter, r *http.Request) (segmentInput, []byte, bool) {
	var input segmentInput
	if !decodeJSON(w, r, &input) {
		return segmentInput{}, nil, false
	}
	input.Name = strings.TrimSpace(input.Name)
	if input.Name == "" || utf8.RuneCountInString(input.Name) > 80 || input.Filters == nil {
		writeProblem(w, http.StatusBadRequest, "Bad request", "segment fields are invalid")
		return segmentInput{}, nil, false
	}
	if err := query.ValidateFilters(input.Filters); err != nil {
		writeProblem(w, http.StatusBadRequest, "Bad request", err.Error())
		return segmentInput{}, nil, false
	}
	encoded, err := json.Marshal(input.Filters)
	if err != nil {
		serviceError(w, err)
		return segmentInput{}, nil, false
	}
	return input, encoded, true
}

func scanSegment(scan func(...any) error) (segmentResponse, error) {
	var segment segmentResponse
	var filters []byte
	if err := scan(&segment.ID, &segment.SiteID, &segment.Name, &filters, &segment.CreatedBy, &segment.CreatedAt, &segment.UpdatedAt); err != nil {
		return segmentResponse{}, err
	}
	if err := json.Unmarshal(filters, &segment.Filters); err != nil {
		return segmentResponse{}, err
	}
	return segment, nil
}

func segmentMutationError(w http.ResponseWriter, err error) {
	var pgErr *pgconn.PgError
	if errors.As(err, &pgErr) && pgErr.Code == "23505" && pgErr.ConstraintName == "segments_site_id_name_lower_key" {
		writeProblem(w, http.StatusConflict, "Conflict", "a segment with this name already exists")
		return
	}
	serviceError(w, err)
}
