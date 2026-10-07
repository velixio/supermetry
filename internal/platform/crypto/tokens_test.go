package crypto_test

import (
	"bytes"
	"strings"
	"testing"

	platformcrypto "panel/internal/platform/crypto"
)

func TestTokensAreRandomAndHashed(t *testing.T) {
	a, hashA := platformcrypto.NewToken("pnl_")
	b, _ := platformcrypto.NewToken("pnl_")
	if a == b {
		t.Fatal("tokens are equal")
	}
	if !strings.HasPrefix(a, "pnl_") {
		t.Fatalf("token %q does not have prefix", a)
	}
	if !bytes.Equal(hashA, platformcrypto.HashToken(a)) {
		t.Fatal("token hash does not match")
	}
	if len(hashA) != 32 {
		t.Fatalf("hash length = %d, want 32", len(hashA))
	}
	if strings.Contains(a, "=") {
		t.Fatalf("token %q contains base32 padding", a)
	}
}
