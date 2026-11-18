// Package debugfile detects and identifies uploaded debug files (ELF, Mach-O,
// ProGuard mappings, Breakpad symbols, source bundles) and keeps a bounded
// on-disk cache of the blobs derived from them.
//
// Everything here reads attacker-controlled bytes. Detection never trusts a
// length from a header: every count and offset is checked against the real
// file size before anything is allocated or read, and every read is capped.
package debugfile

import (
	"encoding/hex"
	"strings"
)

// DebugIDFromBuildID returns the debug id of an ELF GNU build-id: the first 16
// bytes (zero-padded), with the first three UUID fields read little-endian
// (the first 4, next 2 and next 2 bytes swapped), as a hyphenated lowercase UUID.
func DebugIDFromBuildID(id []byte) string {
	var u [16]byte
	copy(u[:], id)
	return formatUUID(swapUUID(u))
}

func swapUUID(u [16]byte) [16]byte {
	u[0], u[1], u[2], u[3] = u[3], u[2], u[1], u[0]
	u[4], u[5] = u[5], u[4]
	u[6], u[7] = u[7], u[6]
	return u
}

// DebugIDFromMachoUUID returns the debug id of a Mach-O LC_UUID value: the
// bytes as a hyphenated lowercase UUID, without any swapping.
func DebugIDFromMachoUUID(u [16]byte) string { return formatUUID(u) }

func formatUUID(u [16]byte) string {
	var b [36]byte
	hex.Encode(b[0:8], u[0:4])
	b[8] = '-'
	hex.Encode(b[9:13], u[4:6])
	b[13] = '-'
	hex.Encode(b[14:18], u[6:8])
	b[18] = '-'
	hex.Encode(b[19:23], u[8:10])
	b[23] = '-'
	hex.Encode(b[24:36], u[10:16])
	return string(b[:])
}

// NormalizeDebugID returns the debug id as a lowercase hyphenated UUID with an
// optional "-<age hex>" suffix (PE and Breakpad ids), or "" when s is not one.
// It accepts braces, a bare 32-digit form and an age appended without a hyphen.
func NormalizeDebugID(s string) string {
	s = strings.ToLower(strings.TrimSpace(s))
	s = strings.TrimPrefix(strings.TrimSuffix(s, "}"), "{")
	var uuid, age string
	switch {
	case len(s) >= 36 && s[8] == '-' && s[13] == '-' && s[18] == '-' && s[23] == '-':
		uuid = s[:36]
		rest := s[36:]
		if rest != "" {
			if rest[0] != '-' {
				return ""
			}
			age = rest[1:]
		}
	case len(s) >= 32:
		uuid = s[:8] + "-" + s[8:12] + "-" + s[12:16] + "-" + s[16:20] + "-" + s[20:32]
		age = s[32:]
	default:
		return ""
	}
	if !validUUID(uuid) {
		return ""
	}
	if age == "" {
		return uuid
	}
	if len(age) > 8 || !isHex(age) {
		return ""
	}
	return uuid + "-" + age
}

func validUUID(s string) bool {
	if len(s) != 36 {
		return false
	}
	for i := 0; i < len(s); i++ {
		switch i {
		case 8, 13, 18, 23:
			if s[i] != '-' {
				return false
			}
		default:
			if !isHexByte(s[i]) {
				return false
			}
		}
	}
	return true
}

func isHex(s string) bool {
	for i := 0; i < len(s); i++ {
		if !isHexByte(s[i]) {
			return false
		}
	}
	return s != ""
}

func isHexByte(c byte) bool { return c >= '0' && c <= '9' || c >= 'a' && c <= 'f' }
