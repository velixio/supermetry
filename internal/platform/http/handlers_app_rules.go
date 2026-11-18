package platformhttp

import (
	"errors"
	"fmt"
	"log/slog"
	"net/http"

	"github.com/jackc/pgx/v5"

	"supermetry/internal/apprules"
	"supermetry/internal/platform/audit"
	"supermetry/internal/platform/authz"
)

// appRulesBody is both the request and the response of the rules endpoints.
type appRulesBody struct {
	Ownership []apprules.OwnershipRule `json:"ownership"`
	Grouping  []apprules.GroupingRule  `json:"grouping"`
}

func (h *handler) getAppRules(w http.ResponseWriter, r *http.Request) {
	site, ok := h.appReadSite(w, r)
	if !ok {
		return
	}
	own, grp, err := h.deps.Store.AppRulesForSite(r.Context(), site.ID)
	if err != nil {
		serviceError(w, err)
		return
	}
	writeJSON(w, http.StatusOK, appRulesBody{Ownership: own, Grouping: grp})
}

func (h *handler) replaceAppRules(w http.ResponseWriter, r *http.Request) {
	principal, site, ok := h.appHealthSite(w, r, authz.ActionSiteEdit, authz.RoleOwner, authz.RoleAdmin, authz.RoleEditor)
	if !ok {
		return
	}
	var in appRulesBody
	if !decodeJSON(w, r, &in) {
		return
	}
	if in.Ownership == nil {
		in.Ownership = []apprules.OwnershipRule{}
	}
	if in.Grouping == nil {
		in.Grouping = []apprules.GroupingRule{}
	}
	var re *apprules.RuleError
	if _, err := apprules.CompileOwnership(in.Ownership); err != nil {
		if errors.As(err, &re) {
			err = fmt.Errorf("ownership[%d]: %s", re.Index, re.Msg)
		}
		writeProblem(w, http.StatusBadRequest, "Bad request", err.Error())
		return
	}
	if _, err := apprules.CompileGrouping(in.Grouping); err != nil {
		if errors.As(err, &re) {
			err = fmt.Errorf("grouping[%d]: %s", re.Index, re.Msg)
		}
		writeProblem(w, http.StatusBadRequest, "Bad request", err.Error())
		return
	}
	if len(in.Ownership) > 0 {
		ids := make([]int64, len(in.Ownership))
		for i, rule := range in.Ownership {
			ids[i] = rule.OwnerUserID
		}
		members, err := h.deps.Store.OwnersInSite(r.Context(), site.ID, ids)
		if err != nil {
			serviceError(w, err)
			return
		}
		for i, rule := range in.Ownership {
			if !members[rule.OwnerUserID] {
				writeProblem(w, http.StatusBadRequest, "Bad request",
					fmt.Sprintf("ownership[%d]: owner must be a member of the organisation with access to this project", i))
				return
			}
		}
	}
	err := h.deps.Store.Tx(r.Context(), func(tx pgx.Tx) error {
		if err := h.deps.Store.ReplaceAppRules(r.Context(), tx, site.ID, in.Ownership, in.Grouping); err != nil {
			return err
		}
		return audit.Write(r.Context(), tx, principal, "site.app_rules_changed", "site", int64(site.ID),
			map[string]any{"ownership": len(in.Ownership), "grouping": len(in.Grouping)})
	})
	if err != nil {
		serviceError(w, err)
		return
	}
	if inv, ok := h.deps.Sites.(interface{ Invalidate() error }); ok {
		if err := inv.Invalidate(); err != nil {
			slog.Error("site registry refresh failed after app rules change", "site_id", site.ID, "error", err)
		}
	}
	writeJSON(w, http.StatusOK, in)
}
