package crypto_test

import (
	"errors"
	"strings"
	"testing"

	platformcrypto "supermetry/internal/platform/crypto"
)

func TestValidateNewPassword(t *testing.T) {
	tests := []struct {
		name     string
		password string
		wantErr  error
	}{
		{name: "six characters", password: "sixsix", wantErr: nil},
		{name: "five characters", password: "short", wantErr: platformcrypto.ErrPasswordTooShort},
		{name: "common password", password: "password123", wantErr: platformcrypto.ErrPasswordCommon},
		{name: "common password ignores case and whitespace", password: "  PASSWORD123  ", wantErr: platformcrypto.ErrPasswordCommon},
	}
	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			err := platformcrypto.ValidateNewPassword(tt.password)
			if !errors.Is(err, tt.wantErr) {
				t.Fatalf("ValidateNewPassword() error = %v, want %v", err, tt.wantErr)
			}
		})
	}
}

func TestPasswordHashRoundTripAndPHC(t *testing.T) {
	hash, err := platformcrypto.HashPassword("correct horse")
	if err != nil {
		t.Fatal(err)
	}
	if !strings.HasPrefix(hash, "$argon2id$v=19$m=65536,t=3,p=2$") {
		t.Fatalf("bad PHC %q", hash)
	}
	if ok, err := platformcrypto.VerifyPassword(hash, "correct horse"); err != nil || !ok {
		t.Fatalf("verify correct password: ok=%t err=%v", ok, err)
	}
	if ok, err := platformcrypto.VerifyPassword(hash, "Correct horse"); err != nil || ok {
		t.Fatalf("verify wrong password: ok=%t err=%v", ok, err)
	}
}

func TestVerifyPasswordRejectsMalformedPHC(t *testing.T) {
	tests := []string{
		"",
		"$argon2id$v=18$m=65536,t=3,p=2$c2FsdA$aGFzaA",
		"$argon2id$v=19$m=1,t=1,p=1$c2FsdA$aGFzaA",
		"$argon2id$v=19$m=65536,t=3,p=2$%%%$%%%",
	}
	for _, phc := range tests {
		t.Run(phc, func(t *testing.T) {
			if ok, err := platformcrypto.VerifyPassword(phc, "password"); err == nil || ok {
				t.Fatalf("VerifyPassword(%q) = %t, %v; want false, error", phc, ok, err)
			}
		})
	}
}
