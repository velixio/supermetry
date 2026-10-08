package platformhttp

import (
	"net/http"
	"time"

	"panel/internal/platform/authz"
	"panel/internal/platform/keys"
)

type apiKeyResponse struct {
	ID         int64      `json:"id"`
	Name       string     `json:"name"`
	Scopes     []string   `json:"scopes"`
	SiteIDs    []int32    `json:"site_ids"`
	CreatedAt  time.Time  `json:"created_at"`
	ExpiresAt  *time.Time `json:"expires_at"`
	LastUsedAt *time.Time `json:"last_used_at"`
	Revoked    bool       `json:"revoked"`
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
	writeJSON(w, http.StatusOK, map[string]any{"data": apiKeyResponses(items)})
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
	responses := make([]apiKeyResponse, 0, len(items))
	for _, item := range items {
		responses = append(responses, apiKeyResponse{
			ID: item.ID, Name: item.Name, Scopes: item.Scopes, SiteIDs: item.SiteIDs,
			CreatedAt: item.CreatedAt, ExpiresAt: item.ExpiresAt, LastUsedAt: item.LastUsedAt,
			Revoked: item.RevokedAt != nil,
		})
	}
	return responses
}
