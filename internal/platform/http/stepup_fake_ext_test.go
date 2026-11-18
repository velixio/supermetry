package platformhttp_test

import "context"

// freshSession stands in for a session that signed in a moment ago.
type freshSession struct{}

func (freshSession) RequireRecentAuth(context.Context, string) error { return nil }
