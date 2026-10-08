package main

import (
	"context"
	"strings"
	"testing"
)

func TestRecomputeRollupsRejectsArguments(t *testing.T) {
	err := recomputeRollupsCommand(context.Background(), []string{"--site", "1"}, func(string) string { return "" })
	if err == nil || !strings.Contains(err.Error(), "takes no arguments") {
		t.Fatalf("err = %v, want argument error", err)
	}
}

func TestRecomputeRollupsNeedsDatabaseURL(t *testing.T) {
	err := recomputeRollupsCommand(context.Background(), nil, func(string) string { return "" })
	if err == nil {
		t.Fatal("want a configuration error without PANEL_DATABASE_URL")
	}
}
