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
