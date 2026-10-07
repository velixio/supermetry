package platformhttp

import (
	"encoding/json"
	"errors"
	"net/http"
	"strings"

	"github.com/jackc/pgx/v5"

	"panel/internal/platform/audit"
	"panel/internal/platform/authz"
	coresites "panel/internal/sites"
)

type goalInput struct {
	Kind      string `json:"kind"`
	Match     string `json:"match"`
	Value     string `json:"value"`
	PropKey   string `json:"prop_key,omitempty"`
	PropValue string `json:"prop_value,omitempty"`
	Name      string `json:"name"`
}

type goalResponse struct {
	ID     int64 `json:"id"`
	SiteID int32 `json:"site_id"`
	goalInput
}

type funnelStep struct {
	Type      string `json:"type"`
	Match     string `json:"match"`
	Value     string `json:"value"`
	PropKey   string `json:"prop_key,omitempty"`
	PropValue string `json:"prop_value,omitempty"`
}

type funnelInput struct {
	Name  string       `json:"name"`
	Steps []funnelStep `json:"steps"`
}

type funnelResponse struct {
	ID     int64 `json:"id"`
	SiteID int32 `json:"site_id"`
	funnelInput
}

func (h *handler) resourceRoles(r *http.Request) []authz.Role {
	if strings.HasPrefix(r.URL.Path, "/api/public/v1/") {
		return []authz.Role{authz.RoleKey}
	}
	return []authz.Role{authz.RoleOwner, authz.RoleAdmin, authz.RoleEditor, authz.RoleViewer}
}

func (h *handler) listGoals(w http.ResponseWriter, r *http.Request) {
	_, site, ok := h.readDefinitionsPrincipal(w, r)
	if !ok {
		return
	}
	if h.deps.Store == nil {
		unavailable(w, "goal")
		return
	}
	rows, err := h.deps.Store.Pool.Query(r.Context(), `select id,site_id,kind,match,value,coalesce(prop_key,''),coalesce(prop_value,''),name from goals where site_id=$1 order by id`, site.ID)
	if err != nil {
		serviceError(w, err)
		return
	}
	defer rows.Close()
	result := make([]goalResponse, 0)
	for rows.Next() {
		var goal goalResponse
		if err := rows.Scan(&goal.ID, &goal.SiteID, &goal.Kind, &goal.Match, &goal.Value, &goal.PropKey, &goal.PropValue, &goal.Name); err != nil {
			serviceError(w, err)
			return
		}
		result = append(result, goal)
	}
	if err := rows.Err(); err != nil {
		serviceError(w, err)
		return
	}
	writeJSON(w, http.StatusOK, result)
}

func (h *handler) createGoal(w http.ResponseWriter, r *http.Request) {
	principal, site, ok := h.sitePrincipal(w, r, authz.ActionGoalsEdit, h.resourceRoles(r)...)
	if !ok {
		return
	}
	var input goalInput
	if !decodeJSON(w, r, &input) {
		return
	}
	if !validGoal(input) {
		writeProblem(w, http.StatusBadRequest, "Bad request", "goal fields are invalid")
		return
	}
	if h.deps.Store == nil {
		unavailable(w, "goal")
		return
	}
	var created goalResponse
	err := h.deps.Store.Tx(r.Context(), func(tx pgx.Tx) error {
		created.SiteID, created.goalInput = site.ID, input
		if err := tx.QueryRow(r.Context(), `insert into goals(site_id,kind,match,value,prop_key,prop_value,name) values($1,$2,$3,$4,nullif($5,''),nullif($6,''),$7) returning id`, site.ID, input.Kind, input.Match, input.Value, input.PropKey, input.PropValue, input.Name).Scan(&created.ID); err != nil {
			return err
		}
		return audit.Write(r.Context(), tx, principal, "goal.created", "goal", created.ID, map[string]any{"site_id": site.ID})
	})
	if err != nil {
		serviceError(w, err)
		return
	}
	h.invalidateDefinitions()
	writeJSON(w, http.StatusCreated, created)
}

func (h *handler) updateGoal(w http.ResponseWriter, r *http.Request) {
	principal, goal, ok := h.goalPrincipal(w, r, authz.ActionGoalsEdit)
	if !ok {
		return
	}
	var input goalInput
	if !decodeJSON(w, r, &input) {
		return
	}
	if !validGoal(input) {
		writeProblem(w, http.StatusBadRequest, "Bad request", "goal fields are invalid")
		return
	}
	err := h.deps.Store.Tx(r.Context(), func(tx pgx.Tx) error {
		if _, err := tx.Exec(r.Context(), `update goals set kind=$2,match=$3,value=$4,prop_key=nullif($5,''),prop_value=nullif($6,''),name=$7 where id=$1`, goal.ID, input.Kind, input.Match, input.Value, input.PropKey, input.PropValue, input.Name); err != nil {
			return err
		}
		return audit.Write(r.Context(), tx, principal, "goal.updated", "goal", goal.ID, map[string]any{"site_id": goal.SiteID})
	})
	if err != nil {
		serviceError(w, err)
		return
	}
	h.invalidateDefinitions()
	goal.goalInput = input
	writeJSON(w, http.StatusOK, goal)
}

