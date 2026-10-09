package platformhttp

import (
	"net/http"
	"strings"

	"supermetry/internal/platform/authz"
)

// authorizeSensitiveExport is called only after tenant/site authorization.
// Profile and recording reads are independent grants; requesting a portable
// representation never substitutes an export grant for the relevant read grant.
func authorizeSensitiveExport(w http.ResponseWriter, r *http.Request, principal authz.Principal, siteID int32, reads ...authz.Action) bool {
	export := r.URL.Query().Get("format") == "csv" || r.URL.Query().Get("limit") == "all"
	for _, value := range strings.Split(r.Header.Get("Accept"), ",") {
		export = export || strings.TrimSpace(strings.SplitN(value, ";", 2)[0]) == "text/csv"
	}
	if !export {
		return true
	}
	actions := append([]authz.Action{authz.ActionStatsRead, authz.ActionDataExport}, reads...)
	for _, action := range actions {
		if !authz.Can(principal, action, siteID) {
			writeProblem(w, http.StatusForbidden, "Forbidden", "the principal cannot export this data")
			return false
		}
	}
	return true
}
