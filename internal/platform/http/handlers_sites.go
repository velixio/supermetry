package platformhttp

import (
	"context"
	"encoding/base64"
	"encoding/json"
	"errors"
	"log/slog"
	"net/http"
	"strconv"
	"strings"
	"time"

	"panel/internal/db"
	"panel/internal/platform/authz"
	"panel/internal/platform/sitesvc"
	coresites "panel/internal/sites"
)

type siteResponse struct {
	ID              int32    `json:"id"`
	OrgID           int64    `json:"org_id"`
	PublicID        string   `json:"public_id"`
	Name            string   `json:"name"`
	Timezone        string   `json:"timezone"`
	RetentionDays   int      `json:"retention_days"`
	AllowedHosts    []string `json:"allowed_hosts"`
	ExcludeIPs      []string `json:"exclude_ips"`
	ExcludePaths    []string `json:"exclude_paths"`
	ProfilesEnabled bool     `json:"profiles_enabled"`
	HeatmapsEnabled bool     `json:"heatmaps_enabled"`
	IconURL         string   `json:"icon_url"`
}

func (h *handler) listSites(w http.ResponseWriter, r *http.Request) {
	principal, ok := h.resourcePrincipal(w, r)
	if !ok {
		return
	}
	limit, valid := parseLimit(r)
	if !valid {
		writeProblem(w, http.StatusBadRequest, "Bad request", "limit must be between 1 and 100")
		return
	}
	if h.deps.Store != nil {
		result, next, err := h.listStoredSites(r, principal, limit)
		if errors.Is(err, errInvalidSiteCursor) {
			writeProblem(w, http.StatusBadRequest, "Bad request", "cursor is invalid")
			return
		}
		if err != nil {
			serviceError(w, err)
			return
		}
		writeJSON(w, http.StatusOK, map[string]any{"data": result, "next_cursor": nullableCursor(next)})
		return
	}
	if lister, ok := h.deps.Mutations.(interface {
		ListSites(context.Context, authz.Principal, string, int) ([]coresites.Site, string, error)
	}); ok {
		sites, next, err := lister.ListSites(r.Context(), principal, r.URL.Query().Get("cursor"), limit)
		if err != nil {
			writeProblem(w, http.StatusBadRequest, "Bad request", "cursor or limit is invalid")
			return
		}
		result := make([]siteResponse, 0, len(sites))
		for _, site := range sites {
			result = append(result, h.responseSite(r, site))
		}
		writeJSON(w, http.StatusOK, map[string]any{"data": result, "next_cursor": nullableCursor(next)})
		return
	}
	result := make([]siteResponse, 0, limit)
	if h.deps.Sites != nil {
		for _, site := range h.deps.Sites.All() {
			if site.OrgID == principal.OrgID && allowsSite(principal.SiteIDs, site.ID) &&
				(authz.Can(principal, authz.ActionStatsRead, site.ID) || authz.Can(principal, authz.ActionSiteEdit, site.ID)) {
				result = append(result, h.responseSite(r, site))
				if len(result) == limit {
					break
				}
			}
		}
	}
	writeJSON(w, http.StatusOK, map[string]any{"data": result, "next_cursor": nil})
}

type siteCursor struct {
	CreatedAt time.Time `json:"created_at"`
	ID        int32     `json:"id"`
}

var errInvalidSiteCursor = errors.New("invalid site cursor")

