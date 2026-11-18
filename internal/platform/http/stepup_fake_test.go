package platformhttp

import "context"

// freshStepUp stands in for a session that signed in a moment ago.
type freshStepUp struct{}

func (freshStepUp) RequireRecentAuth(context.Context, string) error { return nil }
