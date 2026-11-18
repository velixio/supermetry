package platformhttp

import (
	"context"
	"errors"
	"fmt"
	"net/http"
	"strconv"
	"time"

	openapi_types "github.com/oapi-codegen/runtime/types"

	"supermetry/internal/api"
	"supermetry/internal/imports"
	"supermetry/internal/platform/authz"
)

// maxImportCompareDays bounds one comparison: the native side is one query
// with a row per day.
const maxImportCompareDays = 400

// importCompareSource reads imported daily totals; the import store implements it.
type importCompareSource interface {
	ImportedDaily(ctx context.Context, siteID int32, from, to time.Time, importID int64) ([]imports.DayTotals, error)
}

type importCompareResponse struct {
	From string                  `json:"from"`
	To   string                  `json:"to"`
	Days []imports.DayComparison `json:"days"`
}

// compareImports sets imported daily totals against the data Supermetry
// collected itself, for every imported day in the range. It powers the review
// step (one import) and the switch-over helper (the newest import per day).
// Query: from, to (YYYY-MM-DD) and optionally import (an import id of this site).
func (h *handler) compareImports(w http.ResponseWriter, r *http.Request) {
	_, site, ok := h.sitePrincipal(w, r, authz.ActionSiteEdit, authz.RoleOwner, authz.RoleAdmin)
	if !ok {
		return
	}
	source, available := h.deps.Imports.(importCompareSource)
	if h.deps.Imports == nil || !available || h.deps.Query == nil {
		unavailable(w, "import comparison")
		return
	}
	from, to, err := exportRange(r)
	if err != nil {
		writeProblem(w, http.StatusBadRequest, "Bad request", err.Error())
		return
	}
	if days := int(to.Sub(from).Hours()/24) + 1; days > maxImportCompareDays {
		writeProblem(w, http.StatusBadRequest, "Bad request", fmt.Sprintf("a comparison covers at most %d days", maxImportCompareDays))
		return
	}
	var importID int64
	if raw := r.URL.Query().Get("import"); raw != "" {
		importID, err = strconv.ParseInt(raw, 10, 64)
		if err != nil || importID < 1 {
			writeProblem(w, http.StatusBadRequest, "Bad request", "import must be a positive integer")
			return
		}
		item, err := h.deps.Imports.Get(r.Context(), importID)
		if errors.Is(err, imports.ErrNotFound) || (err == nil && item.SiteID != site.ID) {
			writeProblem(w, http.StatusNotFound, "Not found", "import not found")
			return
		}
		if err != nil {
			serviceError(w, err)
			return
		}
	}
	imported, err := source.ImportedDaily(r.Context(), site.ID, from, to, importID)
	if err != nil {
		serviceError(w, err)
		return
	}
	native, err := h.nativeDailyTotals(r.Context(), site.ID, from, to)
	if err != nil {
		serviceError(w, err)
		return
	}
	days := imports.Compare(imported, native)
	if days == nil {
		days = []imports.DayComparison{}
	}
	writeJSON(w, http.StatusOK, importCompareResponse{From: from.Format(time.DateOnly), To: to.Format(time.DateOnly), Days: days})
}

// nativeDailyTotals asks the query engine for per-day totals of collected data
// only (imported rows excluded), so live and closed days are both covered.
func (h *handler) nativeDailyTotals(ctx context.Context, siteID int32, from, to time.Time) ([]imports.DayTotals, error) {
	includeImported := false
	dimensions := []string{"time"}
	limit := maxImportCompareDays + 1
	response, err := h.deps.Query.Query(ctx, api.QueryRequest{
		SiteId:          siteID,
		DateRange:       &api.DateRange{From: openapi_types.Date{Time: from}, To: openapi_types.Date{Time: to}},
		Metrics:         []api.Metric{api.Visitors, api.Visits, api.Pageviews},
		Dimensions:      &dimensions,
		IncludeImported: &includeImported,
		Limit:           &limit,
	})
	if err != nil {
		return nil, err
	}
	result := make([]imports.DayTotals, 0, len(response.Rows))
	for _, row := range response.Rows {
		label, _ := row["time"].(string)
		day, err := time.Parse(time.DateOnly, label)
		if err != nil {
			continue
		}
		result = append(result, imports.DayTotals{
			Day: day, Visitors: rowInt(row["visitors"]), Visits: rowInt(row["visits"]), Pageviews: rowInt(row["pageviews"]),
		})
	}
	return result, nil
}

func rowInt(value any) int64 {
	switch typed := value.(type) {
	case float64:
		return int64(typed + 0.5)
	case int64:
		return typed
	case int:
		return int64(typed)
	}
	return 0
}
