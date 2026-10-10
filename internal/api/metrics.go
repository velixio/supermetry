package api

import (
	"crypto/sha256"
	"crypto/subtle"
	"net/http"
	"strings"

	"supermetry/internal/clientip"
	"supermetry/internal/config"
	"supermetry/internal/health"
)

func metricsHandler(cfg config.Config) http.HandlerFunc {
	return func(w http.ResponseWriter, r *http.Request) {
		if cfg.MetricsToken != "" {
			if !validBearerToken(r.Header.Get("Authorization"), cfg.MetricsToken) {
				w.Header().Set("WWW-Authenticate", "Bearer")
				http.Error(w, http.StatusText(http.StatusUnauthorized), http.StatusUnauthorized)
				return
			}
		} else {
			client, ok := clientip.ResolveAccessClient(r, nil)
			if !ok || !client.IsLoopback() || hasForwardingHeader(r.Header) {
				w.Header().Set("WWW-Authenticate", "Bearer")
				http.Error(w, http.StatusText(http.StatusUnauthorized), http.StatusUnauthorized)
				return
			}
		}

		w.Header().Set("Content-Type", "text/plain; version=0.0.4; charset=utf-8")
		health.WritePrometheus(w)
	}
}

func hasForwardingHeader(header http.Header) bool {
	for _, name := range []string{"X-Forwarded-For", "Forwarded", "X-Real-IP", "CF-Connecting-IP", "True-Client-IP"} {
		if _, ok := header[http.CanonicalHeaderKey(name)]; ok {
			return true
		}
	}
	return false
}

func validBearerToken(header, token string) bool {
	provided, ok := strings.CutPrefix(header, "Bearer ")
	if !ok {
		provided = ""
	}
	providedHash := sha256.Sum256([]byte(provided))
	expectedHash := sha256.Sum256([]byte(token))
	equal := subtle.ConstantTimeCompare(providedHash[:], expectedHash[:]) == 1
	return ok && equal
}
