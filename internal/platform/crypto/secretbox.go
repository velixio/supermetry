package crypto

import (
	"crypto/aes"
	"crypto/cipher"
	"crypto/rand"
	"crypto/sha256"
	"errors"
	"fmt"
	"io"

	"golang.org/x/crypto/hkdf"
)

const minimumMasterKeyBytes = 32

// SecretBox encrypts small application secrets with an AEAD key derived from
// the installation master key and a purpose-specific info string.
type SecretBox struct {
	aead cipher.AEAD
}

// NewSecretBox derives an AES-256-GCM key using HKDF-SHA256.
func NewSecretBox(masterKey []byte, info string) (*SecretBox, error) {
	if len(masterKey) < minimumMasterKeyBytes {
		return nil, fmt.Errorf("master key must be at least %d bytes", minimumMasterKeyBytes)
	}
	key := make([]byte, 32)
	if _, err := io.ReadFull(hkdf.New(sha256.New, masterKey, nil, []byte(info)), key); err != nil {
		return nil, fmt.Errorf("derive secret-box key: %w", err)
	}
	block, err := aes.NewCipher(key)
	if err != nil {
		return nil, fmt.Errorf("create AES cipher: %w", err)
	}
	aead, err := cipher.NewGCM(block)
	if err != nil {
		return nil, fmt.Errorf("create GCM: %w", err)
	}
	return &SecretBox{aead: aead}, nil
}

// Seal encrypts plain and prepends a fresh random nonce.
func (b *SecretBox) Seal(plain []byte) []byte {
	nonce := make([]byte, b.aead.NonceSize())
	if _, err := rand.Read(nonce); err != nil {
		panic("generate encryption nonce: " + err.Error())
	}
	return b.aead.Seal(nonce, nonce, plain, nil)
}

// Open authenticates and decrypts a value returned by Seal.
func (b *SecretBox) Open(sealed []byte) ([]byte, error) {
	if len(sealed) < b.aead.NonceSize()+b.aead.Overhead() {
		return nil, errors.New("encrypted secret is too short")
	}
	nonce := sealed[:b.aead.NonceSize()]
	ciphertext := sealed[b.aead.NonceSize():]
	plain, err := b.aead.Open(nil, nonce, ciphertext, nil)
	if err != nil {
		return nil, fmt.Errorf("open encrypted secret: %w", err)
	}
	return plain, nil
}
