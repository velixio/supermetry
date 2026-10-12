package platformhttp

import (
	"context"
	"errors"
	"net/http"
	"net/mail"
	"net/url"
	"strconv"
	"strings"

	"github.com/jackc/pgx/v5"

	"supermetry/internal/alerts"
	"supermetry/internal/platform/authz"
)

// AlertsService is the transport-facing subset of the alerts service.
type AlertsService interface {
	ListChannels(context.Context, authz.Principal) ([]alerts.Channel, error)
	CreateChannel(context.Context, authz.Principal, alerts.ChannelInput) (alerts.Channel, error)
	UpdateChannel(context.Context, authz.Principal, int64, alerts.ChannelInput) (alerts.Channel, error)
	DeleteChannel(context.Context, authz.Principal, int64) error
	TestChannel(context.Context, authz.Principal, int64) error
	ListAlerts(context.Context, authz.Principal, int32) ([]alerts.Alert, error)
	CreateAlert(context.Context, authz.Principal, int32, alerts.AlertInput) (alerts.Alert, error)
	UpdateAlert(context.Context, authz.Principal, int32, int64, alerts.AlertInput) (alerts.Alert, error)
	DeleteAlert(context.Context, authz.Principal, int32, int64) error
	ListHistory(context.Context, authz.Principal, int32, alerts.HistoryFilter) ([]alerts.DeliveryRecord, error)
	RetryDelivery(context.Context, authz.Principal, int32, int64) (alerts.DeliveryRecord, error)
	ListTemplates(context.Context, authz.Principal) ([]alerts.MessageTemplate, alerts.NotificationSettings, error)
	SaveTemplate(context.Context, authz.Principal, alerts.ChannelKind, alerts.TemplateInput) (alerts.MessageTemplate, error)
	ResetTemplate(context.Context, authz.Principal, alerts.ChannelKind) (alerts.MessageTemplate, error)
	PreviewTemplate(context.Context, authz.Principal, alerts.ChannelKind, alerts.TemplateInput) (alerts.RenderedMessage, error)
	SaveSettings(context.Context, authz.Principal, int) (alerts.NotificationSettings, error)
}

type channelRequest struct {
	Kind    alerts.ChannelKind `json:"kind"`
	Name    string             `json:"name"`
	Enabled *bool              `json:"enabled,omitempty"`
	Config  struct {
		Recipients []string `json:"recipients,omitempty"`
		URL        string   `json:"url,omitempty"`
		Secret     string   `json:"secret,omitempty"`
		BotToken   string   `json:"bot_token,omitempty"`
		ChatID     string   `json:"chat_id,omitempty"`
	} `json:"config"`
}

type alertRequest struct {
	Kind       alerts.AlertKind `json:"kind"`
	Threshold  float64          `json:"threshold"`
	GoalID     *int64           `json:"goal_id,omitempty"`
	ChannelIDs []int64          `json:"channel_ids"`
	Enabled    *bool            `json:"enabled,omitempty"`
}

func (h *handler) listChannels(w http.ResponseWriter, r *http.Request) {
	principal, ok := h.orgManager(w, r, authz.ActionChannelsRead)
	if !ok {
		return
	}
	if h.deps.Alerts == nil {
		unavailable(w, "notification channel")
		return
	}
	channels, err := h.deps.Alerts.ListChannels(r.Context(), principal)
	if err != nil {
		alertServiceError(w, err, "notification channel")
		return
	}
	writeJSON(w, http.StatusOK, map[string]any{"data": channels})
}

func (h *handler) createChannel(w http.ResponseWriter, r *http.Request) {
	principal, ok := h.orgManager(w, r, authz.ActionChannelsManage)
	if !ok {
		return
	}
	input, ok := decodeChannelInput(w, r)
	if !ok {
		return
	}
	if h.deps.Alerts == nil {
		unavailable(w, "notification channel")
		return
	}
	created, err := h.deps.Alerts.CreateChannel(r.Context(), principal, input)
	if err != nil {
		alertServiceError(w, err, "notification channel")
		return
	}
	writeJSON(w, http.StatusCreated, created)
}

