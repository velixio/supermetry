package fixture

import "net/http"

const apiPrefix = "/api/example/v1"

func register(mux *http.ServeMux, handler http.Handler) {
	mux.HandleFunc("GET "+apiPrefix+"/widgets/{widget}", handler.ServeHTTP)
	mux.Handle("POST "+apiPrefix+"/widgets", authenticate(handler))
	for _, pattern := range []string{
		"DELETE " + apiPrefix + "/widgets/{widget}",
	} {
		mux.Handle(pattern, handler)
	}
	mux.Handle("POST "+deps.Config.EventPath, deps.Ingest)
	mux.Handle("GET "+tracker.FullPath(deps.Config.TrackerPath), handler)
}

var deps struct {
	Config struct{ EventPath, TrackerPath string }
	Ingest http.Handler
}

var tracker struct{ FullPath func(string) string }

func authenticate(next http.Handler) http.Handler { return next }
