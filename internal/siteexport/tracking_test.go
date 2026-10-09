package siteexport_test

import (
	"bytes"
	"context"
	"errors"
	"panel/internal/api"
	"panel/internal/siteexport"
	"testing"
	"time"
)

func TestAnonymousExportWritesNoHistoricalData(t *testing.T) {
	var output bytes.Buffer
	err := siteexport.New(nil).Write(context.Background(), &output, 1, time.Now(), time.Now())
	if !errors.Is(err, api.ErrAnonymousHistory) || output.Len() != 0 {
		t.Fatalf("err=%v bytes=%d", err, output.Len())
	}
}
