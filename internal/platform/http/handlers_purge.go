package platformhttp

import (
	"errors"
	"net/http"
	"strconv"
	"time"

	"supermetry/internal/datapurge"
	platformauth "supermetry/internal/platform/auth"
	"supermetry/internal/platform/authz"
)

type createPurgeRequest struct {
	From     string `json:"from"`
	To       string `json:"to"`
	Source   string `json:"source"`
	Country  string `json:"country"`
	Password string `json:"password"`
	TOTPCode string `json:"totp_code,omitempty"`
}

// createDataPurge starts a durable deletion of a site's stored traffic by
// site-local date range and a source and/or country. It needs the caller's
// password (and TOTP code when enabled) because the deletion cannot be undone.
func (h *handler) createDataPurge(w http.ResponseWriter, r *http.Request) {
	principal, site, ok := h.sitePrincipal(w, r, authz.ActionSiteDelete, authz.RoleOwner, authz.RoleAdmin)
	if !ok {
		return
	}
	if principal.UserID == 0 {
		writeProblem(w, http.StatusForbidden, "Forbidden", "a user session is required")
		return
	}
	var request createPurgeRequest
	if !decodeJSON(w, r, &request) {
		return
	}
	from, fromErr := time.Parse(time.DateOnly, request.From)
	to, toErr := time.Parse(time.DateOnly, request.To)
	if fromErr != nil || toErr != nil {
		writeProblem(w, http.StatusBadRequest, "Bad request", "from and to must be dates like 2026-10-01")
		return
	}
	if h.deps.Auth == nil || h.deps.Store == nil {
		unavailable(w, "data purge")
		return
	}
	// Validate before asking for the password so a typo does not use up a
	// verification attempt.
	input := datapurge.Input{SiteID: site.ID, From: from, To: to, Source: request.Source, Country: request.Country, RequestedBy: "user:" + strconv.FormatInt(principal.UserID, 10)}
	if _, err := input.Normalized(); err != nil {
		writeProblem(w, http.StatusBadRequest, "Bad request", err.Error())
		return
	}
	if active, err := datapurge.Active(r.Context(), h.deps.Store.Pool, site.ID); err != nil {
		serviceError(w, err)
		return
	} else if active {
		writeProblem(w, http.StatusConflict, "Conflict", datapurge.ErrBusy.Error())
		return
	}
	if err := h.verifySensitive(w, r, principal.UserID, request.Password, request.TOTPCode); err != nil {
		return
	}
	created, err := datapurge.Create(r.Context(), h.deps.Store.Pool, input)
	switch {
	case errors.Is(err, datapurge.ErrInvalid):
		writeProblem(w, http.StatusBadRequest, "Bad request", err.Error())
	case errors.Is(err, datapurge.ErrBusy):
		writeProblem(w, http.StatusConflict, "Conflict", err.Error())
	case err != nil:
		serviceError(w, err)
	default:
		writeJSON(w, http.StatusAccepted, created)
	}
}

func (h *handler) listDataPurges(w http.ResponseWriter, r *http.Request) {
	_, site, ok := h.sitePrincipal(w, r, authz.ActionSiteDelete, authz.RoleOwner, authz.RoleAdmin)
	if !ok {
		return
	}
	if h.deps.Store == nil {
		writeJSON(w, http.StatusOK, map[string]any{"data": []datapurge.Request{}})
		return
	}
	items, err := datapurge.List(r.Context(), h.deps.Store.Pool, site.ID, 20)
	if err != nil {
		serviceError(w, err)
		return
	}
	writeJSON(w, http.StatusOK, map[string]any{"data": items})
}

// verifySensitive re-checks the caller's password and TOTP code. It writes the
// problem response itself and returns a non-nil error when the caller must stop.
func (h *handler) verifySensitive(w http.ResponseWriter, r *http.Request, userID int64, password, totpCode string) error {
	err := h.deps.Auth.VerifySensitiveAction(h.clientContext(r), userID, password, totpCode)
	switch {
	case err == nil:
		return nil
	case errors.Is(err, platformauth.ErrRateLimited):
		rateLimited(w)
	case errors.Is(err, platformauth.ErrTOTPRequired):
		writeProblem(w, http.StatusBadRequest, "Verification required", "a TOTP code is required")
	case errors.Is(err, platformauth.ErrInvalid):
		writeProblem(w, http.StatusUnauthorized, "Unauthorized", "password or verification code is invalid")
	default:
		serviceError(w, err)
	}
	return err
}
