package appkeys_test

import (
	"crypto/sha256"
	"strings"
	"testing"

	"supermetry/internal/platform/appkeys"
)

func TestNewKeyFormat(t *testing.T) {
	plain, prefix, hash, err := appkeys.New()
	if err != nil {
		t.Fatal(err)
	}
	if !appkeys.Valid(plain) || len(plain) != 43 || prefix != plain[4:12] || hash != sha256.Sum256([]byte(plain)) {
		t.Fatalf("plain=%q prefix=%q", plain, prefix)
	}
	if appkeys.Hash(plain) != hash {
		t.Fatal("Hash differs from New")
	}
	other, _, _, _ := appkeys.New()
	if other == plain {
		t.Fatal("keys must differ")
	}
}

func TestValidRejects(t *testing.T) {
	body := strings.Repeat("a", 39)
	for name, in := range map[string]string{
		"empty": "", "prefix only": "smk_", "uppercase": "smk_" + strings.Repeat("A", 39),
		"wrong prefix": "pnl_" + body, "short": "smk_" + body[:38], "long": "smk_" + body + "a",
		"digit 1": "smk_" + body[:38] + "1", "digit 8": "smk_" + body[:38] + "8",
	} {
		if appkeys.Valid(in) {
			t.Errorf("%s accepted", name)
		}
	}
	if !appkeys.Valid("smk_" + body) {
		t.Error("valid rejected")
	}
}

func TestNewDSNKeyFormat(t *testing.T) {
	seen := make(map[string]struct{}, 1000)
	for i := 0; i < 1000; i++ {
		key := appkeys.NewDSNKey()
		if !appkeys.ValidDSNKey(key) || len(key) != 32 || strings.ToLower(key) != key {
			t.Fatalf("bad key %q", key)
		}
		seen[key] = struct{}{}
	}
	if len(seen) != 1000 {
		t.Fatalf("unique keys = %d", len(seen))
	}
	if appkeys.ValidDSNKey("0123456789ABCDEF0123456789abcdef") || appkeys.ValidDSNKey("") {
		t.Fatal("invalid key accepted")
	}
}
