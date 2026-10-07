package platformhttp

import (
	"errors"
	"net/http"
	"time"

	"github.com/jackc/pgx/v5"

	"panel/internal/platform/audit"
	"panel/internal/platform/authn"
	"panel/internal/platform/authz"
	"panel/internal/platform/reports"
)

func (h *handler) listInvitations(w http.ResponseWriter, r *http.Request) {
	principal, ok := h.orgManager(w, r, authz.ActionMembersManage)
	if !ok {
		return
	}
	if h.deps.Store == nil {
		unavailable(w, "invitation")
		return
	}
	rows, err := h.deps.Store.Pool.Query(r.Context(), `select id,email,role,site_ids,invited_by,expires_at,accepted_at,revoked_at from invitations where org_id=$1 order by id`, principal.OrgID)
	if err != nil {
		serviceError(w, err)
		return
	}
	defer rows.Close()
	type invitation struct {
		ID         int64      `json:"id"`
		Email      string     `json:"email"`
		Role       authz.Role `json:"role"`
		SiteIDs    []int32    `json:"site_ids,omitempty"`
		InvitedBy  int64      `json:"invited_by"`
		ExpiresAt  time.Time  `json:"expires_at"`
		AcceptedAt *time.Time `json:"accepted_at,omitempty"`
		RevokedAt  *time.Time `json:"revoked_at,omitempty"`
	}
	items := make([]invitation, 0)
	for rows.Next() {
		var item invitation
		if err := rows.Scan(&item.ID, &item.Email, &item.Role, &item.SiteIDs, &item.InvitedBy, &item.ExpiresAt, &item.AcceptedAt, &item.RevokedAt); err != nil {
			serviceError(w, err)
			return
		}
		items = append(items, item)
	}
	if err := rows.Err(); err != nil {
		serviceError(w, err)
		return
	}
	writeJSON(w, http.StatusOK, map[string]any{"data": items})
}

func (h *handler) acceptInvitation(w http.ResponseWriter, r *http.Request) {
	if h.deps.Invites == nil {
		unavailable(w, "invitation")
		return
	}
	var request struct {
		Name     string `json:"name,omitempty"`
		Password string `json:"password,omitempty"`
	}
	if !decodeJSON(w, r, &request) {
		return
	}
	userID := int64(0)
	if h.deps.Authenticator != nil {
		if principal, ok := h.deps.Authenticator.Authenticate(r); ok && principal.UserID != 0 {
			userID = principal.UserID
		}
	}
	userID, err := h.deps.Invites.Accept(r.Context(), r.PathValue("token"), userID, request.Name, request.Password)
	if err != nil {
		writeProblem(w, http.StatusBadRequest, "Bad request", "the invitation is invalid or expired")
		return
	}
	writeJSON(w, http.StatusOK, map[string]int64{"user_id": userID})
}

func (h *handler) openShare(w http.ResponseWriter, r *http.Request) {
	if h.deps.Shares == nil {
		unavailable(w, "share")
		return
	}
	var request struct {
		Password string `json:"password,omitempty"`
	}
	if !decodeJSON(w, r, &request) {
		return
	}
	cookieValue, err := h.deps.Shares.Open(r.Context(), r.PathValue("slug"), request.Password)
	if err != nil {
		writeProblem(w, http.StatusUnauthorized, "Unauthorized", "invalid shared dashboard or password")
		return
	}
	if cookieValue != "" {
		setCookie(w, authn.ShareCookieName, cookieValue, 12*time.Hour)
	}
	w.WriteHeader(http.StatusNoContent)
}

func (h *handler) createInvitation(w http.ResponseWriter, r *http.Request) {
	principal, ok := h.orgManager(w, r, authz.ActionMembersManage)
	if !ok {
		return
	}
	var request struct {
		Email   string     `json:"email"`
		Role    authz.Role `json:"role"`
		SiteIDs []int32    `json:"site_ids,omitempty"`
	}
	if !decodeJSON(w, r, &request) {
		return
	}
	if h.deps.Invites == nil {
		unavailable(w, "invitation")
		return
	}
	link, err := h.deps.Invites.Create(r.Context(), principal, request.Email, request.Role, request.SiteIDs)
	if err != nil {
		serviceError(w, err)
		return
	}
	writeJSON(w, http.StatusCreated, map[string]string{"link": link})
}

func (h *handler) revokeInvitation(w http.ResponseWriter, r *http.Request) {
	principal, ok := h.orgManager(w, r, authz.ActionMembersManage)
	if !ok {
		return
	}
	id, err := parseInt64(r.PathValue("invitation"))
	if err != nil {
		writeProblem(w, http.StatusNotFound, "Not found", "invitation not found")
		return
	}
	if h.deps.Invites == nil {
		unavailable(w, "invitation")
		return
	}
	if err := h.deps.Invites.Revoke(r.Context(), principal, id); err != nil {
		serviceError(w, err)
		return
	}
	w.WriteHeader(http.StatusNoContent)
}