func (h *handler) listStoredSites(r *http.Request, principal authz.Principal, limit int) ([]siteResponse, string, error) {
	cursor, hasCursor, err := decodeSiteCursor(r.URL.Query().Get("cursor"))
	if err != nil {
		return nil, "", err
	}
	query := `select id,org_id,public_id,name,allowed_hosts,timezone,retention_class,
		coalesce(settings->'exclude_ips','[]'::jsonb),coalesce(settings->'exclude_paths','[]'::jsonb),
		coalesce(settings->'profiles_enabled'='true'::jsonb,false),
		coalesce(settings->'heatmaps_enabled'='true'::jsonb,false),
		coalesce(settings->>'icon_url',''),created_at
		from sites where org_id=$1 and deleted_at is null and ($2::integer[] is null or id=any($2))
		order by created_at,id limit $3`
	args := []any{principal.OrgID, principal.SiteIDs, limit + 1}
	if hasCursor {
		query = `select id,org_id,public_id,name,allowed_hosts,timezone,retention_class,
			coalesce(settings->'exclude_ips','[]'::jsonb),coalesce(settings->'exclude_paths','[]'::jsonb),
			coalesce(settings->'profiles_enabled'='true'::jsonb,false),
			coalesce(settings->'heatmaps_enabled'='true'::jsonb,false),
			coalesce(settings->>'icon_url',''),created_at
			from sites where org_id=$1 and deleted_at is null and ($2::integer[] is null or id=any($2))
			and (created_at,id)>($3,$4) order by created_at,id limit $5`
		args = []any{principal.OrgID, principal.SiteIDs, cursor.CreatedAt, cursor.ID, limit + 1}
	}
	rows, err := h.deps.Store.Pool.Query(r.Context(), query, args...)
	if err != nil {
		return nil, "", err
	}
	defer rows.Close()
	type row struct {
		response  siteResponse
		createdAt time.Time
	}
	items := make([]row, 0, limit+1)
	for rows.Next() {
		var item row
		var timezone string
		var retention int16
		if err := rows.Scan(&item.response.ID, &item.response.OrgID, &item.response.PublicID, &item.response.Name,
			&item.response.AllowedHosts, &timezone, &retention, &item.response.ExcludeIPs,
			&item.response.ExcludePaths, &item.response.ProfilesEnabled, &item.response.HeatmapsEnabled,
			&item.response.IconURL, &item.createdAt); err != nil {
			return nil, "", err
		}
		item.response.Timezone = timezone
		item.response.RetentionDays = int(db.RetentionClass(retention))
		if !authz.Can(principal, authz.ActionStatsRead, item.response.ID) && !authz.Can(principal, authz.ActionSiteEdit, item.response.ID) {
			continue
		}
		items = append(items, item)
	}
	if err := rows.Err(); err != nil {
		return nil, "", err
	}
	next := ""
	if len(items) > limit {
		last := items[limit-1]
		next = encodeSiteCursor(siteCursor{CreatedAt: last.createdAt, ID: last.response.ID})
		items = items[:limit]
	}
	result := make([]siteResponse, len(items))
	for index := range items {
		result[index] = items[index].response
	}
	return result, next, nil
}

func encodeSiteCursor(cursor siteCursor) string {
	encoded, _ := json.Marshal(cursor)
	return base64.RawURLEncoding.EncodeToString(encoded)
}

func decodeSiteCursor(value string) (siteCursor, bool, error) {
	if value == "" {
		return siteCursor{}, false, nil
	}
	decoded, err := base64.RawURLEncoding.Strict().DecodeString(value)
	if err != nil {
		return siteCursor{}, false, errInvalidSiteCursor
	}
	var cursor siteCursor
	if err := json.Unmarshal(decoded, &cursor); err != nil || cursor.CreatedAt.IsZero() || cursor.ID <= 0 {
		return siteCursor{}, false, errInvalidSiteCursor
	}
	return cursor, true, nil
}

func (s Services) ListSites(ctx context.Context, principal authz.Principal, cursor string, limit int) ([]coresites.Site, string, error) {
	if s.Sites == nil {
		return nil, "", errors.New("site service is unavailable")
	}
	return s.Sites.List(ctx, principal, cursor, limit)
}

func (h *handler) createSite(w http.ResponseWriter, r *http.Request) {
	principal, ok := h.resourcePrincipal(w, r)
	if !ok {
		return
	}
	if !authz.Can(principal, authz.ActionSiteCreate, 0) {
		writeProblem(w, http.StatusForbidden, "Forbidden", "the principal cannot create sites")
		return
	}
	var request CreateSiteRequest
	if !decodeJSON(w, r, &request) {
		return
	}
	if h.deps.Mutations == nil {
		unavailable(w, "site")
		return
	}
	site, err := h.deps.Mutations.CreateSite(r.Context(), principal, request)
	if err != nil {
		serviceError(w, err)
		return
	}
	response := h.responseSite(r, site)
	if response.Name == "" {
		response.Name = request.Name
	}
	h.startIconDiscovery(principal, site.ID)
	writeJSON(w, http.StatusCreated, response)
}

