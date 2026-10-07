// Package crypto provides password, token, and secret encryption primitives
// for the application platform.
package crypto

import (
	"crypto/rand"
	"crypto/sha256"
	"encoding/base32"
)

const tokenBytes = 32

var tokenEncoding = base32.StdEncoding.WithPadding(base32.NoPadding)

// NewToken returns a prefixed random token and the hash that may be stored for
// later verification. The plaintext token must only be shown to the caller.
func NewToken(prefix string) (plain string, hash []byte) {
	random := make([]byte, tokenBytes)
	if _, err := rand.Read(random); err != nil {
		panic("generate secure token: " + err.Error())
	}
	plain = prefix + tokenEncoding.EncodeToString(random)
	return plain, HashToken(plain)
}

// HashToken returns the SHA-256 digest of a plaintext token.
func HashToken(plain string) []byte {
	sum := sha256.Sum256([]byte(plain))
	return sum[:]
}
