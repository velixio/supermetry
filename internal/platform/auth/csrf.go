package auth

import (
	"crypto/subtle"
	"encoding/json"
	"net/http"
)

const (
	CSRFCookieName = "panel_csrf"
	CSRFHeaderName = "X-CSRF-Token"
)

func CSRF(next http.Handler) http.Handler {
	return http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		if r.Method == http.MethodGet {
			next.ServeHTTP(w, r)
			return
		}
		cookie, err := r.Cookie(CSRFCookieName)
		header := r.Header.Get(CSRFHeaderName)
		if err != nil || cookie.Value == "" || header == "" ||
			subtle.ConstantTimeCompare([]byte(cookie.Value), []byte(header)) != 1 {
			writeCSRFProblem(w)
			return
		}
		next.ServeHTTP(w, r)
	})
}

func writeCSRFProblem(w http.ResponseWriter) {
	w.Header().Set("Content-Type", "application/problem+json")
	w.WriteHeader(http.StatusForbidden)
	_ = json.NewEncoder(w).Encode(struct {
		Type   string `json:"type"`
		Title  string `json:"title"`
		Status int    `json:"status"`
		Detail string `json:"detail"`
	}{
		Type:   "about:blank",
		Title:  "Forbidden",
		Status: http.StatusForbidden,
		Detail: "CSRF token is missing or invalid",
	})
}
