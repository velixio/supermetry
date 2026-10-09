package api

import (
	"context"
	"encoding/base64"
	"errors"
	"net/http"
	"strconv"
	"strings"

	"panel/internal/privacy"
)

// ErrAnonymousHistory marks reads that could expose historical identifiers.
var ErrAnonymousHistory = errors.New("anonymous tracking policy restricts historical identity-bearing analytics")

// ValidateAnonymousQuery allows only canonical finite-domain analytics. Free-form
// filters can probe historical identities even in count-only responses.
func ValidateAnonymousQuery(request QueryRequest) error {
	if request.Filters != nil {
		for _, filter := range *request.Filters {
			if filter.Operator != Is && filter.Operator != IsNot {
				return ErrAnonymousHistory
			}
			if !privacy.AnonymousDimensionAllowed(filter.Dimension) {
				return ErrAnonymousHistory
			}
			for _, value := range filter.Values {
				if _, valid := privacy.CanonicalDimensionValue(filter.Dimension, value); !valid || value == "" {
					return ErrAnonymousHistory
				}
			}
		}
	}
	if request.Dimensions != nil {
		for _, dimension := range *request.Dimensions {
			if !privacy.AnonymousDimensionAllowed(dimension) {
				return ErrAnonymousHistory
			}
		}
	}
	if request.Cursor != nil && !anonymousCursor(*request.Cursor) {
		return ErrAnonymousHistory
	}
	return nil
}

func anonymousCursor(value string) bool {
	decoded, err := base64.RawURLEncoding.DecodeString(value)
	if err != nil {
		return false
	}
	offset, err := strconv.Atoi(string(decoded))
	return err == nil && offset >= 0 && strconv.Itoa(offset) == string(decoded)
}

// SanitizeAnonymousResponse is defense in depth for alternate query services.
// The SQL engine coalesces unknown labels before metric finalization and cache.
func SanitizeAnonymousResponse(response QueryResponse, request QueryRequest) QueryResponse {
	dimensions := []string{}
	if request.Dimensions != nil {
		dimensions = *request.Dimensions
	}
	cleanRows := func(rows []map[string]any) []map[string]any {
		clean := make([]map[string]any, 0, len(rows))
		for _, row := range rows {
			item := make(map[string]any, len(dimensions)+len(request.Metrics))
			for _, dimension := range dimensions {
				value, _ := row[dimension].(string)
				item[dimension], _ = privacy.CanonicalDimensionValue(dimension, value)
			}
			for _, metric := range request.Metrics {
				item[string(metric)] = number(row[string(metric)])
			}
			clean = append(clean, item)
		}
		return clean
	}
	cleanTotals := func(totals map[string]float64) map[string]float64 {
		clean := make(map[string]float64, len(request.Metrics))
		for _, metric := range request.Metrics {
			clean[string(metric)] = totals[string(metric)]
		}
		return clean
	}
	response.Totals = cleanTotals(response.Totals)
	sources := make([]string, 0, len(response.Meta.SourcesUsed))
	for _, source := range response.Meta.SourcesUsed {
		switch source {
		case "rollup", "visitor_day", "cube_day", "closed", "hot", "imported":
			sources = append(sources, source)
		}
	}
	response.Meta.SourcesUsed = sources
	response.Rows = cleanRows(response.Rows)
	response.Columns = append([]string(nil), dimensions...)
	for _, metric := range request.Metrics {
		response.Columns = append(response.Columns, string(metric))
	}
	if response.Comparison != nil {
		comparison := *response.Comparison
		comparison.Rows = cleanRows(comparison.Rows)
		comparison.Totals = cleanTotals(comparison.Totals)
		response.Comparison = &comparison
	}
	if response.NextCursor != nil && !anonymousCursor(*response.NextCursor) {
		response.NextCursor = nil
	}
	return response
}

type anonymousQueries struct{ queryService }

func (q anonymousQueries) Query(ctx context.Context, request QueryRequest) (QueryResponse, error) {
	if err := ValidateAnonymousQuery(request); err != nil {
		return QueryResponse{}, err
	}
	response, err := q.queryService.Query(ctx, request)
	return SanitizeAnonymousResponse(response, request), err
}
func (q anonymousQueries) Funnel(context.Context, FunnelRequest) (FunnelResponse, error) {
	return FunnelResponse{}, ErrAnonymousHistory
}
func (q anonymousQueries) Journey(context.Context, JourneyRequest) (JourneyResponse, error) {
	return JourneyResponse{}, ErrAnonymousHistory
}
func (q anonymousQueries) Retention(context.Context, RetentionRequest) (RetentionResponse, error) {
	return RetentionResponse{}, ErrAnonymousHistory
}

// trackingReads runs after authentication. Platform handlers retain their own
// policy checks, including the separate erasure authorization path.
func trackingReads(mode privacy.Mode, next http.Handler) http.Handler {
	return http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		if mode != privacy.Identified {
			path := strings.TrimPrefix(strings.TrimPrefix(r.URL.Path, publicPrefix), privatePrefix)
			allowed := path == "/query" || strings.HasPrefix(path, "/stats/") || path == "/realtime/summary" || path == "/health" || path == "/mcp"
			if !allowed {
				writeProblem(w, forbidden(ErrAnonymousHistory.Error()))
				return
			}
		}
		next.ServeHTTP(w, r)
	})
}
