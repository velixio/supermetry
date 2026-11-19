package platformhttp

import (
	"net/http"
	"time"

	"supermetry/internal/platform/authz"
	"supermetry/internal/platform/keys"
)

type apiKeyResponse struct {
	ID     int64    `json:"id"`
	Name   string   `json:"name"`
	Prefix string   `json:"prefix,omitempty"`
	Scopes []string `json:"scopes"`
	// SiteIDs is null for all sites.
	SiteIDs    []int32    `json:"site_ids"`
	CreatedAt  time.Time  `json:"created_at"`
	ExpiresAt  *time.Time `json:"expires_at"`
	LastUsedAt *time.Time `json:"last_used_at"`
	// LastUsedCountry is a two-letter country and LastUsedClient one of mcp,
	// curl, sdk-js, sdk-python, browser or other; both are null until the key
	// is used. The IP address and User-Agent are never stored.
	LastUsedCountry *string `json:"last_used_country"`
	LastUsedClient  *string `json:"last_used_client"`
	Revoked         bool    `json:"revoked"`
	// NeverExpires is true when expires_at is null. ExpiringSoon is true for a
	// live key that expires within 14 days.
	NeverExpires bool `json:"never_expires"`
	ExpiringSoon bool `json:"expiring_soon"`
}

func (h *handler) listPublicAPIKeys(w http.ResponseWriter, r *http.Request) {
	principal, ok := h.publicKeyManager(w, r)
	if !ok {
		return
	}
	if h.deps.Keys == nil {
		unavailable(w, "API key")
		return
	}
	items, err := h.deps.Keys.List(r.Context(), principal)
	if err != nil {
		serviceError(w, err)
		return
	}
	responses := apiKeyResponses(items)
	for i := range responses {
		responses[i].Prefix = "" // the public API never echoes any part of a secret
	}
	writeJSON(w, http.StatusOK, map[string]any{"data": responses})
}

func (h *handler) createPublicAPIKey(w http.ResponseWriter, r *http.Request) {
	principal, ok := h.publicKeyManager(w, r)
	if !ok {
		return
	}
	var request CreateAPIKeyRequest
	if !decodeJSON(w, r, &request) {
		return
	}
	if h.deps.Mutations == nil {
		unavailable(w, "API key")
		return
	}
	created, err := h.deps.Mutations.CreateAPIKey(r.Context(), principal, request)
	if err != nil {
		serviceError(w, err)
		return
	}
	writeJSON(w, http.StatusCreated, created)
}

func (h *handler) revokePublicAPIKey(w http.ResponseWriter, r *http.Request) {
	principal, ok := h.publicKeyManager(w, r)
	if !ok {
		return
	}
	id, err := parseInt64(r.PathValue("key"))
	if err != nil {
		writeProblem(w, http.StatusNotFound, "Not found", "API key not found")
		return
	}
	if h.deps.Keys == nil {
		unavailable(w, "API key")
		return
	}
	if err := h.deps.Keys.Revoke(r.Context(), principal, id); err != nil {
		serviceError(w, err)
		return
	}
	w.WriteHeader(http.StatusNoContent)
}

func (h *handler) publicKeyManager(w http.ResponseWriter, r *http.Request) (authz.Principal, bool) {
	principal, ok := h.principal(w, r, authz.RoleKey)
	if !ok {
		return authz.Principal{}, false
	}
	if !authz.Can(principal, authz.ActionKeysManage, 0) {
		writeProblem(w, http.StatusForbidden, "Forbidden", "the credential cannot manage API keys")
		return authz.Principal{}, false
	}
	return principal, true
}

func apiKeyResponses(items []keys.KeyInfo) []apiKeyResponse {
	now := time.Now()
	responses := make([]apiKeyResponse, 0, len(items))
	for _, item := range items {
		response := apiKeyResponse{
			ID: item.ID, Name: item.Name, Prefix: item.Prefix, Scopes: item.Scopes, SiteIDs: item.SiteIDs,
			CreatedAt: item.CreatedAt, ExpiresAt: item.ExpiresAt, LastUsedAt: item.LastUsedAt,
			Revoked: item.RevokedAt != nil, NeverExpires: item.NeverExpires(), ExpiringSoon: item.ExpiringSoon(now),
		}
		if item.LastUsedCountry != "" {
			response.LastUsedCountry = &item.LastUsedCountry
		}
		if item.LastUsedClient != "" {
			response.LastUsedClient = &item.LastUsedClient
		}
		responses = append(responses, response)
	}
	return responses
}

func (h *handler) rollPublicAPIKey(w http.ResponseWriter, r *http.Request) {
	principal, ok := h.publicKeyManager(w, r)
	if !ok {
		return
	}
	h.rollAPIKeyFor(w, r, principal)
}

func (h *handler) rollAPIKey(w http.ResponseWriter, r *http.Request) {
	principal, ok := h.orgManager(w, r, authz.ActionKeysManage)
	if !ok || !h.requireRecentAuth(w, r, principal) {
		return
	}
	h.rollAPIKeyFor(w, r, principal)
}

func (h *handler) rollAPIKeyFor(w http.ResponseWriter, r *http.Request, principal authz.Principal) {
	id, err := parseInt64(r.PathValue("key"))
	if err != nil {
		writeProblem(w, http.StatusNotFound, "Not found", "API key not found")
		return
	}
	var request RollAPIKeyRequest
	if r.ContentLength != 0 && !decodeJSON(w, r, &request) {
		return
	}
	if h.deps.Mutations == nil {
		unavailable(w, "API key")
		return
	}
	rolled, err := h.deps.Mutations.RollAPIKey(r.Context(), principal, id, request)
	if err != nil {
		serviceError(w, err)
		return
	}
	writeJSON(w, http.StatusCreated, rolled)
}
