package platformhttp

import (
	"errors"
	"net/http"
	"strings"

	"github.com/jackc/pgx/v5"

	"supermetry/internal/appissues"
	"supermetry/internal/appquery"
	"supermetry/internal/appquery/search"
	"supermetry/internal/platform/audit"
	"supermetry/internal/platform/authz"
	coresites "supermetry/internal/sites"
)

// maxBulkIssues is the most issues one bulk action changes.
const maxBulkIssues = 1000

// parseSearch parses a query-bar search. An empty search is nil. A syntax
// error is answered with 400 and its position.
func parseSearch(w http.ResponseWriter, raw string) (*search.Query, bool) {
	if strings.TrimSpace(raw) == "" {
		return nil, true
	}
	q, err := search.Parse(raw)
	if err != nil {
		var pe *search.ParseError
		if errors.As(err, &pe) {
			writeSearchProblem(w, pe)
		} else {
			writeProblem(w, http.StatusBadRequest, "Bad request", "invalid search")
		}
		return nil, false
	}
	return &q, true
}

// memberLookup resolves assigned:<email> to an organisation member with
// access to the project. Viewers get no lookup: they cannot otherwise see
// member emails, and a search must not reveal them.
func (h *handler) memberLookup(r *http.Request, principal authz.Principal, site coresites.Site) func(string) (int64, bool) {
	switch principal.Role {
	case authz.RoleOwner, authz.RoleAdmin, authz.RoleEditor:
	default:
		return nil
	}
	return func(email string) (int64, bool) {
		var id int64
		err := h.deps.Store.Pool.QueryRow(r.Context(), `select u.id from users u join memberships m on m.user_id = u.id
where m.org_id = $1 and lower(u.email) = $2 and (m.site_ids is null or $3 = any(m.site_ids))`, site.OrgID, strings.ToLower(email), site.ID).Scan(&id)
		return id, err == nil
	}
}

// appIssueQuery reads the issue list request shared by the list and the bulk
// action: range and filters, status, kind, q and the query-bar search.
func (h *handler) appIssueQuery(w http.ResponseWriter, r *http.Request, principal authz.Principal, site coresites.Site) (appquery.IssueQuery, bool) {
	var in appquery.IssueQuery
	f, ok := appFilter(w, r, site, h.deps.Now())
	if !ok {
		return in, false
	}
	query := r.URL.Query()
	in = appquery.IssueQuery{Filter: f, Status: query.Get("status"), Kind: query.Get("kind"), Q: query.Get("q"), Sort: query.Get("sort")}
	if len(in.Kind) > 32 || len(in.Q) > maxIssueQuery || strings.ContainsRune(in.Kind, 0) || strings.ContainsRune(in.Q, 0) {
		writeProblem(w, http.StatusBadRequest, "Bad request", "kind or q is too long or invalid")
		return in, false
	}
	if in.Search, ok = parseSearch(w, query.Get("query")); !ok {
		return in, false
	}
	in.Me = principal.UserID
	in.LookupMember = h.memberLookup(r, principal, site)
	return in, true
}

type appBulkBody struct {
	IDs    []int64          `json:"ids"`
	Query  *string          `json:"query"`
	Change appissues.Change `json:"change"`
}

