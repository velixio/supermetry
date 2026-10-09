package config

import (
	"os"
	"path/filepath"
	"testing"
)

func TestLoadDotEnv(t *testing.T) {
	path := filepath.Join(t.TempDir(), ".env")
	os.WriteFile(path, []byte("# comment\n\nSUPERMETRY_T_A=plain\nexport SUPERMETRY_T_B=\"quoted value\"\nSUPERMETRY_T_C='single'\nSUPERMETRY_T_URL=postgres://u:p@h:5432/db?sslmode=disable\nSUPERMETRY_T_KEEP=from-file\n"), 0o600)
	t.Setenv("SUPERMETRY_T_KEEP", "from-env")
	for _, k := range []string{"SUPERMETRY_T_A", "SUPERMETRY_T_B", "SUPERMETRY_T_C", "SUPERMETRY_T_URL"} {
		os.Unsetenv(k)
		t.Cleanup(func() { os.Unsetenv(k) })
	}
	if err := LoadDotEnv(path); err != nil {
		t.Fatal(err)
	}
	want := map[string]string{"SUPERMETRY_T_A": "plain", "SUPERMETRY_T_B": "quoted value", "SUPERMETRY_T_C": "single", "SUPERMETRY_T_URL": "postgres://u:p@h:5432/db?sslmode=disable", "SUPERMETRY_T_KEEP": "from-env"}
	for k, v := range want {
		if got := os.Getenv(k); got != v {
			t.Errorf("%s = %q, want %q", k, got, v)
		}
	}
	if err := LoadDotEnv(filepath.Join(t.TempDir(), "missing")); err != nil {
		t.Fatalf("missing file must be ignored: %v", err)
	}
	bad := filepath.Join(t.TempDir(), "bad")
	os.WriteFile(bad, []byte("NOT A PAIR\n"), 0o600)
	if err := LoadDotEnv(bad); err == nil {
		t.Fatal("malformed line accepted")
	}
}
