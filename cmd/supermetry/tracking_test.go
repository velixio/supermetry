package main

import (
	"bytes"
	"log/slog"
	"strings"
	"testing"

	"supermetry/internal/config"
	"supermetry/internal/privacy"
)

func TestLogTrackingModeSaysWhenIdentifiedIsTheDefault(t *testing.T) {
	var buf bytes.Buffer
	logger := slog.New(slog.NewTextHandler(&buf, nil))
	logTrackingMode(logger, config.TrackingMode{Mode: privacy.Identified})
	if !strings.Contains(buf.String(), "identified by default") || !strings.Contains(buf.String(), config.TrackingModeEnv+"=anonymous") {
		t.Fatalf("default not announced: %s", buf.String())
	}
	buf.Reset()
	logTrackingMode(logger, config.TrackingMode{Mode: privacy.Anonymous, Explicit: true})
	if strings.Contains(buf.String(), "by default") {
		t.Fatalf("explicit choice reported as default: %s", buf.String())
	}
}
