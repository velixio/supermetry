package platformhttp

import (
	"context"
	"errors"
	"net/http"
	"time"

	"github.com/jackc/pgx/v5"

	"supermetry/internal/platform/authz"
	"supermetry/internal/platform/lifecycle"
	"supermetry/internal/platform/orgs"
	"supermetry/internal/platform/sitesvc"
	"supermetry/internal/platform/store"
)

type deletionResponse struct {
	PurgeAt   *time.Time `json:"purge_at"`
	Scheduled bool       `json:"scheduled"`
}

type deletedSiteResponse struct {
	ID        int32     `json:"id"`
	Name      string    `json:"name"`
	DeletedAt time.Time `json:"deleted_at"`
	PurgeAt   time.Time `json:"purge_at"`
}

type deletedOrgResponse struct {
	ID        int64     `json:"id"`
	Name      string    `json:"name"`
	Slug      string    `json:"slug"`
	DeletedAt time.Time `json:"deleted_at"`
	PurgeAt   time.Time `json:"purge_at"`
	Sites     []string  `json:"sites"`
}

type siteRestorer interface {
	RestoreSite(context.Context, authz.Principal, int32) error
}

func (s Services) RestoreSite(ctx context.Context, principal authz.Principal, id int32) error {
	if s.Sites == nil {
		return errors.New("site service is unavailable")
	}
	return s.Sites.Restore(ctx, principal, id)
}

func (h *handler) listDeletedSites(w http.ResponseWriter, r *http.Request) {
	principal, ok := h.privatePrincipal(w, r)
	if !ok {
		return
	}
	if _, ok := orgIDFromPath(w, r, principal); !ok {
		return
	}
	if principal.Role != authz.RoleOwner && principal.Role != authz.RoleAdmin {
		writeProblem(w, http.StatusForbidden, "Forbidden", "the principal cannot list deleted sites")
		return
	}
	if h.deps.Store == nil {
		unavailable(w, "site")
		return
	}
	deleted, err := store.DeletedSites(r.Context(), h.deps.Store.Pool, principal.OrgID, h.deps.Now())
	if err != nil {
		serviceError(w, err)
		return
	}
	response := make([]deletedSiteResponse, 0, len(deleted))
	for _, site := range deleted {
		response = append(response, deletedSiteResponse{
			ID: site.ID, Name: site.Name, DeletedAt: site.DeletedAt.UTC(), PurgeAt: site.PurgeAt.UTC(),
		})
	}
	writeJSON(w, http.StatusOK, response)
}

func (h *handler) restoreSite(w http.ResponseWriter, r *http.Request) {
	principal, ok := h.privatePrincipal(w, r)
	if !ok {
		return
	}
	if principal.Role != authz.RoleOwner && principal.Role != authz.RoleAdmin {
		writeProblem(w, http.StatusForbidden, "Forbidden", "the principal cannot restore sites")
		return
	}
	siteID, err := parseInt32(r.PathValue("site"))
	if err != nil {
		writeProblem(w, http.StatusBadRequest, "Bad request", "site must be a positive integer")
		return
	}
	if h.deps.Store == nil {
		unavailable(w, "site")
		return
	}
	var exists bool
	if err := h.deps.Store.Pool.QueryRow(r.Context(), `select exists(select 1 from sites where id=$1 and org_id=$2)`, siteID, principal.OrgID).Scan(&exists); err != nil {
		serviceError(w, err)
		return
	}
	if !exists {
		writeProblem(w, http.StatusNotFound, "Not found", "site not found")
		return
	}
	service, ok := h.deps.Mutations.(siteRestorer)
	if !ok {
		unavailable(w, "site")
		return
	}
	if err := service.RestoreSite(r.Context(), principal, siteID); err != nil {
		if errors.Is(err, sitesvc.ErrSiteLimit) {
			writeJSON(w, http.StatusConflict, map[string]string{"error": "site_limit"})
			return
		}
		if errors.Is(err, store.ErrNotRestorable) {
			writeJSON(w, http.StatusConflict, map[string]string{"error": "not_restorable"})
			return
		}
		serviceError(w, err)
		return
	}
	w.WriteHeader(http.StatusNoContent)
}

