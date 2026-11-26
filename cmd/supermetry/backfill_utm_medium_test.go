package main

import (
	"context"
	"strings"
	"testing"
)

func TestBackfillUTMMediumRejectsArguments(t *testing.T) {
	err := backfillUTMMediumCommand(context.Background(), []string{"--site", "1"}, func(string) string { return "" })
	if err == nil || !strings.Contains(err.Error(), "takes no arguments") {
		t.Fatalf("err = %v, want argument error", err)
	}
}