func (h *handler) updateChannel(w http.ResponseWriter, r *http.Request) {
	principal, ok := h.orgManager(w, r, authz.ActionChannelsManage)
	if !ok {
		return
	}
	id, err := parseInt64(r.PathValue("channel"))
	if err != nil {
		writeProblem(w, http.StatusNotFound, "Not found", "notification channel not found")
		return
	}
	input, ok := decodeChannelInput(w, r)
	if !ok {
		return
	}
	if h.deps.Alerts == nil {
		unavailable(w, "notification channel")
		return
	}
	updated, err := h.deps.Alerts.UpdateChannel(r.Context(), principal, id, input)
	if err != nil {
		alertServiceError(w, err, "notification channel")
		return
	}
	writeJSON(w, http.StatusOK, updated)
}

func (h *handler) deleteChannel(w http.ResponseWriter, r *http.Request) {
	principal, id, ok := h.channelMutation(w, r)
	if !ok {
		return
	}
	if err := h.deps.Alerts.DeleteChannel(r.Context(), principal, id); err != nil {
		alertServiceError(w, err, "notification channel")
		return
	}
	w.WriteHeader(http.StatusNoContent)
}

func (h *handler) testChannel(w http.ResponseWriter, r *http.Request) {
	principal, id, ok := h.channelMutation(w, r)
	if !ok {
		return
	}
	if err := h.deps.Alerts.TestChannel(r.Context(), principal, id); err != nil {
		alertServiceError(w, err, "notification channel")
		return
	}
	w.WriteHeader(http.StatusNoContent)
}

func (h *handler) channelMutation(w http.ResponseWriter, r *http.Request) (authz.Principal, int64, bool) {
	principal, ok := h.orgManager(w, r, authz.ActionChannelsManage)
	if !ok {
		return authz.Principal{}, 0, false
	}
	id, err := parseInt64(r.PathValue("channel"))
	if err != nil {
		writeProblem(w, http.StatusNotFound, "Not found", "notification channel not found")
		return authz.Principal{}, 0, false
	}
	if h.deps.Alerts == nil {
		unavailable(w, "notification channel")
		return authz.Principal{}, 0, false
	}
	return principal, id, true
}

func decodeChannelInput(w http.ResponseWriter, r *http.Request) (alerts.ChannelInput, bool) {
	var request channelRequest
	if !decodeJSON(w, r, &request) {
		return alerts.ChannelInput{}, false
	}
	input := alerts.ChannelInput{
		Kind: request.Kind, Name: strings.TrimSpace(request.Name),
		Recipients: request.Config.Recipients, URL: strings.TrimSpace(request.Config.URL), Secret: request.Config.Secret,
		BotToken: request.Config.BotToken, ChatID: strings.TrimSpace(request.Config.ChatID), Enabled: request.Enabled,
	}
	if !validChannelInput(input, r.Method == http.MethodPatch) {
		writeProblem(w, http.StatusBadRequest, "Bad request", "notification channel fields are invalid")
		return alerts.ChannelInput{}, false
	}
	return input, true
}

func validChannelInput(input alerts.ChannelInput, update bool) bool {
	if input.Name == "" {
		return false
	}
	switch input.Kind {
	case alerts.ChannelEmail:
		if len(input.Recipients) == 0 || input.URL != "" || input.Secret != "" {
			return false
		}
		for _, recipient := range input.Recipients {
			address, err := mail.ParseAddress(recipient)
			if err != nil || address.Address != recipient {
				return false
			}
		}
		return true
	case alerts.ChannelWebhook, alerts.ChannelSlack, alerts.ChannelDiscord:
		if len(input.Recipients) != 0 || input.URL == "" {
			return false
		}
		parsed, err := url.ParseRequestURI(input.URL)
		return err == nil && (parsed.Scheme == "http" || parsed.Scheme == "https") && parsed.Host != ""
	case alerts.ChannelTelegram:
		return len(input.Recipients) == 0 && input.URL == "" && input.Secret == "" &&
			alerts.ValidTelegramChatID(input.ChatID) &&
			((update && input.BotToken == "") || alerts.ValidTelegramToken(input.BotToken))
	default:
		return false
	}
}

