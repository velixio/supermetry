package platformhttp

import "net/http"

func (h *handler) adminUpdateStatus(w http.ResponseWriter, r *http.Request) {
	if _, ok := h.adminPrincipal(w, r); !ok {
		return
	}
	writeJSON(w, http.StatusOK, h.deps.UpdateChecker.Check(r.Context()))
}
