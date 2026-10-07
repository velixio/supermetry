package config

import (
	"os"
	"path/filepath"
	"testing"
)

func TestLoadDotEnv(t *testing.T) {
	path := filepath.Join(t.TempDir(), ".env")
	os.WriteFile(path, []byte("# comment\n\nPANEL_T_A=plain\nexport PANEL_T_B=\"quoted value\"\nPANEL_T_C='single'\nPANEL_T_URL=postgres://u:p@h:5432/db?sslmode=disable\nPANEL_T_KEEP=from-file\n"), 0o600)
	t.Setenv("PANEL_T_KEEP", "from-env")
	for _, k := range []string{"PANEL_T_A", "PANEL_T_B", "PANEL_T_C", "PANEL_T_URL"} {
		os.Unsetenv(k)
		t.Cleanup(func() { os.Unsetenv(k) })
	}
	if err := LoadDotEnv(path); err != nil {
		t.Fatal(err)
	}
	want := map[string]string{"PANEL_T_A": "plain", "PANEL_T_B": "quoted value", "PANEL_T_C": "single", "PANEL_T_URL": "postgres://u:p@h:5432/db?sslmode=disable", "PANEL_T_KEEP": "from-env"}
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
