package crypto_test

import (
	"bytes"
	"testing"

	platformcrypto "supermetry/internal/platform/crypto"
)

func TestSecretBox(t *testing.T) {
	box, err := platformcrypto.NewSecretBox(bytes.Repeat([]byte{7}, 32), "panel/totp/v1")
	if err != nil {
		t.Fatal(err)
	}
	sealed := box.Seal([]byte("JBSWY3DPEHPK3PXP"))
	plain, err := box.Open(sealed)
	if err != nil || string(plain) != "JBSWY3DPEHPK3PXP" {
		t.Fatalf("round trip: plain=%q err=%v", plain, err)
	}

	sealed[len(sealed)-1] ^= 1
	if _, err := box.Open(sealed); err == nil {
		t.Fatal("tamper not detected")
	}

	other, err := platformcrypto.NewSecretBox(bytes.Repeat([]byte{7}, 32), "supermetry/other/v1")
	if err != nil {
		t.Fatal(err)
	}
	if _, err := other.Open(box.Seal([]byte("x"))); err == nil {
		t.Fatal("HKDF info is not bound")
	}
}

func TestNewSecretBoxRejectsShortMasterKey(t *testing.T) {
	if _, err := platformcrypto.NewSecretBox(make([]byte, 31), "panel/totp/v1"); err == nil {
		t.Fatal("short master key accepted")
	}
}
