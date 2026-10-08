package api

import "testing"

func TestExploreDimensionAllowsChannel(t *testing.T) {
	if !exploreDimensionAllowed("channel") {
		t.Fatal("channel must be allowed for explore filters and suggestions")
	}
}