func (h *handler) listAPIKeys(w http.ResponseWriter, r *http.Request) {
	principal, ok := h.orgManager(w, r, authz.ActionKeysManage)
	if !ok {
		return
	}
	if h.deps.Keys == nil {
		unavailable(w, "API key")
		return
	}
	items, err := h.deps.Keys.List(r.Context(), principal)
	if err != nil {
		serviceError(w, err)
		return
	}
	writeJSON(w, http.StatusOK, map[string]any{"data": items})
}

func (h *handler) revokeAPIKey(w http.ResponseWriter, r *http.Request) {
	principal, ok := h.orgManager(w, r, authz.ActionKeysManage)
	if !ok {
		return
	}
	id, err := parseInt64(r.PathValue("key"))
	if err != nil {
		writeProblem(w, http.StatusNotFound, "Not found", "API key not found")
		return
	}
	if h.deps.Keys == nil {
		unavailable(w, "API key")
		return
	}
	if err := h.deps.Keys.Revoke(r.Context(), principal, id); err != nil {
		serviceError(w, err)
		return
	}
	w.WriteHeader(http.StatusNoContent)
}

func (h *handler) createShare(w http.ResponseWriter, r *http.Request) {
	principal, site, ok := h.sitePrincipal(w, r, authz.ActionSharesManage, authz.RoleOwner, authz.RoleAdmin, authz.RoleEditor)
	if !ok {
		return
	}
	var request struct {
		Password string `json:"password,omitempty"`
	}
	if !decodeJSON(w, r, &request) {
		return
	}
	if h.deps.Shares == nil {
		unavailable(w, "share")
		return
	}
	slug, info, err := h.deps.Shares.Create(r.Context(), principal, site.ID, request.Password)
	if err != nil {
		serviceError(w, err)
		return
	}
	writeJSON(w, http.StatusCreated, map[string]any{"slug": slug, "share": info})
}

func (h *handler) listShares(w http.ResponseWriter, r *http.Request) {
	_, site, ok := h.sitePrincipal(w, r, authz.ActionSharesManage, authz.RoleOwner, authz.RoleAdmin, authz.RoleEditor)
	if !ok {
		return
	}
	if h.deps.Store == nil {
		unavailable(w, "share")
		return
	}
	rows, err := h.deps.Store.Pool.Query(r.Context(), `select id,site_id,created_by,created_at,revoked_at,password_hash is not null from shared_links where site_id=$1 order by id`, site.ID)
	if err != nil {
		serviceError(w, err)
		return
	}
	defer rows.Close()
	type share struct {
		ID                int64      `json:"id"`
		SiteID            int32      `json:"site_id"`
		CreatedBy         int64      `json:"created_by"`
		CreatedAt         time.Time  `json:"created_at"`
		RevokedAt         *time.Time `json:"revoked_at,omitempty"`
		PasswordProtected bool       `json:"password_protected"`
	}
	items := make([]share, 0)
	for rows.Next() {
		var item share
		if err := rows.Scan(&item.ID, &item.SiteID, &item.CreatedBy, &item.CreatedAt, &item.RevokedAt, &item.PasswordProtected); err != nil {
			serviceError(w, err)
			return
		}
		items = append(items, item)
	}
	if err := rows.Err(); err != nil {
		serviceError(w, err)
		return
	}
	writeJSON(w, http.StatusOK, map[string]any{"data": items})
}

func (h *handler) revokeShare(w http.ResponseWriter, r *http.Request) {
	principal, site, ok := h.sitePrincipal(w, r, authz.ActionSharesManage, authz.RoleOwner, authz.RoleAdmin, authz.RoleEditor)
	if !ok {
		return
	}
	id, err := parseInt64(r.PathValue("share"))
	if err != nil {
		writeProblem(w, http.StatusNotFound, "Not found", "share not found")
		return
	}
	if h.deps.Shares == nil {
		unavailable(w, "share")
		return
	}
	if h.deps.Store == nil {
		unavailable(w, "share")
		return
	}
	var actualSiteID int32
	if err := h.deps.Store.Pool.QueryRow(r.Context(), `select sl.site_id from shared_links sl join sites s on s.id=sl.site_id where sl.id=$1 and sl.site_id=$2 and s.org_id=$3`, id, site.ID, principal.OrgID).Scan(&actualSiteID); errors.Is(err, pgx.ErrNoRows) {
		writeProblem(w, http.StatusNotFound, "Not found", "share not found")
		return
	} else if err != nil {
		serviceError(w, err)
		return
	}
	if err := h.deps.Shares.Revoke(r.Context(), principal, id); err != nil {
		serviceError(w, err)
		return
	}
	w.WriteHeader(http.StatusNoContent)
}