func (h *handler) getSite(w http.ResponseWriter, r *http.Request) {
	roles := []authz.Role{authz.RoleOwner, authz.RoleAdmin, authz.RoleEditor, authz.RoleViewer}
	if !strings.HasPrefix(r.URL.Path, "/api/public/v1/") {
		_, site, ok := h.sitePrincipal(w, r, authz.ActionStatsRead, roles...)
		if ok {
			writeJSON(w, http.StatusOK, h.responseSite(r, site))
		}
		return
	}
	principal, ok := h.principal(w, r)
	if !ok {
		return
	}
	siteID, err := parseInt32(r.PathValue("site"))
	if err != nil {
		writeProblem(w, http.StatusBadRequest, "Bad request", "site must be a positive integer")
		return
	}
	_, site, ok := h.visibleSite(w, principal, siteID)
	if !ok {
		return
	}
	if principal.Role != authz.RoleKey ||
		(!authz.Can(principal, authz.ActionStatsRead, siteID) && !authz.Can(principal, authz.ActionSiteEdit, siteID)) {
		writeProblem(w, http.StatusForbidden, "Forbidden", "the principal cannot perform this action")
		return
	}
	writeJSON(w, http.StatusOK, h.responseSite(r, site))
}

func (h *handler) updateSite(w http.ResponseWriter, r *http.Request) {
	roles := []authz.Role{authz.RoleOwner, authz.RoleAdmin, authz.RoleEditor, authz.RoleViewer}
	if strings.HasPrefix(r.URL.Path, "/api/public/v1/") {
		roles = []authz.Role{authz.RoleKey}
	}
	principal, site, ok := h.sitePrincipal(w, r, authz.ActionSiteEdit, roles...)
	if !ok {
		return
	}
	var request struct {
		Name            *string   `json:"name"`
		Hosts           *[]string `json:"hosts"`
		Timezone        *string   `json:"timezone"`
		RetentionDays   *int      `json:"retention_days"`
		ExcludeIPs      *[]string `json:"exclude_ips"`
		ExcludePaths    *[]string `json:"exclude_paths"`
		ProfilesEnabled *bool     `json:"profiles_enabled"`
		HeatmapsEnabled *bool     `json:"heatmaps_enabled"`
	}
	if !decodeJSON(w, r, &request) {
		return
	}
	if request.ProfilesEnabled != nil && principal.Role != authz.RoleOwner && principal.Role != authz.RoleAdmin {
		writeProblem(w, http.StatusForbidden, "Forbidden", "only owners and admins can change profiles")
		return
	}
	if h.deps.Mutations == nil {
		unavailable(w, "site")
		return
	}
	updated, err := h.deps.Mutations.UpdateSite(r.Context(), principal, site.ID, sitesvc.SitePatch{
		Name: request.Name, Hosts: request.Hosts, TZ: request.Timezone, RetentionDays: request.RetentionDays,
		ExcludeIPs: request.ExcludeIPs, ExcludePaths: request.ExcludePaths,
		ProfilesEnabled: request.ProfilesEnabled, HeatmapsEnabled: request.HeatmapsEnabled,
	})
	if err != nil {
		serviceError(w, err)
		return
	}
	response := h.responseSite(r, updated)
	if response.Name == "" && request.Name != nil {
		response.Name = *request.Name
	}
	writeJSON(w, http.StatusOK, response)
}

func (h *handler) deleteSite(w http.ResponseWriter, r *http.Request) {
	roles := []authz.Role{authz.RoleOwner, authz.RoleAdmin}
	if strings.HasPrefix(r.URL.Path, "/api/public/v1/") {
		roles = []authz.Role{authz.RoleKey}
	}
	principal, site, ok := h.sitePrincipal(w, r, authz.ActionSiteDelete, roles...)
	if !ok {
		return
	}
	if h.deps.Mutations == nil {
		unavailable(w, "site")
		return
	}
	if err := h.deps.Mutations.DeleteSite(r.Context(), principal, site.ID); err != nil {
		serviceError(w, err)
		return
	}
	w.WriteHeader(http.StatusNoContent)
}

