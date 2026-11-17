package lifecycle

import (
	"bytes"
	"context"
	"errors"
	"log/slog"
	"strings"
	"testing"
)

func TestLogFailureNamesTheSMTPStepWithoutTheRecipient(t *testing.T) {
	var logs bytes.Buffer
	previous := slog.Default()
	slog.SetDefault(slog.New(slog.NewTextHandler(&logs, nil)))
	t.Cleanup(func() { slog.SetDefault(previous) })
	cause := errors.New("authenticate SMTP: 535 rejected person@example.com")
	new(Notices).logFailure(context.Background(), DeletionTarget{Kind: DeletionSite, ID: 3}, deletionDeliveryError{err: cause, failedCount: 1})
	out := logs.String()
	if !strings.Contains(out, "step=\"authenticate SMTP\"") || strings.Contains(out, "person@example.com") {
		t.Fatalf("log = %s", out)
	}
}