func (h *handler) subscribeReport(w http.ResponseWriter, r *http.Request) {
	principal, site, ok := h.sitePrincipal(w, r, authz.ActionReportsManage, authz.RoleOwner, authz.RoleAdmin, authz.RoleEditor, authz.RoleViewer)
	if !ok {
		return
	}
	var request struct {
		Cadence string `json:"cadence"`
	}
	if !decodeJSON(w, r, &request) {
		return
	}
	if h.deps.Reports == nil {
		unavailable(w, "report")
		return
	}
	if err := h.deps.Reports.Subscribe(r.Context(), principal, site.ID, request.Cadence); err != nil {
		if err == reports.ErrInvalidCadence {
			writeProblem(w, http.StatusBadRequest, "Bad request", err.Error())
			return
		}
		serviceError(w, err)
		return
	}
	w.WriteHeader(http.StatusCreated)
}

func (h *handler) listReports(w http.ResponseWriter, r *http.Request) {
	principal, site, ok := h.sitePrincipal(w, r, authz.ActionReportsManage, authz.RoleOwner, authz.RoleAdmin, authz.RoleEditor, authz.RoleViewer)
	if !ok {
		return
	}
	if h.deps.Store == nil {
		unavailable(w, "report")
		return
	}
	query := `select id,site_id,user_id,cadence,last_sent_at from report_subscriptions where site_id=$1 order by id`
	args := []any{site.ID}
	if principal.Role == authz.RoleViewer {
		query = `select id,site_id,user_id,cadence,last_sent_at from report_subscriptions where site_id=$1 and user_id=$2 order by id`
		args = append(args, principal.UserID)
	}
	rows, err := h.deps.Store.Pool.Query(r.Context(), query, args...)
	if err != nil {
		serviceError(w, err)
		return
	}
	defer rows.Close()
	type report struct {
		ID         int64      `json:"id"`
		SiteID     int32      `json:"site_id"`
		UserID     int64      `json:"user_id"`
		Cadence    string     `json:"cadence"`
		LastSentAt *time.Time `json:"last_sent_at,omitempty"`
	}
	items := make([]report, 0)
	for rows.Next() {
		var item report
		if err := rows.Scan(&item.ID, &item.SiteID, &item.UserID, &item.Cadence, &item.LastSentAt); err != nil {
			serviceError(w, err)
			return
		}
		items = append(items, item)
	}
	if err := rows.Err(); err != nil {
		serviceError(w, err)
		return
	}
	writeJSON(w, http.StatusOK, map[string]any{"data": items})
}

func (h *handler) deleteReport(w http.ResponseWriter, r *http.Request) {
	principal, site, ok := h.sitePrincipal(w, r, authz.ActionReportsManage, authz.RoleOwner, authz.RoleAdmin, authz.RoleEditor, authz.RoleViewer)
	if !ok {
		return
	}
	id, err := parseInt64(r.PathValue("report"))
	if err != nil || h.deps.Store == nil {
		writeProblem(w, http.StatusNotFound, "Not found", "report not found")
		return
	}
	err = h.deps.Store.Tx(r.Context(), func(tx pgx.Tx) error {
		query := `delete from report_subscriptions where id=$1 and site_id=$2`
		args := []any{id, site.ID}
		if principal.Role == authz.RoleViewer {
			query += ` and user_id=$3`
			args = append(args, principal.UserID)
		}
		tag, err := tx.Exec(r.Context(), query, args...)
		if err != nil {
			return err
		}
		if tag.RowsAffected() != 1 {
			return pgx.ErrNoRows
		}
		return audit.Write(r.Context(), tx, principal, "report.unsubscribed", "report_subscription", id, map[string]any{"site_id": site.ID})
	})
	if errors.Is(err, pgx.ErrNoRows) {
		writeProblem(w, http.StatusNotFound, "Not found", "report not found")
		return
	}
	if err != nil {
		serviceError(w, err)
		return
	}
	w.WriteHeader(http.StatusNoContent)
}

func (h *handler) orgManager(w http.ResponseWriter, r *http.Request, action authz.Action) (authz.Principal, bool) {
	principal, ok := h.privatePrincipal(w, r)
	if !ok {
		return authz.Principal{}, false
	}
	if _, ok := orgIDFromPath(w, r, principal); !ok {
		return authz.Principal{}, false
	}
	if !authz.Can(principal, action, 0) {
		writeProblem(w, http.StatusForbidden, "Forbidden", "the principal cannot perform this action")
		return authz.Principal{}, false
	}
	return principal, true
}
