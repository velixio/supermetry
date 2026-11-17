package platformhttp

import (
	"errors"
	"net/http"
	"strconv"
	"strings"
	"time"

	"github.com/jackc/pgx/v5"

	"supermetry/internal/appissues"
	"supermetry/internal/platform/audit"
	"supermetry/internal/platform/authz"
)

func (h *handler) appIssues() *appissues.Service {
	return appissues.New(h.deps.Store.Pool, h.deps.Now)
}

func appIssueError(w http.ResponseWriter, err error) {
	switch {
	case errors.Is(err, appissues.ErrNotFound):
		writeProblem(w, http.StatusNotFound, "Not found", "not found")
	case errors.Is(err, appissues.ErrForbidden):
		writeProblem(w, http.StatusForbidden, "Forbidden", err.Error())
	case errors.Is(err, appissues.ErrInvalidAssignee):
		writeProblem(w, http.StatusBadRequest, "Bad request", err.Error())
	case errors.Is(err, appissues.ErrInvalid):
		writeProblem(w, http.StatusBadRequest, "Bad request", strings.TrimPrefix(err.Error(), appissues.ErrInvalid.Error()+": "))
	default:
		appQueryError(w, err)
	}
}

// appIssueActor maps a principal to the service's actor; owners and admins may
// change other people's comments.
func appIssueActor(p authz.Principal) appissues.Actor {
	return appissues.Actor{UserID: p.UserID, Admin: p.Role == authz.RoleOwner || p.Role == authz.RoleAdmin}
}

type appIssueChangeResponse struct {
	appissues.IssueState
	Updated []int64 `json:"updated"`
	Skipped []int64 `json:"skipped"`
}

// updateAppIssue changes one issue's workflow state (status, assignee,
// priority, snooze, archive, GitHub link). It also serves the older status-only
// body: {status, resolved_in_release}.
func (h *handler) updateAppIssue(w http.ResponseWriter, r *http.Request) {
	principal, site, ok := h.appHealthSite(w, r, authz.ActionSiteEdit, authz.RoleOwner, authz.RoleAdmin, authz.RoleEditor)
	if !ok {
		return
	}
	id, err := parseInt64(r.PathValue("issue"))
	if err != nil {
		writeProblem(w, http.StatusNotFound, "Not found", "issue not found")
		return
	}
	var in appissues.Change
	if !decodeJSON(w, r, &in) {
		return
	}
	var res appissues.Result
	err = h.deps.Store.Tx(r.Context(), func(tx pgx.Tx) error {
		var err error
		res, err = h.appIssues().UpdateTx(r.Context(), tx, site.ID, []int64{id}, appIssueActor(principal), in)
		if err != nil {
			return err
		}
		if len(res.Updated) == 0 {
			return appissues.ErrNotFound
		}
		state := res.Issues[0]
		other := []string{}
		for _, f := range res.Fields {
			if f == "status" {
				meta := map[string]any{"site_id": site.ID, "from": state.Previous, "to": state.Status, "resolved_next": state.ResolvedNext}
				if state.ResolvedInRelease != nil {
					meta["resolved_in_release"] = *state.ResolvedInRelease
				}
				if err := audit.Write(r.Context(), tx, principal, "app.issue_status_changed", "app_issue", id, meta); err != nil {
					return err
				}
				continue
			}
			other = append(other, f)
		}
		if len(other) > 0 {
			return audit.Write(r.Context(), tx, principal, "app.issue_updated", "app_issue", id, map[string]any{"site_id": site.ID, "fields": other})
		}
		return nil
	})
	if err != nil {
		appIssueError(w, err)
		return
	}
	writeJSON(w, http.StatusOK, appIssueChangeResponse{IssueState: res.Issues[0], Updated: res.Updated, Skipped: res.Skipped})
}

type appCommentBody struct {
	Body string `json:"body"`
}

func (h *handler) listAppIssueComments(w http.ResponseWriter, r *http.Request) {
	site, ok := h.appReadSite(w, r)
	if !ok {
		return
	}
	id, err := parseInt64(r.PathValue("issue"))
	if err != nil {
		writeProblem(w, http.StatusNotFound, "Not found", "issue not found")
		return
	}
	list, err := h.appIssues().Comments(r.Context(), site.ID, id)
	if err != nil {
		appIssueError(w, err)
		return
	}
	writeJSON(w, http.StatusOK, map[string]any{"comments": list})
}

