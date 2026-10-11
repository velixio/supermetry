package installer

import (
	"os"
	"path/filepath"
	"strings"
	"testing"

	"supermetry/internal/config"
)

func TestEnvRoundTripSpecialChars(t *testing.T) {
	const password = "p@ss:w/rd#% x"
	values := EnvValues{
		DatabaseURL:      (DBConfig{Host: "localhost", Port: "5432", User: "supermetry", Password: password, Name: "supermetry"}).ConnString(),
		BaseURL:          "https://analytics.example.com",
		Listen:           "127.0.0.1:8787",
		TrustedProxies:   "127.0.0.1",
		SecretKey:        "secret+key/with=padding",
		Docker:           true,
		PostgresPassword: password,
	}
	path := filepath.Join(t.TempDir(), ".env")
	if err := os.WriteFile(path, RenderEnv(values), 0o600); err != nil {
		t.Fatal(err)
	}
	keys := []string{"SUPERMETRY_DATABASE_URL", "SUPERMETRY_BASE_URL", "SUPERMETRY_LISTEN", "SUPERMETRY_TRUSTED_PROXIES", "SUPERMETRY_SECRET_KEY", "POSTGRES_PASSWORD"}
	for _, key := range keys {
		t.Setenv(key, "")
		if err := os.Unsetenv(key); err != nil {
			t.Fatal(err)
		}
	}
	if err := config.LoadDotEnv(path); err != nil {
		t.Fatal(err)
	}
	checks := map[string]string{
		"SUPERMETRY_DATABASE_URL":    values.DatabaseURL,
		"SUPERMETRY_BASE_URL":        values.BaseURL,
		"SUPERMETRY_LISTEN":          values.Listen,
		"SUPERMETRY_TRUSTED_PROXIES": values.TrustedProxies,
		"SUPERMETRY_SECRET_KEY":      values.SecretKey,
		"POSTGRES_PASSWORD":          password,
	}
	for key, want := range checks {
		if got := os.Getenv(key); got != want {
			t.Errorf("%s = %q, want %q", key, got, want)
		}
	}
}

func TestRenderEnvHasSMTPPlaceholders(t *testing.T) {
	t.Parallel()

	got := string(RenderEnv(EnvValues{}))
	for _, line := range []string{
		"# Optional. Fill in any time, then restart Supermetry.",
		"# SUPERMETRY_SMTP_HOST=",
		"# SUPERMETRY_SMTP_PORT=",
		"# SUPERMETRY_SMTP_USER=",
		"# SUPERMETRY_SMTP_PASS=",
		"# SUPERMETRY_SMTP_FROM=",
	} {
		if !strings.Contains(got, line) {
			t.Errorf("RenderEnv() missing %q", line)
		}
	}
}

func TestRenderEnvKeepsSecret(t *testing.T) {
	const secret = "existing-secret"
	path := filepath.Join(t.TempDir(), ".env")
	if err := os.WriteFile(path, []byte("SUPERMETRY_SECRET_KEY="+secret+"\n"), 0o600); err != nil {
		t.Fatal(err)
	}
	gotSecret := ReadExistingSecret(path)
	if gotSecret != secret {
		t.Fatalf("ReadExistingSecret() = %q, want %q", gotSecret, secret)
	}
	if got := string(RenderEnv(EnvValues{SecretKey: gotSecret})); !strings.Contains(got, "SUPERMETRY_SECRET_KEY="+secret+"\n") {
		t.Fatalf("RenderEnv() did not keep existing secret:\n%s", got)
	}
}

func TestNewSecretKey(t *testing.T) {
	t.Parallel()

	a, err := NewSecretKey()
	if err != nil {
		t.Fatal(err)
	}
	b, err := NewSecretKey()
	if err != nil {
		t.Fatal(err)
	}
	if a == b || len(a) != 44 || len(b) != 44 {
		t.Fatalf("keys must be distinct base64 encodings of 32 bytes: %q %q", a, b)
	}
}
