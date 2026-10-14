package installer

import (
	"bytes"
	"os"
	"strings"
	"testing"
)

func inTempDir(t *testing.T, env string) {
	t.Helper()
	dir := t.TempDir()
	if env != "" {
		if err := os.WriteFile(dir+"/.env", []byte(env), 0o600); err != nil {
			t.Fatal(err)
		}
	}
	old, _ := os.Getwd()
	if err := os.Chdir(dir); err != nil {
		t.Fatal(err)
	}
	t.Cleanup(func() { _ = os.Chdir(old) })
}

func TestAdoptDotEnvUsesAndMasks(t *testing.T) {
	inTempDir(t, "SUPERMETRY_DATABASE_URL=postgres://u:hunter2@db:5432/s\nSUPERMETRY_BASE_URL=https://a.example.com\nSUPERMETRY_LISTEN=0.0.0.0:8787\nSUPERMETRY_SECRET_KEY=topsecret\n")
	var out bytes.Buffer
	got, err := adoptDotEnv(Options{DotEnv: ".env"}, NewPrompter(strings.NewReader("y\n"), &out))
	if err != nil {
		t.Fatal(err)
	}
	if got.DB.URL == "" || got.BaseURL != "https://a.example.com" || got.Listen != "0.0.0.0:8787" {
		t.Fatalf("not adopted: %+v", got)
	}
	for _, secret := range []string{"hunter2", "topsecret"} {
		if strings.Contains(out.String(), secret) {
			t.Fatalf("output leaks %q:\n%s", secret, out.String())
		}
	}
	if !strings.Contains(out.String(), "Found configuration") {
		t.Fatalf("missing notice:\n%s", out.String())
	}
}

func TestAdoptDotEnvDeclinedOrAbsent(t *testing.T) {
	inTempDir(t, "SUPERMETRY_DATABASE_URL=postgres://u:p@db/s\n")
	got, err := adoptDotEnv(Options{DotEnv: ".env"}, NewPrompter(strings.NewReader("n\n"), &bytes.Buffer{}))
	if err != nil || got.DB.URL != "" {
		t.Fatalf("declined must not adopt: %+v %v", got, err)
	}
	inTempDir(t, "")
	var out bytes.Buffer
	got, err = adoptDotEnv(Options{DotEnv: ".env"}, NewPrompter(strings.NewReader(""), &out))
	if err != nil || got.DB.URL != "" || out.Len() != 0 {
		t.Fatalf("absent .env must be silent: %+v %v %q", got, err, out.String())
	}
}

func TestAdoptDotEnvKeepsFlags(t *testing.T) {
	inTempDir(t, "SUPERMETRY_DATABASE_URL=postgres://u:p@db/s\n")
	opts := Options{DotEnv: ".env", DB: DBConfig{URL: "postgres://flag"}}
	got, _ := adoptDotEnv(opts, NewPrompter(strings.NewReader(""), &bytes.Buffer{}))
	if got.DB.URL != "postgres://flag" {
		t.Fatalf("flag overridden: %+v", got)
	}
}
