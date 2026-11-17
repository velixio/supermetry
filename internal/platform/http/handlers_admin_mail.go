package platformhttp

import (
	"context"
	"errors"
	"log/slog"
	"net/http"
	"net/mail"
	"strings"
	"time"

	"github.com/jackc/pgx/v5"

	"supermetry/internal/platform/authz"
	platformmail "supermetry/internal/platform/mail"
	"supermetry/internal/platform/store"
)

// EmailSettings is the SMTP configuration the instance admin screen reads and
// writes. *mail.Manager implements it.
type EmailSettings interface {
	Status(context.Context) (platformmail.Status, error)
	Save(context.Context, platformmail.Settings) error
	ClearPassword(context.Context) error
	Clear(context.Context) error
	SendTest(ctx context.Context, to string) error
}

const (
	emailTestsPerMinute = 5
	emailTestTimeout    = 30 * time.Second
	emailDeliveryNote   = "The mail server accepted the message. That does not guarantee it reached an inbox: check the spam folder and the SPF, DKIM and DMARC records for the sender domain."
)

type adminEmailResponse struct {
	Source      string `json:"source"`
	Host        string `json:"host"`
	Port        int    `json:"port"`
	TLS         string `json:"tls"`
	User        string `json:"user"`
	From        string `json:"from"`
	Helo        string `json:"helo"`
	PasswordSet bool   `json:"password_set"`
}

func newAdminEmailResponse(status platformmail.Status) adminEmailResponse {
	s := status.Settings
	return adminEmailResponse{Source: status.Source, Host: s.Host, Port: s.Port, TLS: s.TLS, User: s.User, From: s.From, Helo: s.Hello, PasswordSet: status.PasswordSet}
}

// emailPrincipal authenticates an instance admin and refuses managed instances
// and builds without email settings.
func (h *handler) emailPrincipal(w http.ResponseWriter, r *http.Request) (authz.Principal, bool) {
	principal, ok := h.adminPrincipal(w, r)
	if !ok {
		return authz.Principal{}, false
	}
	if h.deps.Managed {
		writeProblem(w, http.StatusConflict, "Email settings unavailable", "email is managed by the operator of this instance")
		return authz.Principal{}, false
	}
	if h.deps.Mail == nil {
		writeProblem(w, http.StatusNotFound, "Not found", "resource not found")
		return authz.Principal{}, false
	}
	return principal, true
}

func (h *handler) writeEmailStatus(w http.ResponseWriter, r *http.Request) {
	status, err := h.deps.Mail.Status(r.Context())
	if err != nil {
		serviceError(w, err)
		return
	}
	writeJSON(w, http.StatusOK, newAdminEmailResponse(status))
}

func (h *handler) adminGetEmail(w http.ResponseWriter, r *http.Request) {
	if _, ok := h.emailPrincipal(w, r); !ok {
		return
	}
	h.writeEmailStatus(w, r)
}

func (h *handler) adminPutEmail(w http.ResponseWriter, r *http.Request) {
	principal, ok := h.emailPrincipal(w, r)
	if !ok {
		return
	}
	var request struct {
		Host          string `json:"host"`
		Port          int    `json:"port"`
		TLS           string `json:"tls"`
		User          string `json:"user"`
		Password      string `json:"password"`
		ClearPassword bool   `json:"clear_password"`
		From          string `json:"from"`
		Helo          string `json:"helo"`
	}
	if !decodeJSON(w, r, &request) {
		return
	}
	if request.ClearPassword && request.Password != "" {
		writeProblem(w, http.StatusBadRequest, "Bad request", "send either a password or clear_password, not both")
		return
	}
	if request.ClearPassword {
		if err := h.deps.Mail.ClearPassword(r.Context()); err != nil {
			h.emailError(w, err)
			return
		}
	}
	err := h.deps.Mail.Save(r.Context(), platformmail.Settings{
		Host: request.Host, Port: request.Port, TLS: request.TLS, User: request.User, Pass: request.Password, From: request.From, Hello: request.Helo,
	})
	if err != nil {
		h.emailError(w, err)
		return
	}
	h.auditEmail(r, principal, "instance.smtp.updated")
	h.writeEmailStatus(w, r)
}

