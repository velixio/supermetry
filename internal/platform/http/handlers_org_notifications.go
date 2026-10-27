package platformhttp

import (
	"context"
	"errors"
	"net/http"

	"supermetry/internal/notifications"
	"supermetry/internal/platform/authz"
)

// OrgNotificationsService is the transport-facing subset of the org
// notification channel service.
type OrgNotificationsService interface {
	List(context.Context, int64) ([]notifications.Channel, error)
	SaveTelegram(context.Context, authz.Principal, notifications.TelegramInput) (notifications.Channel, error)
	DeleteTelegram(context.Context, authz.Principal) error
	TestTelegram(context.Context, authz.Principal) error
}

type orgNotificationsResponse struct {
	Data       []notifications.Channel  `json:"data"`
	EventKinds []notifications.KindInfo `json:"event_kinds"`
}

func (h *handler) orgNotificationsService(w http.ResponseWriter, r *http.Request) (OrgNotificationsService, authz.Principal, bool) {
	principal, ok := h.orgManager(w, r, authz.ActionChannelsManage)
	if !ok {
		return nil, authz.Principal{}, false
	}
	if h.deps.OrgNotifications == nil {
		unavailable(w, "organization notification")
		return nil, authz.Principal{}, false
	}
	return h.deps.OrgNotifications, principal, true
}

func orgNotificationsError(w http.ResponseWriter, err error) {
	switch {
	case errors.Is(err, notifications.ErrInvalid):
		writeProblem(w, http.StatusBadRequest, "Bad request", err.Error())
	case errors.Is(err, notifications.ErrNotFound):
		writeProblem(w, http.StatusNotFound, "Not found", "notification channel not found")
	case errors.Is(err, notifications.ErrForbidden):
		writeProblem(w, http.StatusForbidden, "Forbidden", "the principal cannot perform this action")
	case errors.Is(err, notifications.ErrTooSoon):
		w.Header().Set("Retry-After", "30")
		writeProblem(w, http.StatusTooManyRequests, "Rate limit exceeded", err.Error())
	default:
		serviceError(w, err)
	}
}

func (h *handler) listOrgNotifications(w http.ResponseWriter, r *http.Request) {
	service, principal, ok := h.orgNotificationsService(w, r)
	if !ok {
		return
	}
	channels, err := service.List(r.Context(), principal.OrgID)
	if err != nil {
		orgNotificationsError(w, err)
		return
	}
	writeJSON(w, http.StatusOK, orgNotificationsResponse{Data: channels, EventKinds: notifications.Kinds()})
}

func (h *handler) saveOrgTelegram(w http.ResponseWriter, r *http.Request) {
	service, principal, ok := h.orgNotificationsService(w, r)
	if !ok {
		return
	}
	var request struct {
		Token   *string                     `json:"token"`
		ChatID  string                      `json:"chat_id"`
		Enabled bool                        `json:"enabled"`
		Events  map[notifications.Kind]bool `json:"events"`
	}
	if !decodeJSON(w, r, &request) {
		return
	}
	channel, err := service.SaveTelegram(r.Context(), principal, notifications.TelegramInput{
		Token: request.Token, ChatID: request.ChatID, Enabled: request.Enabled, Events: request.Events,
	})
	if err != nil {
		orgNotificationsError(w, err)
		return
	}
	writeJSON(w, http.StatusOK, channel)
}

func (h *handler) deleteOrgTelegram(w http.ResponseWriter, r *http.Request) {
	service, principal, ok := h.orgNotificationsService(w, r)
	if !ok {
		return
	}
	if err := service.DeleteTelegram(r.Context(), principal); err != nil {
		orgNotificationsError(w, err)
		return
	}
	w.WriteHeader(http.StatusNoContent)
}

func (h *handler) testOrgTelegram(w http.ResponseWriter, r *http.Request) {
	service, principal, ok := h.orgNotificationsService(w, r)
	if !ok {
		return
	}
	err := service.TestTelegram(r.Context(), principal)
	var delivery *notifications.DeliveryError
	switch {
	case err == nil:
		writeJSON(w, http.StatusOK, map[string]any{"ok": true})
	case errors.As(err, &delivery):
		writeJSON(w, http.StatusOK, map[string]any{"ok": false, "error": delivery.Message})
	default:
		orgNotificationsError(w, err)
	}
}
