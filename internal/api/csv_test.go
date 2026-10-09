package api

import (
	"context"
	"net/http"
	"net/http/httptest"
	"testing"
)

type csvQueryStub struct {
	queryService
	response QueryResponse
	err      error
}

func (q csvQueryStub) Query(context.Context, QueryRequest) (QueryResponse, error) {
	return q.response, q.err
}

func TestCSVVerifiedScopeHeaders(t *testing.T) {
	for _, tc := range []struct {
		name                string
		streaming, verified bool
	}{
		{"verified direct", false, true}, {"verified streaming", true, true},
		{"ordinary direct", false, false}, {"ordinary streaming", true, false},
	} {
		t.Run(tc.name, func(t *testing.T) {
			response := QueryResponse{Columns: []string{"page", "visitors"}, Rows: []map[string]any{{"page": "/public", "visitors": 2}}}
			if tc.verified {
				response.Meta.Privacy = &QueryPrivacy{View: "verified_paths", Coverage: "verified_only", PathSanitizerVersion: 1, PolicyId: "verified-policy", UnverifiedExcluded: true, ImportsExcluded: true}
			}
			w := httptest.NewRecorder()
			if tc.streaming {
				streamQueryCSV(w, httptest.NewRequest("GET", "/query", nil), csvQueryStub{response: response}, QueryRequest{}, "site-query.csv")
			} else {
				writeCSV(w, response, "site-query.csv")
			}
			result := w.Result() // Snapshot headers at the first body write/flush.
			defer result.Body.Close()
			wantFilename := `attachment; filename="site-query.csv"`
			if tc.verified {
				wantFilename = `attachment; filename="site-query-verified-only.csv"`
				for key, want := range map[string]string{
					"X-Supermetry-Privacy-Scope":                  "verified-only",
					"X-Supermetry-Privacy-Path-Sanitizer-Version": "1",
					"X-Supermetry-Privacy-Policy-Id":              "verified-policy",
					"X-Supermetry-Privacy-Unverified-Excluded":    "true",
					"X-Supermetry-Privacy-Imports-Excluded":       "true",
				} {
					if got := result.Header.Get(key); got != want {
						t.Errorf("%s=%q want %q", key, got, want)
					}
				}
			} else if result.Header.Get("X-Supermetry-Privacy-Scope") != "" {
				t.Fatal("ordinary CSV marked verified")
			}
			if got := result.Header.Get("Content-Disposition"); got != wantFilename {
				t.Errorf("filename=%q want %q", got, wantFilename)
			}
			if got := w.Body.String(); got != "page,visitors\r\n/public,2\r\n" {
				t.Errorf("CSV body=%q", got)
			}
		})
	}
}

func TestCSVFailedQueryHasNoSuccessScope(t *testing.T) {
	w := httptest.NewRecorder()
	streamQueryCSV(w, httptest.NewRequest("GET", "/query", nil), csvQueryStub{err: ErrAnonymousHistory}, QueryRequest{}, "site-query.csv")
	if w.Code != http.StatusForbidden {
		t.Fatalf("status=%d body=%s", w.Code, w.Body.String())
	}
	if w.Header().Get("X-Supermetry-Privacy-Scope") != "" || w.Header().Get("Content-Disposition") != "" {
		t.Fatalf("failed query has export headers: %v", w.Header())
	}
}
