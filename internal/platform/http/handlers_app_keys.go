package platformhttp

import (
	"net/http"
	"time"

	"supermetry/internal/platform/appkeys"
	"supermetry/internal/platform/audit"
	"supermetry/internal/platform/authz"
	coresites "supermetry/internal/sites"

	"github.com/jackc/pgx/v5"
)

func (h *handler) appSite(w http.ResponseWriter, r *http.Request) (authz.Principal, coresites.Site, bool) {
	principal, site, ok := h.sitePrincipal(w, r, authz.ActionSiteEdit, authz.RoleOwner, authz.RoleAdmin)
	if !ok {
		return principal, site, false
	}
	if site.Kind != coresites.KindApp || h.deps.Store == nil {
		writeProblem(w, http.StatusNotFound, "Not found", "not an app project")
		return principal, site, false
	}
	return principal, site, true
}

func (h *handler) rotateAppIngestKey(w http.ResponseWriter, r *http.Request) {
	principal, site, ok := h.appSite(w, r)
	if !ok {
		return
	}
	plain, prefix, hash, err := appkeys.New()
	if err != nil {
		serviceError(w, err)
		return
	}
	var createdBy *int64
	if principal.UserID != 0 {
		createdBy = &principal.UserID
	}
	now := h.deps.Now()
	var previous *time.Time
	err = h.deps.Store.Tx(r.Context(), func(tx pgx.Tx) error {
		var err error
		if previous, err = h.deps.Store.RotateAppKeyTx(r.Context(), tx, site.ID, createdBy, prefix, hash, now); err != nil {
			return err
		}
		return audit.Write(r.Context(), tx, principal, "site.ingest_key_rotated", "site", int64(site.ID), map[string]any{"prefix": prefix})
	})
	if err != nil {
		serviceError(w, err)
		return
	}
	if inv, ok := h.deps.Sites.(interface{ Invalidate() error }); ok {
		if err := inv.Invalidate(); err != nil {
			serviceError(w, err)
			return
		}
	}
	writeJSON(w, http.StatusCreated, map[string]any{"key": plain, "prefix": prefix, "previous_expires_at": previous})
}

func (h *handler) listAppIngestKeys(w http.ResponseWriter, r *http.Request) {
	_, site, ok := h.appSite(w, r)
	if !ok {
		return
	}
	rows, err := h.deps.Store.ListAppKeys(r.Context(), site.ID)
	if err != nil {
		serviceError(w, err)
		return
	}
	type item struct {
		Prefix    string     `json:"prefix"`
		CreatedAt time.Time  `json:"created_at"`
		ExpiresAt *time.Time `json:"expires_at"`
	}
	keys := make([]item, 0, len(rows))
	for _, row := range rows {
		keys = append(keys, item{Prefix: row.Prefix, CreatedAt: row.CreatedAt, ExpiresAt: row.ExpiresAt})
	}
	writeJSON(w, http.StatusOK, map[string]any{"keys": keys})
}