func (h *handler) deleteGoal(w http.ResponseWriter, r *http.Request) {
	principal, goal, ok := h.goalPrincipal(w, r, authz.ActionGoalsEdit)
	if !ok {
		return
	}
	err := h.deps.Store.Tx(r.Context(), func(tx pgx.Tx) error {
		if _, err := tx.Exec(r.Context(), `delete from goals where id=$1`, goal.ID); err != nil {
			return err
		}
		return audit.Write(r.Context(), tx, principal, "goal.deleted", "goal", goal.ID, map[string]any{"site_id": goal.SiteID})
	})
	if err != nil {
		serviceError(w, err)
		return
	}
	h.invalidateDefinitions()
	w.WriteHeader(http.StatusNoContent)
}

func (h *handler) goalPrincipal(w http.ResponseWriter, r *http.Request, action authz.Action) (authz.Principal, goalResponse, bool) {
	principal, ok := h.principal(w, r)
	if !ok {
		return authz.Principal{}, goalResponse{}, false
	}
	id, err := parseInt64(r.PathValue("goal"))
	if err != nil || h.deps.Store == nil {
		writeProblem(w, http.StatusNotFound, "Not found", "goal not found")
		return authz.Principal{}, goalResponse{}, false
	}
	var goal goalResponse
	err = h.deps.Store.Pool.QueryRow(r.Context(), `select id,site_id,kind,match,value,coalesce(prop_key,''),coalesce(prop_value,''),name from goals where id=$1`, id).Scan(&goal.ID, &goal.SiteID, &goal.Kind, &goal.Match, &goal.Value, &goal.PropKey, &goal.PropValue, &goal.Name)
	if errors.Is(err, pgx.ErrNoRows) {
		writeProblem(w, http.StatusNotFound, "Not found", "goal not found")
		return authz.Principal{}, goalResponse{}, false
	}
	if err != nil {
		serviceError(w, err)
		return authz.Principal{}, goalResponse{}, false
	}
	if _, _, ok := h.visibleSite(w, principal, goal.SiteID); !ok {
		return authz.Principal{}, goalResponse{}, false
	}
	if !roleAllowed(principal.Role, h.resourceRoles(r)) || !authz.Can(principal, action, goal.SiteID) {
		writeProblem(w, http.StatusForbidden, "Forbidden", "the principal cannot perform this action")
		return authz.Principal{}, goalResponse{}, false
	}
	return principal, goal, true
}

func validGoal(input goalInput) bool {
	return (input.Kind == "page" || input.Kind == "event") &&
		(input.Match == "exact" || input.Match == "prefix" || input.Match == "wildcard") &&
		strings.TrimSpace(input.Value) != "" && strings.TrimSpace(input.Name) != ""
}

func (h *handler) listFunnels(w http.ResponseWriter, r *http.Request) {
	_, site, ok := h.readDefinitionsPrincipal(w, r)
	if !ok {
		return
	}
	if h.deps.Store == nil {
		unavailable(w, "funnel")
		return
	}
	rows, err := h.deps.Store.Pool.Query(r.Context(), `select id,site_id,name,steps from funnels where site_id=$1 order by id`, site.ID)
	if err != nil {
		serviceError(w, err)
		return
	}
	defer rows.Close()
	result := make([]funnelResponse, 0)
	for rows.Next() {
		var item funnelResponse
		var steps []byte
		if err := rows.Scan(&item.ID, &item.SiteID, &item.Name, &steps); err != nil || json.Unmarshal(steps, &item.Steps) != nil {
			serviceError(w, errors.New("decode funnel"))
			return
		}
		result = append(result, item)
	}
	if err := rows.Err(); err != nil {
		serviceError(w, err)
		return
	}
	writeJSON(w, http.StatusOK, result)
}

func (h *handler) readDefinitionsPrincipal(w http.ResponseWriter, r *http.Request) (authz.Principal, coresites.Site, bool) {
	principal, ok := h.principal(w, r)
	if !ok {
		return authz.Principal{}, coresites.Site{}, false
	}
	siteID, err := parseInt32(r.PathValue("site"))
	if err != nil {
		writeProblem(w, http.StatusBadRequest, "Bad request", "site must be a positive integer")
		return authz.Principal{}, coresites.Site{}, false
	}
	principal, site, ok := h.visibleSite(w, principal, siteID)
	if !ok {
		return authz.Principal{}, coresites.Site{}, false
	}
	if !roleAllowed(principal.Role, h.resourceRoles(r)) ||
		(!authz.Can(principal, authz.ActionStatsRead, siteID) && !authz.Can(principal, authz.ActionGoalsEdit, siteID)) {
		writeProblem(w, http.StatusForbidden, "Forbidden", "the principal cannot perform this action")
		return authz.Principal{}, coresites.Site{}, false
	}
	return principal, site, true
}