func (h *handler) adminDeleteEmail(w http.ResponseWriter, r *http.Request) {
	principal, ok := h.emailPrincipal(w, r)
	if !ok {
		return
	}
	if err := h.deps.Mail.Clear(r.Context()); err != nil {
		h.emailError(w, err)
		return
	}
	h.auditEmail(r, principal, "instance.smtp.cleared")
	h.writeEmailStatus(w, r)
}

func (h *handler) adminTestEmail(w http.ResponseWriter, r *http.Request) {
	principal, ok := h.emailPrincipal(w, r)
	if !ok {
		return
	}
	var request struct {
		To string `json:"to"`
	}
	if r.ContentLength != 0 && !decodeJSON(w, r, &request) {
		return
	}
	if !h.allowEmailTest(principal.UserID) {
		w.Header().Set("Retry-After", "60")
		writeProblem(w, http.StatusTooManyRequests, "Rate limit exceeded", "retry after the indicated delay")
		return
	}
	to := strings.TrimSpace(request.To)
	if to == "" && h.deps.Store != nil && principal.UserID != 0 {
		if err := h.deps.Store.Pool.QueryRow(r.Context(), `select email from users where id=$1`, principal.UserID).Scan(&to); err != nil {
			serviceError(w, err)
			return
		}
	}
	if strings.ContainsAny(to, "\r\n") {
		writeProblem(w, http.StatusBadRequest, "Bad request", "recipient must be a single email address")
		return
	}
	if parsed, err := mail.ParseAddress(to); err != nil || parsed.Address != to {
		writeProblem(w, http.StatusBadRequest, "Bad request", "recipient must be a plain email address")
		return
	}
	ctx, cancel := context.WithTimeout(r.Context(), emailTestTimeout)
	defer cancel()
	result := map[string]any{"ok": true, "to": to, "note": emailDeliveryNote}
	if err := h.deps.Mail.SendTest(ctx, to); err != nil {
		slog.Warn("admin SMTP test failed", "step", platformmail.ErrorStep(err))
		result = map[string]any{"ok": false, "to": to, "error": err.Error(), "note": emailDeliveryNote}
	}
	writeJSON(w, http.StatusOK, result)
}

func (h *handler) emailError(w http.ResponseWriter, err error) {
	switch {
	case errors.Is(err, platformmail.ErrEnvironmentManaged):
		writeProblem(w, http.StatusConflict, "Email settings are read-only", "SMTP is configured by environment variables; unset SUPERMETRY_SMTP_HOST to edit it here")
	case errors.Is(err, platformmail.ErrInvalidSettings):
		writeProblem(w, http.StatusBadRequest, "Bad request", err.Error())
	default:
		serviceError(w, err)
	}
}

func (h *handler) auditEmail(r *http.Request, principal authz.Principal, action string) {
	if h.deps.Store == nil {
		return
	}
	// Best effort: the setting is saved; a failed audit row only gets logged.
	err := h.deps.Store.Tx(r.Context(), func(tx pgx.Tx) error {
		return store.WriteInstanceAudit(r.Context(), tx, store.InstanceAuditEntry{ActorUserID: principal.UserID, Action: action, TargetType: "instance", TargetID: 1})
	})
	if err != nil {
		slog.Warn("audit SMTP settings change", "error", err)
	}
}

func (h *handler) allowEmailTest(userID int64) bool {
	now := h.deps.Now()
	h.adminLimitMu.Lock()
	defer h.adminLimitMu.Unlock()
	if h.emailTests == nil {
		h.emailTests = map[int64]adminRateWindow{}
	}
	window := h.emailTests[userID]
	if window.start.IsZero() || now.Sub(window.start) >= time.Minute {
		window = adminRateWindow{start: now}
	}
	if window.count >= emailTestsPerMinute {
		h.emailTests[userID] = window
		return false
	}
	window.count++
	h.emailTests[userID] = window
	return true
}