func (h *handler) listAlerts(w http.ResponseWriter, r *http.Request) {
	principal, site, ok := h.sitePrincipal(w, r, authz.ActionAlertsManage, authz.RoleOwner, authz.RoleAdmin, authz.RoleEditor)
	if !ok {
		return
	}
	if h.deps.Alerts == nil {
		unavailable(w, "alert")
		return
	}
	items, err := h.deps.Alerts.ListAlerts(r.Context(), principal, site.ID)
	if err != nil {
		alertServiceError(w, err, "alert")
		return
	}
	writeJSON(w, http.StatusOK, map[string]any{"data": items})
}

func (h *handler) createAlert(w http.ResponseWriter, r *http.Request) {
	principal, site, ok := h.sitePrincipal(w, r, authz.ActionAlertsManage, authz.RoleOwner, authz.RoleAdmin, authz.RoleEditor)
	if !ok {
		return
	}
	input, ok := decodeAlertInput(w, r)
	if !ok {
		return
	}
	if h.deps.Alerts == nil {
		unavailable(w, "alert")
		return
	}
	created, err := h.deps.Alerts.CreateAlert(r.Context(), principal, site.ID, input)
	if err != nil {
		alertServiceError(w, err, "alert")
		return
	}
	writeJSON(w, http.StatusCreated, created)
}

func (h *handler) updateAlert(w http.ResponseWriter, r *http.Request) {
	principal, site, ok := h.sitePrincipal(w, r, authz.ActionAlertsManage, authz.RoleOwner, authz.RoleAdmin, authz.RoleEditor)
	if !ok {
		return
	}
	id, err := parseInt64(r.PathValue("alert"))
	if err != nil {
		writeProblem(w, http.StatusNotFound, "Not found", "alert not found")
		return
	}
	input, ok := decodeAlertInput(w, r)
	if !ok {
		return
	}
	if h.deps.Alerts == nil {
		unavailable(w, "alert")
		return
	}
	updated, err := h.deps.Alerts.UpdateAlert(r.Context(), principal, site.ID, id, input)
	if err != nil {
		alertServiceError(w, err, "alert")
		return
	}
	writeJSON(w, http.StatusOK, updated)
}

func (h *handler) deleteAlert(w http.ResponseWriter, r *http.Request) {
	principal, site, ok := h.sitePrincipal(w, r, authz.ActionAlertsManage, authz.RoleOwner, authz.RoleAdmin, authz.RoleEditor)
	if !ok {
		return
	}
	id, err := parseInt64(r.PathValue("alert"))
	if err != nil {
		writeProblem(w, http.StatusNotFound, "Not found", "alert not found")
		return
	}
	if h.deps.Alerts == nil {
		unavailable(w, "alert")
		return
	}
	if err := h.deps.Alerts.DeleteAlert(r.Context(), principal, site.ID, id); err != nil {
		alertServiceError(w, err, "alert")
		return
	}
	w.WriteHeader(http.StatusNoContent)
}

