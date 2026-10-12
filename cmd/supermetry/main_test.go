package main

import (
	"bytes"
	"context"
	"strings"
	"testing"
)

func TestCommandNameDefaultsToServe(t *testing.T) {
	for _, tc := range []struct {
		args []string
		want string
	}{
		{[]string{"supermetry"}, "serve"},
		{[]string{"supermetry", "serve"}, "serve"},
		{[]string{"supermetry", "migrate"}, "migrate"},
		{[]string{"supermetry", "--help"}, "help"},
		{[]string{"supermetry", "-h"}, "help"},
		{[]string{"supermetry", "help"}, "help"},
	} {
		if got := commandName(tc.args); got != tc.want {
			t.Errorf("commandName(%v) = %q, want %q", tc.args, got, tc.want)
		}
	}
}

func TestInstallHelpDocumentsFlagsAndExamples(t *testing.T) {
	t.Parallel()
	var output bytes.Buffer
	if err := installCommand(context.Background(), []string{"--help"}, strings.NewReader(""), &output); err != nil {
		t.Fatal(err)
	}
	for _, want := range []string{"--dry-run", "--mode", "--base-url", "--database-url", "--db-host", "--db-port", "--db-user", "--db-password", "--db-name", "--db-sslmode", "--listen", "--proxy", "--image", "--dir", "--yes", "--version", "Examples:", "supermetry install --dry-run"} {
		if !strings.Contains(output.String(), want) {
			t.Errorf("help missing %q:\n%s", want, output.String())
		}
	}
}