func (h *handler) responseSite(r *http.Request, site coresites.Site) siteResponse {
	timezone := ""
	if site.TZ != nil {
		timezone = site.TZ.String()
	}
	response := siteResponse{
		ID: site.ID, OrgID: site.OrgID, PublicID: site.PublicID, Timezone: timezone,
		RetentionDays: int(site.Retention), AllowedHosts: append([]string(nil), site.AllowedHosts...),
		ExcludeIPs: append([]string{}, site.Exclusions.IPRules...), ExcludePaths: append([]string{}, site.Exclusions.PathRules...),
		ProfilesEnabled: site.ProfilesEnabled,
		HeatmapsEnabled: site.HeatmapsEnabled,
		IconURL:         site.IconURL,
	}
	if h.deps.Store != nil {
		_ = h.deps.Store.Pool.QueryRow(r.Context(), `select name,coalesce(settings->>'icon_url','') from sites where id=$1 and org_id=$2 and deleted_at is null`, site.ID, site.OrgID).Scan(&response.Name, &response.IconURL)
	}
	return response
}

func (h *handler) startIconDiscovery(principal authz.Principal, siteID int32) {
	service, ok := h.deps.Mutations.(siteIconMutationService)
	if !ok {
		return
	}
	select {
	case h.iconDiscoveries <- struct{}{}:
	default:
		slog.Debug("site icon discovery skipped; capacity reached", "site_id", siteID)
		return
	}
	h.iconDiscoveryWG.Add(1)
	go func() {
		defer h.iconDiscoveryWG.Done()
		defer func() { <-h.iconDiscoveries }()
		ctx, cancel := context.WithTimeout(context.Background(), 15*time.Second)
		defer cancel()
		if _, err := service.RefreshSiteIcon(ctx, principal, siteID); err != nil {
			slog.Warn("site icon discovery failed", "site_id", siteID, "error", err)
		}
	}()
}

func (h *handler) refetchSiteIcon(w http.ResponseWriter, r *http.Request) {
	roles := []authz.Role{authz.RoleOwner, authz.RoleAdmin}
	principal, site, ok := h.sitePrincipal(w, r, authz.ActionSiteEdit, roles...)
	if !ok {
		return
	}
	service, ok := h.deps.Mutations.(siteIconMutationService)
	if !ok {
		unavailable(w, "site icon")
		return
	}
	now := time.Now()
	h.iconRefetchMu.Lock()
	last := h.iconRefetched[site.ID]
	if remaining := 30*time.Second - now.Sub(last); !last.IsZero() && remaining > 0 {
		retry := int((remaining + time.Second - 1) / time.Second)
		h.iconRefetchMu.Unlock()
		w.Header().Set("Retry-After", strconv.Itoa(retry))
		writeProblem(w, http.StatusTooManyRequests, "Rate limit exceeded", "retry after the indicated delay")
		return
	}
	h.iconRefetched[site.ID] = now
	h.iconRefetchMu.Unlock()

	ctx, cancel := context.WithTimeout(r.Context(), 15*time.Second)
	defer cancel()
	updated, err := service.RefreshSiteIcon(ctx, principal, site.ID)
	if err != nil {
		serviceError(w, err)
		return
	}
	writeJSON(w, http.StatusOK, h.responseSite(r, updated))
}

func parseLimit(r *http.Request) (int, bool) {
	if value := r.URL.Query().Get("limit"); value != "" {
		limit, err := strconv.Atoi(value)
		return limit, err == nil && limit >= 1 && limit <= 100
	}
	return 50, true
}

// siteSnippet returns the installation snippet for a site the caller can read.
func (h *handler) siteSnippet(w http.ResponseWriter, r *http.Request) {
	roles := []authz.Role{authz.RoleOwner, authz.RoleAdmin, authz.RoleEditor, authz.RoleViewer}
	_, site, ok := h.sitePrincipal(w, r, authz.ActionStatsRead, roles...)
	if !ok {
		return
	}
	if h.deps.Snippet == nil {
		writeProblem(w, http.StatusInternalServerError, "Internal server error", "snippet is not configured")
		return
	}
	writeJSON(w, http.StatusOK, map[string]string{
		"snippet":      h.deps.Snippet(site.PublicID, false),
		"full_snippet": h.deps.Snippet(site.PublicID, true),
	})
}
