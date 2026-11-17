// Package appkeys generates and checks ingest keys for app projects.
package appkeys

import (
	"crypto/rand"
	"crypto/sha256"
	"encoding/base32"
	"encoding/hex"
	"fmt"
	"strings"
)

const (
	keyPrefix  = "smk_"
	randomLen  = 24
	bodyLen    = 39 // 24 bytes in unpadded base32
	prefixFrom = 4
	prefixTo   = 12
)

var encoding = base32.StdEncoding.WithPadding(base32.NoPadding)

// New returns a fresh key, its 8-character display prefix and its SHA-256 hash.
// Only the hash is stored; the plaintext is shown once.
func New() (plain, prefix string, hash [32]byte, err error) {
	random := make([]byte, randomLen)
	if _, err = rand.Read(random); err != nil {
		return "", "", hash, fmt.Errorf("generate ingest key: %w", err)
	}
	plain = keyPrefix + strings.ToLower(encoding.EncodeToString(random))
	return plain, plain[prefixFrom:prefixTo], Hash(plain), nil
}

// NewDSNKey returns the public part of a DSN: 32 lowercase hex characters from
// 16 random bytes. It is public by design and stored in plain text.
func NewDSNKey() string {
	random := make([]byte, 16)
	_, _ = rand.Read(random) // never fails since Go 1.24; it aborts the process instead
	return hex.EncodeToString(random)
}

// ValidDSNKey reports whether s has the shape of a DSN public key.
func ValidDSNKey(s string) bool {
	if len(s) != 32 {
		return false
	}
	for _, c := range []byte(s) {
		if !(c >= '0' && c <= '9' || c >= 'a' && c <= 'f') {
			return false
		}
	}
	return true
}

// Hash returns the SHA-256 digest of a plaintext key.
func Hash(plain string) [32]byte { return sha256.Sum256([]byte(plain)) }

// Valid reports whether plain has the shape of an ingest key.
func Valid(plain string) bool {
	if len(plain) != len(keyPrefix)+bodyLen || !strings.HasPrefix(plain, keyPrefix) {
		return false
	}
	for _, c := range []byte(plain[len(keyPrefix):]) {
		if !(c >= 'a' && c <= 'z' || c >= '2' && c <= '7') {
			return false
		}
	}
	return true
}
