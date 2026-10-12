package routehits

import (
	"context"
	"net/http"
	"os"
	"strings"
	"sync/atomic"
)

const Env = "SUPERMETRY_ROUTE_HITS"

type testNameKey struct{}
type recorderKey struct{}

// state is shared by the outermost recorder and any nested ones for a request.
// A nested recorder sees the pattern its own mux matched even when middleware
// handed it a cloned request, and tells the outer recorder to skip the
// catch-all mount pattern it would otherwise record.
type state struct{ innerRecorded atomic.Bool }

// WithTestName annotates a test request without reading or recording request
// metadata. Callers that do not supply a name still record the matched route.
func WithTestName(request *http.Request, name string) *http.Request {
	return request.WithContext(context.WithValue(request.Context(), testNameKey{}, name))
}

// Wrap returns next unchanged unless route recording is enabled.
func Wrap(next http.Handler) http.Handler {
	return WrapFile(next, os.Getenv(Env))
}

// File returns the configured recording file, falling back to the environment so
// tests that build a bare config (and make api-inventory) still record routes.
func File(configured string) string {
	if configured != "" {
		return configured
	}
	return os.Getenv(Env)
}

func WrapFile(next http.Handler, filename string) http.Handler {
	if filename == "" {
		return next
	}
	return http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		if st, nested := r.Context().Value(recorderKey{}).(*state); nested {
			defer func() {
				if r.Pattern != "" {
					record(filename, r)
					st.innerRecorded.Store(true)
				}
			}()
			next.ServeHTTP(w, r)
			return
		}
		st := &state{}
		r = r.WithContext(context.WithValue(r.Context(), recorderKey{}, st))
		defer func() {
			if !st.innerRecorded.Load() {
				record(filename, r)
			}
		}()
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
