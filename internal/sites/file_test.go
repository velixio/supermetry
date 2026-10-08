package sites_test

import (
	"os"
	"path/filepath"
	"testing"

	"panel/internal/db"
	"panel/internal/sites"
)

func TestLoadFileAndAllows(t *testing.T) {
	p := filepath.Join(t.TempDir(), "sites.json")
	if err := os.WriteFile(p, []byte(`[{"id":1,"public_id":"abc123","timezone":"Asia/Kathmandu","retention_days":30,"allowed_hosts":["example.com","*.example.org"],"profiles_enabled":true}]`), 0o600); err != nil {
		t.Fatal(err)
	}
	r, err := sites.LoadFile(p)
	if err != nil {
		t.Fatal(err)
	}
	s, ok := r.ByPublicID("abc123")
	if !ok || s.TZ.String() != "Asia/Kathmandu" || s.Retention != db.Ret30 || !s.ProfilesEnabled || !s.RespectPrivacySignals {
		t.Fatalf("%+v %v", s, ok)
	}
	for host, want := range map[string]bool{
		"example.com":      true,
		"EXAMPLE.com":      true,
		"a.example.org":    true,
		"example.org":      false,
		"evil-example.com": false,
	} {
		if s.Allows(host) != want {
			t.Errorf("%s: %v", host, !want)
		}
	}
	if err := os.WriteFile(p, []byte(`[{"id":1,"public_id":"x","timezone":"Mars/Base","retention_days":30}]`), 0o600); err != nil {
		t.Fatal(err)
	}
	if _, err := sites.LoadFile(p); err == nil {
		t.Fatal("bad tz accepted")
	}
	if err := os.WriteFile(p, []byte(`[{"id":1,"public_id":"x","timezone":"UTC","retention_days":14}]`), 0o600); err != nil {
		t.Fatal(err)
	}
	if _, err := sites.LoadFile(p); err == nil {
		t.Fatal("bad retention accepted")
	}
}

func TestLoadFileRejectsDuplicateIdentifiers(t *testing.T) {
	tests := []struct {
		name string
		data string
	}{
		{
			name: "numeric ID",
			data: `[
				{"id":1,"public_id":"first","timezone":"UTC","retention_days":30},
				{"id":1,"public_id":"second","timezone":"UTC","retention_days":30}
			]`,
		},
		{
			name: "public ID",
			data: `[
				{"id":1,"public_id":"same","timezone":"UTC","retention_days":30},
				{"id":2,"public_id":"same","timezone":"UTC","retention_days":30}
			]`,
		},
	}
	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			p := filepath.Join(t.TempDir(), "sites.json")
			if err := os.WriteFile(p, []byte(tt.data), 0o600); err != nil {
				t.Fatal(err)
			}
			if _, err := sites.LoadFile(p); err == nil {
				t.Fatal("duplicate identifier accepted")
			}
		})
	}
}
