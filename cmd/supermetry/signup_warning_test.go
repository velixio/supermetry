package main

import (
	"bytes"
	"context"
	"log/slog"
	"strings"
	"testing"
)

func TestWarnPublicSignup(t *testing.T) {
	var output bytes.Buffer
	logger := slog.New(slog.NewTextHandler(&output, nil))

	warnPublicSignup(context.Background(), logger, "invite")
	if output.Len() != 0 {
		t.Fatalf("invite mode logged warning: %s", output.String())
	}
	warnPublicSignup(context.Background(), logger, "open")
	if !strings.Contains(output.String(), "public signup is open") {
		t.Fatalf("warning = %q", output.String())
	}
}
