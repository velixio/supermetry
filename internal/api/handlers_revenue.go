package api

import (
	"context"
	"encoding/csv"
	"errors"
	"net/http"
	"strconv"
	"strings"
	"time"

	"supermetry/internal/csvsafe"
)

var (
	// ErrRevenueInput marks a revenue request the caller can correct.
	ErrRevenueInput = errors.New("invalid revenue report request")
	// ErrMixedRevenueCurrency means the period holds orders in several currencies.
	ErrMixedRevenueCurrency = errors.New("revenue report refused: orders use more than one currency")
)

type revenueService interface {
	RevenueReport(ctx context.Context, siteID int32, req RevenueReportRequest) (RevenueReport, error)
	RevenueCustomers(ctx context.Context, siteID int32, from, to time.Time, search string, limit int) ([]RevenueCustomer, error)
}

// RevenueReportRequest is the parsed query of a revenue report.
type RevenueReportRequest struct {
	From, To     time.Time
	Dimension    string
	Attribution  string
	LookbackDays int
	Limit        int
}

func parseSiteAndRange(w http.ResponseWriter, r *http.Request) (int32, time.Time, time.Time, bool) {
	siteValue, err := strconv.ParseInt(r.PathValue("site"), 10, 32)
	if err != nil || siteValue < 1 {
		writeProblem(w, badRequest("site must be a positive integer"))
		return 0, time.Time{}, time.Time{}, false
	}
	from, errFrom := time.Parse(time.DateOnly, r.URL.Query().Get("from"))
	to, errTo := time.Parse(time.DateOnly, r.URL.Query().Get("to"))
	if errFrom != nil || errTo != nil {
		writeProblem(w, badRequest("from and to must be valid dates"))
		return 0, time.Time{}, time.Time{}, false
	}
	return int32(siteValue), from, to, true
}

func optionalInt(w http.ResponseWriter, r *http.Request, name string) (int, bool) {
	raw := r.URL.Query().Get(name)
	if raw == "" {
		return 0, true
	}
	value, err := strconv.Atoi(raw)
	if err != nil {
		writeProblem(w, badRequest(name+" must be an integer"))
		return 0, false
	}
	return value, true
}

func writeRevenueError(w http.ResponseWriter, err error) {
	switch {
	case errors.Is(err, ErrRevenueInput):
		writeProblem(w, badRequest(err.Error()))
	case errors.Is(err, ErrMixedRevenueCurrency):
		writeProblem(w, newProblem(http.StatusConflict, "https://supermetry.com/problems/currency-conflict", "Currency conflict", err.Error()))
	default:
		writeProblem(w, internalProblem())
	}
}

func (h *handlers) revenueReport(w http.ResponseWriter, r *http.Request) {
	siteID, from, to, ok := parseSiteAndRange(w, r)
	if !ok || !authorizeRequest(w, r, siteID, "stats:read") {
		return
	}
	if h.revenue == nil {
		writeProblem(w, internalProblem())
		return
	}
	lookback, ok := optionalInt(w, r, "lookback_days")
	if !ok {
		return
	}
	limit, ok := optionalInt(w, r, "limit")
	if !ok {
		return
	}
	report, err := h.revenue.RevenueReport(r.Context(), siteID, RevenueReportRequest{
		From: from, To: to, Dimension: r.URL.Query().Get("breakdown"), Attribution: r.URL.Query().Get("attribution"), LookbackDays: lookback, Limit: limit,
	})
	if err != nil {
		writeRevenueError(w, err)
		return
	}
	if report.Rows == nil {
		report.Rows = []RevenueReportRow{}
	}
	if wantsCSV(r) {
		writeRevenueCSV(w, report)
		return
	}
	writeJSON(w, http.StatusOK, report)
}

func (h *handlers) revenueCustomers(w http.ResponseWriter, r *http.Request) {
	siteID, from, to, ok := parseSiteAndRange(w, r)
	if !ok || !authorizeRequest(w, r, siteID, "profiles:read") {
		return
	}
	if h.revenue == nil {
		writeProblem(w, internalProblem())
		return
	}
	limit, ok := optionalInt(w, r, "limit")
	if !ok {
		return
	}
	customers, err := h.revenue.RevenueCustomers(r.Context(), siteID, from, to, r.URL.Query().Get("search"), limit)
	if err != nil {
		if !errors.Is(err, ErrRevenueInput) {
			// Anonymous mode or profiles disabled: the feature is unavailable, not a server fault.
			if strings.Contains(err.Error(), "identified tracking") {
				writeProblem(w, forbidden(err.Error()))
				return
			}
		}
		writeRevenueError(w, err)
		return
	}
	if customers == nil {
		customers = []RevenueCustomer{}
	}
	writeJSON(w, http.StatusOK, RevenueCustomersResponse{Data: customers})
}

func writeRevenueCSV(w http.ResponseWriter, report RevenueReport) {
	w.Header().Set("Content-Type", "text/csv; charset=utf-8")
	w.Header().Set("Content-Disposition", `attachment; filename="revenue-`+report.Breakdown+`.csv"`)
	writer := csv.NewWriter(w)
	_ = writer.Write([]string{report.Breakdown, "orders", "revenue", "average_order_value", "unique_customers", "repeat_customers", "repeat_rate"})
	for _, row := range report.Rows {
		_ = writer.Write([]string{
			csvsafe.Cell(row.Key), strconv.FormatInt(row.Orders, 10), formatMoney(row.Revenue), formatMoney(row.AverageOrderValue),
			optionalCount(row.UniqueCustomers), optionalCount(row.RepeatCustomers), optionalPercent(row.RepeatRate),
		})
	}
	writer.Flush()
}

func formatMoney(value float64) string { return strconv.FormatFloat(value, 'f', 2, 64) }

func optionalCount(value *int64) string {
	if value == nil {
		return ""
	}
	return strconv.FormatInt(*value, 10)
}

func optionalPercent(value *float64) string {
	if value == nil {
		return ""
	}
	return strconv.FormatFloat(*value, 'f', 1, 64)
}
