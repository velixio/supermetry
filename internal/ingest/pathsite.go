package ingest

import "context"

type pathSiteKey struct{}

// WithPathSite records the site public ID named by a neutral intake path
// (/e/{site}). Handlers that read the site from the request body or query
// reject a request whose site differs from it.
func WithPathSite(ctx context.Context, publicID string) context.Context {
	return context.WithValue(ctx, pathSiteKey{}, publicID)
}

// PathSite returns the site public ID set by WithPathSite.
func PathSite(ctx context.Context) (string, bool) {
	publicID, ok := ctx.Value(pathSiteKey{}).(string)
	return publicID, ok
}