func (h *handler) renameOrg(w http.ResponseWriter, r *http.Request) {
	principal, ok := h.privatePrincipal(w, r)
	if !ok {
		return
	}
	orgID, ok := orgIDFromPath(w, r, principal)
	if !ok {
		return
	}
	if !authz.Can(principal, authz.ActionOrgUpdate, 0) {
		writeProblem(w, http.StatusForbidden, "Forbidden", "the principal cannot rename the organization")
		return
	}
	var request struct {
		Name string `json:"name"`
	}
	if !decodeJSON(w, r, &request) {
		return
	}
	if h.deps.Orgs == nil || h.deps.Store == nil {
		unavailable(w, "organization")
		return
	}
	if err := h.deps.Orgs.Rename(r.Context(), principal, request.Name); err != nil {
		if errors.Is(err, orgs.ErrInvalidName) {
			writeJSON(w, http.StatusBadRequest, map[string]string{"error": "invalid_name"})
			return
		}
		serviceError(w, err)
		return
	}
	var response struct {
		ID   int64  `json:"id"`
		Name string `json:"name"`
		Slug string `json:"slug"`
	}
	if err := h.deps.Store.Pool.QueryRow(r.Context(), `select id,name,slug from orgs where id=$1 and deleted_at is null`, orgID).Scan(&response.ID, &response.Name, &response.Slug); err != nil {
		serviceError(w, err)
		return
	}
	writeJSON(w, http.StatusOK, response)
}

func (h *handler) listDeletedOrgs(w http.ResponseWriter, r *http.Request) {
	principal, ok := h.privatePrincipal(w, r)
	if !ok {
		return
	}
	if h.deps.Store == nil {
		unavailable(w, "organization")
		return
	}
	deleted, err := store.DeletedOrgsForOwner(r.Context(), h.deps.Store.Pool, principal.UserID, h.deps.Now())
	if err != nil {
		serviceError(w, err)
		return
	}
	response := make([]deletedOrgResponse, 0, len(deleted))
	for _, org := range deleted {
		response = append(response, deletedOrgResponse{
			ID: org.ID, Name: org.Name, Slug: org.Slug, DeletedAt: org.DeletedAt.UTC(),
			PurgeAt: org.PurgeAt.UTC(), Sites: append([]string{}, org.Sites...),
		})
	}
	writeJSON(w, http.StatusOK, response)
}

func (h *handler) restoreOrg(w http.ResponseWriter, r *http.Request) {
	principal, ok := h.privatePrincipal(w, r)
	if !ok {
		return
	}
	orgID, err := parseInt64(r.PathValue("org"))
	if err != nil {
		writeProblem(w, http.StatusBadRequest, "Bad request", "organization must be a positive integer")
		return
	}
	if h.deps.Orgs == nil {
		unavailable(w, "organization")
		return
	}
	if err := h.deps.Orgs.Restore(r.Context(), principal.UserID, orgID, h.deps.Now()); err != nil {
		switch {
		case errors.Is(err, orgs.ErrForbidden):
			writeProblem(w, http.StatusNotFound, "Not found", "organization not found")
		case errors.Is(err, orgs.ErrNotRestorable):
			writeJSON(w, http.StatusConflict, map[string]string{"error": "not_restorable"})
		case errors.Is(err, orgs.ErrSiteLimit):
			writeJSON(w, http.StatusConflict, map[string]string{"error": "site_limit"})
		default:
			serviceError(w, err)
		}
		return
	}
	w.WriteHeader(http.StatusNoContent)
}

func (h *handler) deletedSiteResult(ctx context.Context, orgID int64, siteID int32) (deletionResponse, error) {
	var deletedAt time.Time
	var demo bool
	if err := h.deps.Store.Pool.QueryRow(ctx, `select deleted_at,is_demo from sites where id=$1 and org_id=$2`, siteID, orgID).Scan(&deletedAt, &demo); err != nil {
		return deletionResponse{}, err
	}
	return makeDeletionResponse(deletedAt, !demo), nil
}

func (h *handler) deletedOrgResult(ctx context.Context, orgID int64) (deletionResponse, error) {
	var deletedAt *time.Time
	if err := h.deps.Store.Pool.QueryRow(ctx, `select deleted_at from orgs where id=$1`, orgID).Scan(&deletedAt); err != nil {
		return deletionResponse{}, err
	}
	if deletedAt == nil {
		return deletionResponse{}, pgx.ErrNoRows
	}
	var scheduled bool
	if err := h.deps.Store.Pool.QueryRow(ctx, `select exists(select 1 from sites where org_id=$1 and deleted_with_org)`, orgID).Scan(&scheduled); err != nil {
		return deletionResponse{}, err
	}
	return makeDeletionResponse(*deletedAt, scheduled), nil
}

func makeDeletionResponse(deletedAt time.Time, scheduled bool) deletionResponse {
	response := deletionResponse{Scheduled: scheduled}
	if scheduled {
		purgeAt := lifecycle.PurgeAt(deletedAt).UTC()
		response.PurgeAt = &purgeAt
	}
	return response
}
