// Package appproduct turns app product hits (screens and custom events) into
// the web pipeline's events and sessions, so app projects get the same
// analytics as web sites without a second event table.
package appproduct

import (
	"crypto/hmac"
	"crypto/sha256"
	"encoding/binary"
	"encoding/hex"
)

func installMAC(secret []byte, installID string) []byte {
	mac := hmac.New(sha256.New, secret)
	mac.Write([]byte(installID))
	return mac.Sum(nil)
}

// VisitorID is the stable, non-negative analytics visitor id of an install:
// the first 63 bits of HMAC-SHA256(secret, installID). There is no daily
// salt, so the id is the same on every day for the same install.
func VisitorID(secret []byte, installID string) int64 {
	return int64(binary.BigEndian.Uint64(installMAC(secret, installID)[:8]) >> 1)
}

// ProfileExternalID is the profile key of an install, "i:" plus the full
// hex HMAC (66 characters). The raw install id is never stored.
func ProfileExternalID(secret []byte, installID string) string {
	return "i:" + hex.EncodeToString(installMAC(secret, installID))
}