func (h *handler) listNotificationHistory(w http.ResponseWriter, r *http.Request) {
	principal, site, ok := h.sitePrincipal(w, r, authz.ActionAlertsManage, authz.RoleOwner, authz.RoleAdmin, authz.RoleEditor)
	if !ok {
		return
	}
	filter := alerts.HistoryFilter{Status: alerts.DeliveryStatus(r.URL.Query().Get("status")), ChannelKind: alerts.ChannelKind(r.URL.Query().Get("channel")), Limit: 50}
	if raw := r.URL.Query().Get("alert_id"); raw != "" {
		id, err := strconv.ParseInt(raw, 10, 64)
		if err != nil || id <= 0 {
			writeProblem(w, http.StatusBadRequest, "Bad request", "alert_id is invalid")
			return
		}
		filter.AlertID = id
	}
	if raw := r.URL.Query().Get("limit"); raw != "" {
		limit, err := strconv.Atoi(raw)
		if err != nil || limit < 1 || limit > 100 {
			writeProblem(w, http.StatusBadRequest, "Bad request", "limit must be between 1 and 100")
			return
		}
		filter.Limit = limit
	}
	if filter.Status != "" && filter.Status != alerts.DeliveryPending && filter.Status != alerts.DeliverySucceeded && filter.Status != alerts.DeliveryFailed {
		writeProblem(w, http.StatusBadRequest, "Bad request", "status is invalid")
		return
	}
	if filter.ChannelKind != "" && !validHistoryChannel(filter.ChannelKind) {
		writeProblem(w, http.StatusBadRequest, "Bad request", "channel is invalid")
		return
	}
	items, err := h.deps.Alerts.ListHistory(r.Context(), principal, site.ID, filter)
	if err != nil {
		alertServiceError(w, err, "notification delivery")
		return
	}
	writeJSON(w, http.StatusOK, map[string]any{"data": items})
}

func validHistoryChannel(kind alerts.ChannelKind) bool {
	switch kind {
	case alerts.ChannelEmail, alerts.ChannelWebhook, alerts.ChannelSlack, alerts.ChannelDiscord, alerts.ChannelTelegram:
		return true
	default:
		return false
	}
}

func (h *handler) retryNotificationDelivery(w http.ResponseWriter, r *http.Request) {
	principal, site, ok := h.sitePrincipal(w, r, authz.ActionAlertsManage, authz.RoleOwner, authz.RoleAdmin, authz.RoleEditor)
	if !ok {
		return
	}
	id, err := parseInt64(r.PathValue("delivery"))
	if err != nil {
		writeProblem(w, http.StatusNotFound, "Not found", "notification delivery not found")
		return
	}
	item, err := h.deps.Alerts.RetryDelivery(r.Context(), principal, site.ID, id)
	if err != nil {
		alertServiceError(w, err, "notification delivery")
		return
	}
	writeJSON(w, http.StatusOK, item)
}

func (h *handler) listNotificationTemplates(w http.ResponseWriter, r *http.Request) {
	principal, ok := h.orgManager(w, r, authz.ActionChannelsRead)
	if !ok {
		return
	}
	items, settings, err := h.deps.Alerts.ListTemplates(r.Context(), principal)
	if err != nil {
		alertServiceError(w, err, "notification template")
		return
	}
	writeJSON(w, http.StatusOK, map[string]any{"data": items, "settings": settings, "variables": alerts.TemplateVariables})
}

func decodeTemplateInput(w http.ResponseWriter, r *http.Request) (alerts.TemplateInput, bool) {
	var input alerts.TemplateInput
	if !decodeJSON(w, r, &input) {
		return alerts.TemplateInput{}, false
	}
	return input, true
}

func (h *handler) saveNotificationTemplate(w http.ResponseWriter, r *http.Request) {
	principal, ok := h.orgManager(w, r, authz.ActionChannelsManage)
	if !ok {
		return
	}
	input, ok := decodeTemplateInput(w, r)
	if !ok {
		return
	}
	item, err := h.deps.Alerts.SaveTemplate(r.Context(), principal, alerts.ChannelKind(r.PathValue("kind")), input)
	if err != nil {
		alertServiceError(w, err, "notification template")
		return
	}
	writeJSON(w, http.StatusOK, item)
}

func (h *handler) resetNotificationTemplate(w http.ResponseWriter, r *http.Request) {
	principal, ok := h.orgManager(w, r, authz.ActionChannelsManage)
	if !ok {
		return
	}
	item, err := h.deps.Alerts.ResetTemplate(r.Context(), principal, alerts.ChannelKind(r.PathValue("kind")))
	if err != nil {
		alertServiceError(w, err, "notification template")
		return
	}
	writeJSON(w, http.StatusOK, item)
}

