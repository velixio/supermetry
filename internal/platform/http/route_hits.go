package platformhttp

import (
	"net/http"

	"supermetry/internal/routehits"
)

const routeHitsEnv = routehits.Env

// WithRouteTestName annotates a test request without reading or recording any
// request metadata. Callers that do not supply a name still record the route.
func WithRouteTestName(request *http.Request, name string) *http.Request {
	return routehits.WithTestName(request, name)
}

// withRouteHitRecorder deliberately returns next unchanged unless recording is
// enabled, so production requests pay no middleware or environment lookup cost.
func withRouteHitRecorder(next http.Handler) http.Handler {
	return routehits.Wrap(next)
}
