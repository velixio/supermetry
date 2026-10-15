package platformhttp

import (
	"crypto/rand"
	"encoding/base64"
	"errors"
	"net/http"
	"strconv"

	"supermetry/internal/platform/admin"
	platformauth "supermetry/internal/platform/auth"
	"supermetry/internal/platform/authz"
	"supermetry/internal/updatecheck"
	"supermetry/internal/updateclient"
	"supermetry/internal/updatehelper"
)

type adminUpdateStatusResponse struct {
	updatecheck.Status
	UpdateSupported bool              `json:"update_supported"`
	DeploymentMode  string            `json:"deployment_mode,omitempty"`
	Job             *admin.UpdateJob  `json:"job,omitempty"`
	History         []admin.UpdateJob `json:"history"`
}

// updateHistoryLimit is how many recent update jobs the status response carries.
const updateHistoryLimit = 10

func (h *handler) adminUpdateStatus(w http.ResponseWriter, r *http.Request) {
	principal, ok := h.adminPrincipal(w, r)
	if !ok {
		return
	}
	h.writeUpdateStatus(w, r, principal, h.deps.UpdateChecker.Check(r.Context()))
}

// adminCheckUpdate forces a fresh release check; it never downloads or applies
// anything.
func (h *handler) adminCheckUpdate(w http.ResponseWriter, r *http.Request) {
	principal, ok := h.adminPrincipal(w, r)
	if !ok {
		return
	}
	h.writeUpdateStatus(w, r, principal, h.deps.UpdateChecker.Refresh(r.Context()))
}

func (h *handler) writeUpdateStatus(w http.ResponseWriter, r *http.Request, principal authz.Principal, checked updatecheck.Status) {
	response := adminUpdateStatusResponse{
		Status:          checked,
		UpdateSupported: h.deps.UpdateStarter != nil && (h.deps.UpdateMode == updatehelper.DeploymentBinary || h.deps.UpdateMode == updatehelper.DeploymentDocker),
		DeploymentMode:  string(h.deps.UpdateMode),
	}
	if h.deps.Admin != nil {
		job, err := h.deps.Admin.LatestUpdateJob(r.Context())
		if err != nil {
			serviceError(w, err)
			return
		}
		if job != nil && h.deps.UpdateStarter != nil && !terminalUpdateStatus(job.Status) {
			helperStatus, statusErr := h.deps.UpdateStarter.Status(r.Context(), strconv.FormatInt(job.ID, 10))
			if statusErr == nil {
				if err := h.deps.Admin.SyncUpdateJob(r.Context(), principal.UserID, job.ID, helperStatus.State, helperStatus.ErrorCode); err != nil {
					serviceError(w, err)
					return
				}
				job, err = h.deps.Admin.LatestUpdateJob(r.Context())
				if err != nil {
					serviceError(w, err)
					return
				}
			}
		}
		response.Job = job
		history, err := h.deps.Admin.RecentUpdateJobs(r.Context(), updateHistoryLimit)
		if err != nil {
			serviceError(w, err)
			return
		}
		response.History = history
	}
	if response.History == nil {
		response.History = []admin.UpdateJob{}
	}
	writeJSON(w, http.StatusOK, response)
}

func terminalUpdateStatus(status string) bool {
	return status == string(updatehelper.StateComplete) || status == string(updatehelper.StateFailed) || status == string(updatehelper.StateRecoveryRequired)
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
		helperStatus, statusErr := h.deps.UpdateStarter.Status(r.Context(), strconv.FormatInt(job.ID, 10))
		if statusErr != nil {
			writeProblem(w, http.StatusServiceUnavailable, "Updater unavailable", "the host updater helper could not confirm the request")
			return
		}
		if helperStatus.ErrorCode != "job_not_found" {
			if err := h.deps.Admin.SyncUpdateJob(r.Context(), principal.UserID, job.ID, helperStatus.State, helperStatus.ErrorCode); err != nil {
				serviceError(w, err)
				return
			}
			job.Status = string(helperStatus.State)
			job.ErrorCode = helperStatus.ErrorCode
			writeJSON(w, http.StatusAccepted, job)
			return
		}
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
