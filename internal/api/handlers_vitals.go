package api

import (
	"context"
	"net/http"
)

type vitalsService interface {
	Vitals(context.Context, VitalsRequest) (VitalsResponse, error)
}

func (h *handlers) vitalsRequest(w http.ResponseWriter, r *http.Request) {
	service, ok := h.query.(vitalsService)
	if !ok {
		writeProblem(w, internalProblem())
		return
	}
	var request VitalsRequest
	if problem := decodeJSON(w, r, &request); problem != nil {
		writeProblem(w, *problem)
		return
	}
	if !authorizeRequest(w, r, request.SiteId, "stats:read") {
		return
	}
	response, err := service.Vitals(r.Context(), request)
	if err != nil {
		writeQueryError(w, err)
		return
	}
	writeJSON(w, http.StatusOK, response)
}
