package api

import (
	"context"
	"net/http"
)

type heatmapService interface {
	Query(context.Context, HeatmapRequest) (HeatmapResponse, error)
}

func (h *handlers) heatmapRequest(w http.ResponseWriter, r *http.Request) {
	if h.heatmaps == nil {
		writeProblem(w, internalProblem())
		return
	}
	var request HeatmapRequest
	if problem := decodeJSON(w, r, &request); problem != nil {
		writeProblem(w, *problem)
		return
	}
	if !authorizeRequest(w, r, request.SiteId, "stats:read") {
		return
	}
	response, err := h.heatmaps.Query(r.Context(), request)
	if err != nil {
		writeQueryError(w, err)
		return
	}
	writeJSON(w, http.StatusOK, response)
}
