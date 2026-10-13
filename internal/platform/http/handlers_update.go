package platformhttp

import (
	"crypto/rand"
	"encoding/base64"
	"errors"
	"net/http"
	"strconv"

	"supermetry/internal/platform/admin"
	platformauth "supermetry/internal/platform/auth"
	"supermetry/internal/updatecheck"
	"supermetry/internal/updateclient"
	"supermetry/internal/updatehelper"
)

type adminUpdateStatusResponse struct {
	updatecheck.Status
	UpdateSupported bool             `json:"update_supported"`
	DeploymentMode  string           `json:"deployment_mode,omitempty"`
	Job             *admin.UpdateJob `json:"job,omitempty"`
}

func (h *handler) adminUpdateStatus(w http.ResponseWriter, r *http.Request) {
	if _, ok := h.adminPrincipal(w, r); !ok {
		return
	}
	response := adminUpdateStatusResponse{
		Status:          h.deps.UpdateChecker.Check(r.Context()),
		UpdateSupported: h.deps.UpdateStarter != nil && (h.deps.UpdateMode == updatehelper.DeploymentBinary || h.deps.UpdateMode == updatehelper.DeploymentDocker),
		DeploymentMode:  string(h.deps.UpdateMode),
	}
	if h.deps.Admin != nil {
		job, err := h.deps.Admin.LatestUpdateJob(r.Context())
		if err != nil {
			serviceError(w, err)
			return
		}
		response.Job = job
	}
	writeJSON(w, http.StatusOK, response)
}

func (h *handler) adminApplyUpdate(w http.ResponseWriter, r *http.Request) {
	principal, ok := h.adminPrincipal(w, r)
	if !ok {
		return
	}
	if h.deps.UpdateStarter == nil || h.deps.UpdateMode != updatehelper.DeploymentBinary && h.deps.UpdateMode != updatehelper.DeploymentDocker {
		writeProblem(w, http.StatusConflict, "Update unavailable", "this deployment is not managed by the Supermetry updater")
		return
	}
	var request struct {
		TargetVersion  string `json:"target_version"`
		IdempotencyKey string `json:"idempotency_key"`
		Password       string `json:"password"`
		TOTPCode       string `json:"totp_code,omitempty"`
	}
	if !decodeJSON(w, r, &request) {
		return
	}
	if h.deps.Auth == nil {
		unavailable(w, "authentication")
		return
	}
	if err := h.deps.Auth.VerifySensitiveAction(h.clientContext(r), principal.UserID, request.Password, request.TOTPCode); err != nil {
		switch {
		case errors.Is(err, platformauth.ErrRateLimited):
			rateLimited(w)
		case errors.Is(err, platformauth.ErrTOTPRequired):
			writeProblem(w, http.StatusBadRequest, "Verification required", "a TOTP code is required")
		case errors.Is(err, platformauth.ErrInvalid):
			writeProblem(w, http.StatusUnauthorized, "Unauthorized", "password or verification code is invalid")
		default:
			serviceError(w, err)
		}
		return
	}
	job, created, err := h.deps.Admin.RequestUpdate(r.Context(), principal.UserID, admin.UpdateRequest{
		IdempotencyKey: request.IdempotencyKey,
		FromVersion:    h.deps.Version,
		TargetVersion:  request.TargetVersion,
		DeploymentMode: string(h.deps.UpdateMode),
	})
	if err != nil {
		adminError(w, err)
		return
	}
	if !created {
		writeJSON(w, http.StatusAccepted, job)
		return
	}
	nonce, err := h.deps.UpdateNonce()
	if err != nil {
		_ = h.deps.Admin.FailUndispatchedUpdate(r.Context(), principal.UserID, job.ID, "nonce_generation_failed")
		serviceError(w, err)
		return
	}
	_, err = h.deps.UpdateStarter.Start(r.Context(), updatehelper.Request{
		Version: updatehelper.ProtocolVersion, JobID: strconv.FormatInt(job.ID, 10),
		TargetVersion: job.TargetVersion, Nonce: nonce,
	})
	if err != nil {
		if errors.Is(err, updateclient.ErrUnavailable) {
			_ = h.deps.Admin.FailUndispatchedUpdate(r.Context(), principal.UserID, job.ID, "helper_unavailable")
		}
		writeProblem(w, http.StatusServiceUnavailable, "Updater unavailable", "the host updater helper could not accept the request")
		return
	}
	writeJSON(w, http.StatusAccepted, job)
}

func newUpdateNonce() (string, error) {
	nonce := make([]byte, 32)
	if _, err := rand.Read(nonce); err != nil {
		return "", err
	}
	return base64.RawURLEncoding.EncodeToString(nonce), nil
}
