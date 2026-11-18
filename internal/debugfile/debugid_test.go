package debugfile

import (
	"encoding/hex"
	"testing"
)

func TestDebugIDFromBuildID(t *testing.T) {
	id, _ := hex.DecodeString("109e3c7ab2558e4b8a532f0c8d4e9a01c0ffee00")
	if got, want := DebugIDFromBuildID(id), "7a3c9e10-55b2-4b8e-8a53-2f0c8d4e9a01"; got != want {
		t.Fatalf("got %s want %s", got, want)
	}
	short, _ := hex.DecodeString("0102030405060708")
	if got, want := DebugIDFromBuildID(short), "04030201-0605-0807-0000-000000000000"; got != want {
		t.Fatalf("short: got %s want %s", got, want)
	}
	if got := DebugIDFromBuildID(nil); got != "00000000-0000-0000-0000-000000000000" {
		t.Fatalf("empty: %s", got)
	}
}

func TestDebugIDFromMachoUUID(t *testing.T) {
	raw, _ := hex.DecodeString("de7fada356f73ce7b81e0b6e964459c1")
	var u [16]byte
	copy(u[:], raw)
	if got, want := DebugIDFromMachoUUID(u), "de7fada3-56f7-3ce7-b81e-0b6e964459c1"; got != want {
		t.Fatalf("got %s want %s", got, want)
	}
}

func TestNormalizeDebugID(t *testing.T) {
	cases := map[string]string{
		"DE7FADA356F73CE7B81E0B6E964459C1":               "de7fada3-56f7-3ce7-b81e-0b6e964459c1",
		"{DE7FADA3-56F7-3CE7-B81E-0B6E964459C1}":         "de7fada3-56f7-3ce7-b81e-0b6e964459c1",
		"de7fada3-56f7-3ce7-b81e-0b6e964459c1-1a":        "de7fada3-56f7-3ce7-b81e-0b6e964459c1-1a",
		"DE7FADA356F73CE7B81E0B6E964459C11A":             "de7fada3-56f7-3ce7-b81e-0b6e964459c1-1a",
		"not an id":                                      "",
		"":                                               "",
		"de7fada3-56f7-3ce7-b81e-0b6e964459c1-zz":        "",
		"de7fada3-56f7-3ce7-b81e-0b6e964459c1-123456789": "",
		"de7fada3-56f7-3ce7-b81e-0b6e964459cg":           "",
	}
	for in, want := range cases {
		if got := NormalizeDebugID(in); got != want {
			t.Errorf("NormalizeDebugID(%q) = %q, want %q", in, got, want)
		}
	}
}
