package opencounters

import (
	"os"
	"path/filepath"
	"strings"
	"testing"
)

func TestParsePath(t *testing.T) {
	longRepo := strings.Repeat("a", 100)
	tests := []struct {
		path, key, ext string
		ok             bool
	}{
		{"github.com/Velixio.svg", "github.com/velixio", "svg", true},
		{"github.com/velixio/Supermetry.gif", "github.com/velixio/supermetry", "gif", true},
		{"github.com/a-b/c.d_e-f", "github.com/a-b/c.d_e-f", "", true},
		{"github.com/a/" + longRepo, "github.com/a/" + longRepo, "", true},
		{"gitlab.com/a", "", "", false},
		{"github.com/", "", "", false},
		{"github.com/a_b", "", "", false},
		{"github.com/" + strings.Repeat("a", 40), "", "", false},
		{"github.com/a/" + strings.Repeat("b", 101), "", "", false},
		{"github.com/a/b/c", "", "", false},
		{"github.com/<x>", "", "", false},
		{"github.com/a.png", "", "", false},
		{"github.com/a.svg.svg", "", "", false},
		{"github.com/a/.", "", "", false},
	}
	for _, tc := range tests {
		t.Run(tc.path, func(t *testing.T) {
			key, ext, ok := ParsePath(tc.path)
			if key != tc.key || ext != tc.ext || ok != tc.ok {
				t.Fatalf("ParsePath(%q) = (%q, %q, %v), want (%q, %q, %v)", tc.path, key, ext, ok, tc.key, tc.ext, tc.ok)
			}
		})
	}
}

func TestLoadBlocklist(t *testing.T) {
	if got, err := LoadBlocklist(""); err != nil || len(got) != 0 {
		t.Fatalf("LoadBlocklist(empty) = %v, %v", got, err)
	}
	path := filepath.Join(t.TempDir(), "blocked.txt")
	if err := os.WriteFile(path, []byte("# spam\nGitHub.com/Bad\n\n github.com/bad/repo \n"), 0o600); err != nil {
		t.Fatal(err)
	}
	got, err := LoadBlocklist(path)
	if err != nil {
		t.Fatal(err)
	}
	for _, key := range []string{"github.com/bad", "github.com/bad/repo"} {
		if !got[key] {
			t.Errorf("missing blocklist key %q", key)
		}
	}
}
