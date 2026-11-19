package platformhttp_test

import (
	"context"

	platformauth "supermetry/internal/platform/auth"
)

// freshSession stands in for a session that signed in a moment ago.
type freshSession struct{}

func (freshSession) RequireRecentAuth(context.Context, string) error { return nil }

// toggleSession is fresh until stale is set.
type toggleSession struct{ stale *bool }

func (t toggleSession) RequireRecentAuth(context.Context, string) error {
	if *t.stale {
		return platformauth.ErrReauthRequired
	}
	return nil
}