// bulkAppIssues applies one change to up to 1,000 issues named by id, or to
// the issues a search matches. Ids that are not issues of the path's project
// are reported as skipped and left alone.
func (h *handler) bulkAppIssues(w http.ResponseWriter, r *http.Request) {
	principal, site, ok := h.appHealthSite(w, r, authz.ActionSiteEdit, authz.RoleOwner, authz.RoleAdmin, authz.RoleEditor)
	if !ok {
		return
	}
	var in appBulkBody
	if !decodeJSON(w, r, &in) {
		return
	}
	if (in.IDs == nil) == (in.Query == nil) {
		writeProblem(w, http.StatusBadRequest, "Bad request", "give either ids or query")
		return
	}
	ids := in.IDs
	if in.Query == nil {
		if len(ids) == 0 || len(ids) > maxBulkIssues {
			writeProblem(w, http.StatusBadRequest, "Bad request", "ids must hold 1 to 1000 issue ids")
			return
		}
		for _, id := range ids {
			if id < 1 {
				writeProblem(w, http.StatusBadRequest, "Bad request", "ids must be positive")
				return
			}
		}
	} else {
		iq, ok := h.appIssueQuery(w, r, principal, site)
		if !ok {
			return
		}
		if iq.Search, ok = parseSearch(w, *in.Query); !ok {
			return
		}
		var err error
		if ids, err = h.appQuerier().MatchIssues(r.Context(), appScope(site), iq, maxBulkIssues); err != nil {
			appQueryError(w, err)
			return
		}
		if len(ids) > maxBulkIssues {
			writeProblem(w, http.StatusBadRequest, "Bad request", "the search matches more than 1000 issues; narrow it")
			return
		}
		if len(ids) == 0 {
			writeJSON(w, http.StatusOK, appissues.Result{Updated: []int64{}, Skipped: []int64{}})
			return
		}
	}
	var res appissues.Result
	err := h.deps.Store.Tx(r.Context(), func(tx pgx.Tx) error {
		var err error
		if res, err = h.appIssues().UpdateTx(r.Context(), tx, site.ID, ids, appIssueActor(principal), in.Change); err != nil {
			return err
		}
		return audit.Write(r.Context(), tx, principal, "app.issues_bulk_updated", "site", int64(site.ID),
			map[string]any{"site_id": site.ID, "updated": len(res.Updated), "skipped": len(res.Skipped), "fields": res.Fields})
	})
	if err != nil {
		appIssueError(w, err)
		return
	}
	writeJSON(w, http.StatusOK, res)
}

type appViewBody struct {
	Name   string `json:"name"`
	Query  string `json:"query"`
	Shared bool   `json:"shared"`
}

func viewError(w http.ResponseWriter, err error) {
	if errors.Is(err, appissues.ErrViewExists) {
		writeProblem(w, http.StatusConflict, "Conflict", err.Error())
		return
	}
	appIssueError(w, err)
}

func (h *handler) listAppViews(w http.ResponseWriter, r *http.Request) {
	principal, site, ok := h.appHealthSite(w, r, authz.ActionStatsRead, authz.RoleOwner, authz.RoleAdmin, authz.RoleEditor, authz.RoleViewer)
	if !ok {
		return
	}
	views, err := h.appIssues().Views(r.Context(), site.ID, principal.UserID)
	if err != nil {
		viewError(w, err)
		return
	}
	writeJSON(w, http.StatusOK, map[string]any{"views": views})
}

func (h *handler) createAppView(w http.ResponseWriter, r *http.Request) {
	principal, site, ok := h.appHealthSite(w, r, authz.ActionSiteEdit, authz.RoleOwner, authz.RoleAdmin, authz.RoleEditor)
	if !ok {
		return
	}
	var in appViewBody
	if !decodeJSON(w, r, &in) {
		return
	}
	v, err := h.appIssues().CreateView(r.Context(), site.ID, appIssueActor(principal), in.Name, in.Query, in.Shared)
	if err != nil {
		viewError(w, err)
		return
	}
	writeJSON(w, http.StatusCreated, v)
}

func (h *handler) updateAppView(w http.ResponseWriter, r *http.Request) {
	principal, site, ok := h.appHealthSite(w, r, authz.ActionSiteEdit, authz.RoleOwner, authz.RoleAdmin, authz.RoleEditor)
	if !ok {
		return
	}
	id, err := parseInt64(r.PathValue("view"))
	if err != nil {
		writeProblem(w, http.StatusNotFound, "Not found", "view not found")
		return
	}
	var in appissues.ViewChange
	if !decodeJSON(w, r, &in) {
		return
	}
	v, err := h.appIssues().UpdateView(r.Context(), site.ID, id, appIssueActor(principal), in)
	if err != nil {
		viewError(w, err)
		return
	}
	writeJSON(w, http.StatusOK, v)
}

func (h *handler) deleteAppView(w http.ResponseWriter, r *http.Request) {
	principal, site, ok := h.appHealthSite(w, r, authz.ActionSiteEdit, authz.RoleOwner, authz.RoleAdmin, authz.RoleEditor)
	if !ok {
		return
	}
	id, err := parseInt64(r.PathValue("view"))
	if err != nil {
		writeProblem(w, http.StatusNotFound, "Not found", "view not found")
		return
	}
	if err := h.appIssues().DeleteView(r.Context(), site.ID, id, appIssueActor(principal)); err != nil {
		viewError(w, err)
		return
	}
	w.WriteHeader(http.StatusNoContent)
}
