package platformhttp

import (
	"context"
	"net/http"

	"supermetry/internal/integrations/stripe"
	"supermetry/internal/platform/authz"
)

// StripeService is the transport-facing subset of the Stripe revenue service.
type StripeService interface {
	Get(context.Context, authz.Principal, int32) (stripe.Status, error)
	Enable(context.Context, authz.Principal, int32) (stripe.Status, error)
	Configure(context.Context, authz.Principal, int32, stripe.Update) (stripe.Status, error)
	Disconnect(context.Context, authz.Principal, int32) error
}

func (h *handler) getStripe(w http.ResponseWriter, r *http.Request) {
	principal, site, ok := h.sitePrincipal(w, r, authz.ActionSiteEdit, authz.RoleOwner, authz.RoleAdmin, authz.RoleEditor)
	if !ok {
		return
	}
	if h.deps.Stripe == nil {
		unavailable(w, "integration")
		return
	}
	status, err := h.deps.Stripe.Get(r.Context(), principal, site.ID)
	if err != nil {
		integrationError(w, err)
		return
	}
	writeJSON(w, http.StatusOK, status)
}

func (h *handler) enableStripe(w http.ResponseWriter, r *http.Request) {
	principal, site, ok := h.sitePrincipal(w, r, authz.ActionSiteEdit, authz.RoleOwner, authz.RoleAdmin)
	if !ok {
		return
	}
	if h.deps.Stripe == nil {
		unavailable(w, "integration")
		return
	}
	status, err := h.deps.Stripe.Enable(r.Context(), principal, site.ID)
	if err != nil {
		integrationError(w, err)
		return
	}
	writeJSON(w, http.StatusOK, status)
}

func (h *handler) configureStripe(w http.ResponseWriter, r *http.Request) {
	principal, site, ok := h.sitePrincipal(w, r, authz.ActionSiteEdit, authz.RoleOwner, authz.RoleAdmin)
	if !ok {
		return
	}
	var update stripe.Update
	if !decodeJSON(w, r, &update) {
		return
	}
	if h.deps.Stripe == nil {
		unavailable(w, "integration")
		return
	}
	status, err := h.deps.Stripe.Configure(r.Context(), principal, site.ID, update)
	if err != nil {
		integrationError(w, err)
		return
	}
	writeJSON(w, http.StatusOK, status)
}

func (h *handler) disconnectStripe(w http.ResponseWriter, r *http.Request) {
	principal, site, ok := h.sitePrincipal(w, r, authz.ActionSiteEdit, authz.RoleOwner, authz.RoleAdmin)
	if !ok {
		return
	}
	if h.deps.Stripe == nil {
		unavailable(w, "integration")
		return
	}
	if err := h.deps.Stripe.Disconnect(r.Context(), principal, site.ID); err != nil {
		integrationError(w, err)
		return
	}
	w.WriteHeader(http.StatusNoContent)
}