func (h *handler) previewNotificationTemplate(w http.ResponseWriter, r *http.Request) {
	principal, ok := h.orgManager(w, r, authz.ActionChannelsRead)
	if !ok {
		return
	}
	input, ok := decodeTemplateInput(w, r)
	if !ok {
		return
	}
	item, err := h.deps.Alerts.PreviewTemplate(r.Context(), principal, alerts.ChannelKind(r.PathValue("kind")), input)
	if err != nil {
		alertServiceError(w, err, "notification template")
		return
	}
	writeJSON(w, http.StatusOK, item)
}

func (h *handler) saveNotificationSettings(w http.ResponseWriter, r *http.Request) {
	principal, ok := h.orgManager(w, r, authz.ActionChannelsManage)
	if !ok {
		return
	}
	var input alerts.NotificationSettings
	if !decodeJSON(w, r, &input) {
		return
	}
	settings, err := h.deps.Alerts.SaveSettings(r.Context(), principal, input.HistoryRetentionDays)
	if err != nil {
		alertServiceError(w, err, "notification settings")
		return
	}
	writeJSON(w, http.StatusOK, settings)
}

func decodeAlertInput(w http.ResponseWriter, r *http.Request) (alerts.AlertInput, bool) {
	var request alertRequest
	if !decodeJSON(w, r, &request) {
		return alerts.AlertInput{}, false
	}
	input := alerts.AlertInput{
		Kind: request.Kind, Threshold: request.Threshold, GoalID: request.GoalID,
		ChannelIDs: request.ChannelIDs, Enabled: request.Enabled,
	}
	if !validAlertInput(input) {
		writeProblem(w, http.StatusBadRequest, "Bad request", "alert fields are invalid")
		return alerts.AlertInput{}, false
	}
	return input, true
}

func validAlertInput(input alerts.AlertInput) bool {
	enabled := input.Enabled == nil || *input.Enabled
	if input.Threshold <= 0 || enabled && len(input.ChannelIDs) == 0 {
		return false
	}
	seen := make(map[int64]bool, len(input.ChannelIDs))
	for _, id := range input.ChannelIDs {
		if id <= 0 || seen[id] {
			return false
		}
		seen[id] = true
	}
	switch input.Kind {
	case alerts.AlertGoalReached:
		return input.GoalID != nil && *input.GoalID > 0
	case alerts.AlertTrafficSpike, alerts.AlertTrafficDrop:
		return input.GoalID == nil
	default:
		return false
	}
}

func alertServiceError(w http.ResponseWriter, err error, resource string) {
	switch {
	case errors.Is(err, pgx.ErrNoRows):
		writeProblem(w, http.StatusNotFound, "Not found", resource+" not found")
	case errors.Is(err, alerts.ErrForbidden):
		writeProblem(w, http.StatusForbidden, "Forbidden", "the principal cannot perform this action")
	case errors.Is(err, alerts.ErrChannelInUse):
		writeProblem(w, http.StatusConflict, "Conflict", err.Error())
	case errors.Is(err, alerts.ErrInvalid):
		writeProblem(w, http.StatusBadRequest, "Bad request", err.Error())
	case errors.Is(err, alerts.ErrInvalidTemplate):
		writeProblem(w, http.StatusBadRequest, "Bad request", err.Error())
	case errors.Is(err, alerts.ErrRateLimited):
		w.Header().Set("Retry-After", "60")
		writeProblem(w, http.StatusTooManyRequests, "Rate limit exceeded", err.Error())
	case errors.Is(err, alerts.ErrTestDelivery):
		writeProblem(w, http.StatusBadGateway, "Bad gateway", alerts.ErrTestDelivery.Error())
	default:
		serviceError(w, err)
	}
}
