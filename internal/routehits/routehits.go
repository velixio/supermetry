package routehits

import (
	"context"
	"net/http"
	"os"
	"strings"
)

const Env = "SUPERMETRY_ROUTE_HITS"

type testNameKey struct{}
type recorderKey struct{}

// WithTestName annotates a test request without reading or recording request
// metadata. Callers that do not supply a name still record the matched route.
func WithTestName(request *http.Request, name string) *http.Request {
	return request.WithContext(context.WithValue(request.Context(), testNameKey{}, name))
}

// Wrap returns next unchanged unless route recording is enabled.
func Wrap(next http.Handler) http.Handler {
	filename := os.Getenv(Env)
	if filename == "" {
		return next
	}
	return http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		if _, nested := r.Context().Value(recorderKey{}).(struct{}); nested {
			next.ServeHTTP(w, r)
			return
		}
		r = r.WithContext(context.WithValue(r.Context(), recorderKey{}, struct{}{}))
		defer record(filename, r)
		next.ServeHTTP(w, r)
	})
}

func record(filename string, request *http.Request) {
	if request.Pattern == "" {
		return
	}
	line := request.Pattern
	if name, ok := request.Context().Value(testNameKey{}).(string); ok && name != "" {
		line += "\t" + sanitize(name)
	}
	line += "\n"
	file, err := os.OpenFile(filename, os.O_CREATE|os.O_WRONLY|os.O_APPEND, 0o600)
	if err != nil {
		return
	}
	_, _ = file.Write([]byte(line)) // One O_APPEND write keeps each cross-process record intact.
	_ = file.Close()
}

func sanitize(value string) string {
	return strings.NewReplacer("\t", " ", "\r", " ", "\n", " ").Replace(value)
}