func (h *handler) createAppIssueComment(w http.ResponseWriter, r *http.Request) {
	principal, site, ok := h.appHealthSite(w, r, authz.ActionSiteEdit, authz.RoleOwner, authz.RoleAdmin, authz.RoleEditor)
	if !ok {
		return
	}
	id, err := parseInt64(r.PathValue("issue"))
	if err != nil {
		writeProblem(w, http.StatusNotFound, "Not found", "issue not found")
		return
	}
	var in appCommentBody
	if !decodeJSON(w, r, &in) {
		return
	}
	c, err := h.appIssues().Comment(r.Context(), site.ID, id, appIssueActor(principal), in.Body)
	if err != nil {
		appIssueError(w, err)
		return
	}
	writeJSON(w, http.StatusCreated, c)
}

func (h *handler) editAppIssueComment(w http.ResponseWriter, r *http.Request) {
	principal, site, ok := h.appHealthSite(w, r, authz.ActionSiteEdit, authz.RoleOwner, authz.RoleAdmin, authz.RoleEditor)
	if !ok {
		return
	}
	id, err := parseInt64(r.PathValue("comment"))
	if err != nil {
		writeProblem(w, http.StatusNotFound, "Not found", "comment not found")
		return
	}
	var in appCommentBody
	if !decodeJSON(w, r, &in) {
		return
	}
	c, err := h.appIssues().EditComment(r.Context(), site.ID, id, appIssueActor(principal), in.Body)
	if err != nil {
		appIssueError(w, err)
		return
	}
	writeJSON(w, http.StatusOK, c)
}

func (h *handler) deleteAppIssueComment(w http.ResponseWriter, r *http.Request) {
	principal, site, ok := h.appHealthSite(w, r, authz.ActionSiteEdit, authz.RoleOwner, authz.RoleAdmin, authz.RoleEditor)
	if !ok {
		return
	}
	id, err := parseInt64(r.PathValue("comment"))
	if err != nil {
		writeProblem(w, http.StatusNotFound, "Not found", "comment not found")
		return
	}
	if err := h.appIssues().DeleteComment(r.Context(), site.ID, id, appIssueActor(principal)); err != nil {
		appIssueError(w, err)
		return
	}
	w.WriteHeader(http.StatusNoContent)
}

func (h *handler) listAppIssueActivity(w http.ResponseWriter, r *http.Request) {
	site, ok := h.appReadSite(w, r)
	if !ok {
		return
	}
	id, err := parseInt64(r.PathValue("issue"))
	if err != nil {
		writeProblem(w, http.StatusNotFound, "Not found", "issue not found")
		return
	}
	limit := 0
	if v := r.URL.Query().Get("limit"); v != "" {
		if limit, err = strconv.Atoi(v); err != nil || limit < 1 || limit > 200 {
			writeProblem(w, http.StatusBadRequest, "Bad request", "limit must be between 1 and 200")
			return
		}
	}
	var before time.Time
	if v := r.URL.Query().Get("before"); v != "" {
		if before, err = time.Parse(time.RFC3339Nano, v); err != nil {
			writeProblem(w, http.StatusBadRequest, "Bad request", "before must be an RFC 3339 time")
			return
		}
	}
	items, err := h.appIssues().Timeline(r.Context(), site.ID, id, limit, before)
	if err != nil {
		appIssueError(w, err)
		return
	}
	writeJSON(w, http.StatusOK, map[string]any{"activity": items})
}

// listAppAssignees lists the organisation members who can be assigned issues
// of the project: those with access to it. Names only, no emails.
func (h *handler) listAppAssignees(w http.ResponseWriter, r *http.Request) {
	site, ok := h.appReadSite(w, r)
	if !ok {
		return
	}
	rows, err := h.deps.Store.Pool.Query(r.Context(), `select u.id, u.name from memberships m join users u on u.id = m.user_id
where m.org_id = $1 and (m.site_ids is null or $2 = any(m.site_ids)) order by lower(u.name), u.id`, site.OrgID, site.ID)
	if err != nil {
		serviceError(w, err)
		return
	}
	defer rows.Close()
	type member struct {
		UserID int64  `json:"user_id"`
		Name   string `json:"name"`
	}
	out := []member{}
	for rows.Next() {
		var m member
		if err := rows.Scan(&m.UserID, &m.Name); err != nil {
			serviceError(w, err)
			return
		}
		out = append(out, m)
	}
	if err := rows.Err(); err != nil {
		serviceError(w, err)
		return
	}
	writeJSON(w, http.StatusOK, map[string]any{"members": out})
}
