package api

import (
	"net/http"
	"net/http/httptest"
	"testing"
)

func TestOpenCountersDisabledByDefault(t *testing.T) {
	router := NewRouter(Deps{})
	response := httptest.NewRecorder()
	router.ServeHTTP(response, httptest.NewRequest(http.MethodGet, "/c/github.com/a.svg", nil))
	if response.Code != http.StatusNotFound {
		t.Fatalf("status = %d, want 404", response.Code)
	}
}