func (h *handler) createFunnel(w http.ResponseWriter, r *http.Request) {
	principal, site, ok := h.sitePrincipal(w, r, authz.ActionGoalsEdit, h.resourceRoles(r)...)
	if !ok {
		return
	}
	var input funnelInput
	if !decodeJSON(w, r, &input) {
		return
	}
	steps, ok := validFunnel(input)
	if !ok {
		writeProblem(w, http.StatusBadRequest, "Bad request", "funnel fields are invalid")
		return
	}
	var created funnelResponse
	err := h.deps.Store.Tx(r.Context(), func(tx pgx.Tx) error {
		created.SiteID, created.funnelInput = site.ID, input
		if err := tx.QueryRow(r.Context(), `insert into funnels(site_id,name,steps) values($1,$2,$3) returning id`, site.ID, input.Name, steps).Scan(&created.ID); err != nil {
			return err
		}
		return audit.Write(r.Context(), tx, principal, "funnel.created", "funnel", created.ID, map[string]any{"site_id": site.ID})
	})
	if err != nil {
		serviceError(w, err)
		return
	}
	h.invalidateDefinitions()
	writeJSON(w, http.StatusCreated, created)
}

func (h *handler) updateFunnel(w http.ResponseWriter, r *http.Request) {
	principal, funnel, ok := h.funnelPrincipal(w, r, authz.ActionGoalsEdit)
	if !ok {
		return
	}
	var input funnelInput
	if !decodeJSON(w, r, &input) {
		return
	}
	steps, valid := validFunnel(input)
	if !valid {
		writeProblem(w, http.StatusBadRequest, "Bad request", "funnel fields are invalid")
		return
	}
	err := h.deps.Store.Tx(r.Context(), func(tx pgx.Tx) error {
		if _, err := tx.Exec(r.Context(), `update funnels set name=$2,steps=$3 where id=$1`, funnel.ID, input.Name, steps); err != nil {
			return err
		}
		return audit.Write(r.Context(), tx, principal, "funnel.updated", "funnel", funnel.ID, map[string]any{"site_id": funnel.SiteID})
	})
	if err != nil {
		serviceError(w, err)
		return
	}
	h.invalidateDefinitions()
	funnel.funnelInput = input
	writeJSON(w, http.StatusOK, funnel)
}

func (h *handler) deleteFunnel(w http.ResponseWriter, r *http.Request) {
	principal, funnel, ok := h.funnelPrincipal(w, r, authz.ActionGoalsEdit)
	if !ok {
		return
	}
	err := h.deps.Store.Tx(r.Context(), func(tx pgx.Tx) error {
		if _, err := tx.Exec(r.Context(), `delete from funnels where id=$1`, funnel.ID); err != nil {
			return err
		}
		return audit.Write(r.Context(), tx, principal, "funnel.deleted", "funnel", funnel.ID, map[string]any{"site_id": funnel.SiteID})
	})
	if err != nil {
		serviceError(w, err)
		return
	}
	h.invalidateDefinitions()
	w.WriteHeader(http.StatusNoContent)
}

func (h *handler) funnelPrincipal(w http.ResponseWriter, r *http.Request, action authz.Action) (authz.Principal, funnelResponse, bool) {
	principal, ok := h.principal(w, r)
	if !ok {
		return authz.Principal{}, funnelResponse{}, false
	}
	id, err := parseInt64(r.PathValue("funnel"))
	if err != nil || h.deps.Store == nil {
		writeProblem(w, http.StatusNotFound, "Not found", "funnel not found")
		return authz.Principal{}, funnelResponse{}, false
	}
	var funnel funnelResponse
	var steps []byte
	err = h.deps.Store.Pool.QueryRow(r.Context(), `select id,site_id,name,steps from funnels where id=$1`, id).Scan(&funnel.ID, &funnel.SiteID, &funnel.Name, &steps)
	if errors.Is(err, pgx.ErrNoRows) {
		writeProblem(w, http.StatusNotFound, "Not found", "funnel not found")
		return authz.Principal{}, funnelResponse{}, false
	}
	if err != nil || json.Unmarshal(steps, &funnel.Steps) != nil {
		serviceError(w, err)
		return authz.Principal{}, funnelResponse{}, false
	}
	if _, _, ok := h.visibleSite(w, principal, funnel.SiteID); !ok {
		return authz.Principal{}, funnelResponse{}, false
	}
	if !roleAllowed(principal.Role, h.resourceRoles(r)) || !authz.Can(principal, action, funnel.SiteID) {
		writeProblem(w, http.StatusForbidden, "Forbidden", "the principal cannot perform this action")
		return authz.Principal{}, funnelResponse{}, false
	}
	return principal, funnel, true
}

func validFunnel(input funnelInput) ([]byte, bool) {
	if strings.TrimSpace(input.Name) == "" || len(input.Steps) < 2 {
		return nil, false
	}
	for _, step := range input.Steps {
		if (step.Type != "page" && step.Type != "event") ||
			(step.Match != "exact" && step.Match != "prefix" && step.Match != "wildcard") || strings.TrimSpace(step.Value) == "" {
			return nil, false
		}
	}
	encoded, err := json.Marshal(input.Steps)
	return encoded, err == nil
}

func (h *handler) invalidateDefinitions() {
	if h.deps.Definitions != nil {
		_ = h.deps.Definitions.Invalidate()
	}
}
