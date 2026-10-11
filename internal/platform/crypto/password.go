package crypto

import (
	"crypto/rand"
	"crypto/subtle"
	_ "embed"
	"encoding/base64"
	"errors"
	"fmt"
	"strings"
	"unicode/utf8"

	"golang.org/x/crypto/argon2"
)

const (
	argonTime      uint32 = 3
	argonMemory    uint32 = 64 * 1024
	argonThreads   uint8  = 2
	argonSaltBytes        = 16
	argonHashBytes        = 32
)

var errInvalidPasswordHash = errors.New("invalid argon2id password hash")

var (
	ErrPasswordTooShort = errors.New("password must be at least 6 characters")
	ErrPasswordCommon   = errors.New("password is too common")
)

//go:embed common_passwords.txt
var commonPasswordsText string

var commonPasswords = func() map[string]struct{} {
	passwords := make(map[string]struct{})
	for _, password := range strings.Fields(commonPasswordsText) {
		passwords[strings.ToLower(password)] = struct{}{}
	}
	return passwords
}()

// ValidateNewPassword applies the policy for newly chosen account and share
// passwords. Existing hashes remain valid so deployments can upgrade safely.
func ValidateNewPassword(password string) error {
	if utf8.RuneCountInString(password) < 6 {
		return ErrPasswordTooShort
	}
	if _, found := commonPasswords[strings.ToLower(strings.TrimSpace(password))]; found {
		return ErrPasswordCommon
	}
	return nil
}

// HashPassword hashes a password with the platform's fixed Argon2id policy.
func HashPassword(password string) (string, error) {
	salt := make([]byte, argonSaltBytes)
	if _, err := rand.Read(salt); err != nil {
		return "", fmt.Errorf("generate password salt: %w", err)
	}
	hash := argon2.IDKey([]byte(password), salt, argonTime, argonMemory, argonThreads, argonHashBytes)
	return fmt.Sprintf("$argon2id$v=%d$m=%d,t=%d,p=%d$%s$%s",
		argon2.Version,
		argonMemory,
		argonTime,
		argonThreads,
		base64.RawStdEncoding.EncodeToString(salt),
		base64.RawStdEncoding.EncodeToString(hash),
	), nil
}

// VerifyPassword compares a password against a PHC-formatted Argon2id hash.
func VerifyPassword(phc, password string) (bool, error) {
	parts := strings.Split(phc, "$")
	if len(parts) != 6 || parts[0] != "" || parts[1] != "argon2id" {
		return false, errInvalidPasswordHash
	}

	if parts[2] != fmt.Sprintf("v=%d", argon2.Version) {
		return false, errInvalidPasswordHash
	}
	if parts[3] != fmt.Sprintf("m=%d,t=%d,p=%d", argonMemory, argonTime, argonThreads) {
		return false, errInvalidPasswordHash
	}

	salt, err := base64.RawStdEncoding.Strict().DecodeString(parts[4])
	if err != nil || len(salt) != argonSaltBytes {
		return false, errInvalidPasswordHash
	}
	want, err := base64.RawStdEncoding.Strict().DecodeString(parts[5])
	if err != nil || len(want) != argonHashBytes {
		return false, errInvalidPasswordHash
	}
	got := argon2.IDKey([]byte(password), salt, argonTime, argonMemory, argonThreads, uint32(len(want)))
	return subtle.ConstantTimeCompare(got, want) == 1, nil
}
