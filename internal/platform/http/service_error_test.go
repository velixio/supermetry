package platformhttp

import (
	"bytes"
	"errors"
	"fmt"
	"log/slog"
	nethttp "net/http"
	"net/http/httptest"
	"strings"
	"testing"

	"supermetry/internal/platform/sitesvc"
)

func captureLogs(t *testing.T) *bytes.Buffer {
	t.Helper()
	var buffer bytes.Buffer
	previous := slog.Default()
	slog.SetDefault(slog.New(slog.NewTextHandler(&buffer, &slog.HandlerOptions{Level: slog.LevelDebug})))
	t.Cleanup(func() { slog.SetDefault(previous) })
	return &buffer
}

func TestServiceErrorLogsTheCauseOfAnUnexpectedFailure(t *testing.T) {
	logs := captureLogs(t)
	recorder := httptest.NewRecorder()
	serviceError(recorder, fmt.Errorf("query hourly overview: %w", errors.New(`relation "site_last_event" does not exist`)))

	if recorder.Code != nethttp.StatusInternalServerError {
		t.Fatalf("status = %d", recorder.Code)
	}
	body := recorder.Body.String()
	if strings.Contains(body, "site_last_event") || !strings.Contains(body, "the request could not be completed") {
		t.Fatalf("the response must stay generic, got %s", body)
	}
	if line := logs.String(); !strings.Contains(line, "query hourly overview") || !strings.Contains(line, "site_last_event") || !strings.Contains(line, "level=ERROR") {
		t.Fatalf("the cause must be logged, got %q", line)
	}
}

func TestServiceErrorDoesNotLogExpectedOutcomes(t *testing.T) {
	logs := captureLogs(t)
	for _, err := range []error{sitesvc.ErrNotFound, sitesvc.ErrForbidden, sitesvc.ErrInvalid} {
		serviceError(httptest.NewRecorder(), err)
	}
	if logs.Len() != 0 {
		t.Fatalf("expected outcomes must not be logged as errors, got %q", logs.String())
	}
}
