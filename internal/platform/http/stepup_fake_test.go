package platformhttp

import (
	"context"

	platformauth "supermetry/internal/platform/auth"
)

// freshStepUp stands in for a session that signed in a moment ago.
type freshStepUp struct{}

func (freshStepUp) RequireRecentAuth(context.Context, string) error { return nil }

// toggleStepUp is fresh until stale is set.
type toggleStepUp struct{ stale *bool }

func (t toggleStepUp) RequireRecentAuth(context.Context, string) error {
	if *t.stale {
		return platformauth.ErrReauthRequired
	}
	return nil
}
