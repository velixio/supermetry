package main

import (
	"crypto/ed25519"
	"crypto/rand"
	"encoding/base64"
	"os"
	"path/filepath"
	"testing"
)

func TestUpdaterReleaseKeyringRejectsMissingKeyAndAcceptsReleaseKey(t *testing.T) {
	t.Parallel()
	if _, err := updaterReleaseKeyring(""); err == nil {
		t.Fatal("missing key accepted")
	}
	public, _, err := ed25519.GenerateKey(rand.Reader)
	if err != nil {
		t.Fatal(err)
	}
	if _, err := updaterReleaseKeyring(base64.StdEncoding.EncodeToString(public)); err != nil {
		t.Fatal(err)
	}
	encoded := base64.StdEncoding.EncodeToString(public)
	if _, err := updaterReleaseKeyring(encoded + "," + encoded); err == nil {
		t.Fatal("duplicate key accepted")
	}
}

func TestReadUpdaterEnvironmentRejectsWritableFilesAndDoesNotExpandValues(t *testing.T) {
	t.Parallel()
	path := filepath.Join(t.TempDir(), "supermetry.env")
	if err := os.WriteFile(path, []byte("SUPERMETRY_DATABASE_URL='postgres://example/$VALUE'\n"), 0o600); err != nil {
		t.Fatal(err)
	}
	values, err := readUpdaterEnvironment(path)
	if err != nil {
		t.Fatal(err)
	}
	if len(values) != 2 || values[1] != "SUPERMETRY_DATABASE_URL=postgres://example/$VALUE" {
		t.Fatalf("environment = %#v", values)
	}
	if err := os.Chmod(path, 0o666); err != nil {
		t.Fatal(err)
	}
	if _, err := readUpdaterEnvironment(path); err == nil {
		t.Fatal("writable environment accepted")
	}
}
