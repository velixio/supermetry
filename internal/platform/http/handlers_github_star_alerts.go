package platformhttp

import (
	"net/http"

	"supermetry/internal/ghstars"
)

type starAlertSettingsResponse struct {
	Events     map[ghstars.AlertKind]bool `json:"events"`
	ChannelIDs []int64                    `json:"channel_ids"`
	EventKinds []ghstars.AlertKindInfo    `json:"event_kinds"`
}

func newStarAlertSettingsResponse(s ghstars.AlertSettings) starAlertSettingsResponse {
	return starAlertSettingsResponse{Events: s.Events, ChannelIDs: s.ChannelIDs, EventKinds: ghstars.AlertKinds()}
}

func (h *handler) orgStarsAlerts(w http.ResponseWriter, r *http.Request) {
	svc, _, orgID, ok := h.starsService(w, r, false)
	if !ok {
		return
	}
	id, valid := adminID(r, "id")
	if !valid {
		writeProblem(w, http.StatusBadRequest, "Bad request", "repository id is invalid")
		return
	}
	settings, err := svc.AlertSettings(r.Context(), orgID, id)
	if err != nil {
		starsError(w, err)
		return
	}
	writeJSON(w, http.StatusOK, newStarAlertSettingsResponse(settings))
}

func (h *handler) orgStarsSaveAlerts(w http.ResponseWriter, r *http.Request) {
	svc, principal, orgID, ok := h.starsService(w, r, true)
	if !ok {
		return
	}
	id, valid := adminID(r, "id")
	if !valid {
		writeProblem(w, http.StatusBadRequest, "Bad request", "repository id is invalid")
		return
	}
	var request struct {
		Events     map[ghstars.AlertKind]bool `json:"events"`
		ChannelIDs []int64                    `json:"channel_ids"`
	}
	if !decodeJSON(w, r, &request) {
		return
	}
	settings, err := svc.SaveAlertSettings(r.Context(), orgID, id, ghstars.AlertSettings{Events: request.Events, ChannelIDs: request.ChannelIDs}, principal.UserID)
	if err != nil {
		starsError(w, err)
		return
	}
	writeJSON(w, http.StatusOK, newStarAlertSettingsResponse(settings))
}

func (h *handler) orgStarsTestAlert(w http.ResponseWriter, r *http.Request) {
	svc, _, orgID, ok := h.starsService(w, r, true)
	if !ok {
		return
	}
	id, valid := adminID(r, "id")
	if !valid {
		writeProblem(w, http.StatusBadRequest, "Bad request", "repository id is invalid")
		return
	}
	results, err := svc.SendTestAlert(r.Context(), orgID, id)
	if err != nil {
		starsError(w, err)
		return
	}
	if results == nil {
		results = []ghstars.AlertDelivery{}
	}
	writeJSON(w, http.StatusOK, map[string]any{"data": results})
}
