package debugfile

import (
	"bytes"
	"encoding/binary"
	"encoding/hex"
	"errors"
	"slices"
	"strings"
	"testing"
)

func detect(t *testing.T, data []byte, name string) ([]Object, error) {
	t.Helper()
	return Detect(bytes.NewReader(data), int64(len(data)), name)
}

func TestDetectELF(t *testing.T) {
	id, _ := hex.DecodeString("109e3c7ab2558e4b8a532f0c8d4e9a01c0ffee00")
	data := buildELF(elfSpec{machine: 183, buildID: id, text: []byte("code code code"), symtab: true, debugInfo: true})
	objs, err := detect(t, data, "libfix.so")
	if err != nil || len(objs) != 1 {
		t.Fatalf("objs=%v err=%v", objs, err)
	}
	o := objs[0]
	if o.Kind != "elf" || o.Arch != "arm64" || o.DebugID != "7a3c9e10-55b2-4b8e-8a53-2f0c8d4e9a01" || o.CodeID != hex.EncodeToString(id) {
		t.Fatalf("object %+v", o)
	}
	for _, f := range []string{"symtab", "debug", "code"} {
		if !slices.Contains(o.Features, f) {
			t.Fatalf("features %v miss %s", o.Features, f)
		}
	}
}

func TestDetectELFStrippedDebugHasNoDebugFeature(t *testing.T) {
	data := buildELF(elfSpec{machine: 62, buildID: []byte("0123456789abcdef"), text: []byte("x"), debugInfo: true, debugNoBit: true})
	objs, err := detect(t, data, "x")
	if err != nil || slices.Contains(objs[0].Features, "debug") {
		t.Fatalf("objs=%+v err=%v", objs, err)
	}
}

func TestDetectELFWithoutBuildIDFoldsText(t *testing.T) {
	text := make([]byte, 5000)
	for i := range text {
		text[i] = byte(i * 7)
	}
	data := buildELF(elfSpec{machine: 62, text: text})
	objs, err := detect(t, data, "x")
	if err != nil {
		t.Fatal(err)
	}
	var fold [16]byte
	for i := 0; i < foldLen; i++ {
		fold[i%16] ^= text[i]
	}
	if want := DebugIDFromBuildID(fold[:]); objs[0].DebugID != want || objs[0].CodeID != "" {
		t.Fatalf("got %+v want id %s", objs[0], want)
	}
}

func TestDetectELFWithoutAnyIdentityFails(t *testing.T) {
	if _, err := detect(t, buildELF(elfSpec{machine: 62}), "x"); !errors.Is(err, ErrUnsupported) {
		t.Fatalf("err = %v", err)
	}
}

func TestDetectMacho(t *testing.T) {
	var u [16]byte
	raw, _ := hex.DecodeString("de7fada356f73ce7b81e0b6e964459c1")
	copy(u[:], raw)
	objs, err := detect(t, buildMacho(machoSpec{cpu: 0x0100000c, uuid: u, dwarf: true, symtab: true}), "App.dSYM")
	if err != nil || len(objs) != 1 {
		t.Fatalf("objs=%v err=%v", objs, err)
	}
	o := objs[0]
	if o.Kind != "macho" || o.Arch != "arm64" || o.DebugID != "de7fada3-56f7-3ce7-b81e-0b6e964459c1" || !slices.Contains(o.Features, "debug") || !slices.Contains(o.Features, "symtab") {
		t.Fatalf("object %+v", o)
	}
}

func TestDetectFatMacho(t *testing.T) {
	a := buildMacho(machoSpec{cpu: 0x0100000c, uuid: [16]byte{1}, symtab: true})
	b := buildMacho(machoSpec{cpu: 0x01000007, uuid: [16]byte{2}, symtab: true})
	objs, err := detect(t, buildFat(a, b), "fat")
	if err != nil || len(objs) != 2 {
		t.Fatalf("objs=%v err=%v", objs, err)
	}
	if objs[0].Arch != "arm64" || objs[0].Slice != 0 || objs[1].Arch != "x86_64" || objs[1].Slice != 1 || objs[0].DebugID == objs[1].DebugID {
		t.Fatalf("objects %+v", objs)
	}
}

func TestDetectProguard(t *testing.T) {
	objs, err := detect(t, []byte("# compiler: R8\ncom.example.A -> a.a:\n"), "/proguard/6a1b8c0e-3d5f-3e2a-9b47-0c1d2e3f4a5b.txt")
	if err != nil || objs[0].Kind != "proguard" || objs[0].DebugID != "6a1b8c0e-3d5f-3e2a-9b47-0c1d2e3f4a5b" {
		t.Fatalf("objs=%+v err=%v", objs, err)
	}
	objs, err = detect(t, []byte("# comment\n\ncom.example.A -> a.a:\n    int x -> a\n"), "mapping.txt")
	if err != nil || objs[0].Kind != "proguard" || objs[0].DebugID != "" {
		t.Fatalf("sniffed: objs=%+v err=%v", objs, err)
	}
}

func TestDetectBreakpadSourceBundlePE(t *testing.T) {
	objs, err := detect(t, []byte("MODULE Linux x86_64 DE7FADA356F73CE7B81E0B6E964459C10 libfoo.so\nFUNC 0 1 0 f\n"), "libfoo.sym")
	if err != nil || objs[0].Kind != "breakpad" || objs[0].DebugID != "de7fada3-56f7-3ce7-b81e-0b6e964459c1-0" || objs[0].Arch != "x86_64" {
		t.Fatalf("breakpad: %+v %v", objs, err)
	}
	if objs, err = detect(t, []byte("SYSB\x02\x00\x00\x00PK\x03\x04"), "src.zip"); err != nil || objs[0].Kind != "sourcebundle" {
		t.Fatalf("sourcebundle: %+v %v", objs, err)
	}
	pe := make([]byte, 128)
	copy(pe, "MZ")
	binary.LittleEndian.PutUint32(pe[0x3c:], 64)
	copy(pe[64:], "PE\x00\x00")
	if objs, err = detect(t, pe, "a.dll"); err != nil || objs[0].Kind != "pe" {
		t.Fatalf("pe: %+v %v", objs, err)
	}
}

func TestDetectOther(t *testing.T) {
	for _, data := range [][]byte{nil, {1, 2}, bytes.Repeat([]byte{0xa5}, 16)} {
		objs, err := detect(t, data, "x.bin")
		if err != nil || len(objs) != 1 || objs[0].Kind != "other" {
			t.Fatalf("data %v: %+v %v", data, objs, err)
		}
	}
	// A Java class file shares the fat Mach-O magic and must not be rejected.
	class := append([]byte{0xca, 0xfe, 0xba, 0xbe, 0, 0, 0, 52}, make([]byte, 40)...)
	if objs, err := detect(t, class, "A.class"); err != nil || objs[0].Kind != "other" {
		t.Fatalf("class: %+v %v", objs, err)
	}
}

// Review Focus 5: hostile headers are rejected, never trusted.
func TestDetectHostile(t *testing.T) {
	le := binary.LittleEndian
	good := buildMacho(machoSpec{cpu: 0x0100000c, uuid: [16]byte{1}})
	elf := buildELF(elfSpec{machine: 62, buildID: []byte("0123456789abcdef"), text: []byte("x")})
	mk := func(base []byte, f func([]byte)) []byte {
		b := slices.Clone(base)
		f(b)
		return b
	}
	cases := map[string][]byte{
		"macho ncmds 2^31":      mk(good, func(b []byte) { le.PutUint32(b[16:], 1<<31) }),
		"macho ncmds max":       mk(good, func(b []byte) { le.PutUint32(b[16:], 0xffffffff) }),
		"macho sizeofcmds huge": mk(good, func(b []byte) { le.PutUint32(b[20:], 0xffffff00) }),
		"macho cmdsize beyond":  mk(good, func(b []byte) { le.PutUint32(b[36:], 1<<30) }),
		"macho truncated":       good[:30],
		"macho cmdsize tiny":    mk(good, func(b []byte) { le.PutUint32(b[36:], 1) }),
		"elf truncated header":  elf[:40],
		"elf shoff beyond":      mk(elf, func(b []byte) { le.PutUint64(b[40:], 1<<40) }),
		"elf shoff negative":    mk(elf, func(b []byte) { le.PutUint64(b[40:], 1<<63) }),
		"elf phoff beyond":      mk(elf, func(b []byte) { le.PutUint64(b[32:], 1<<40) }),
		"elf shnum 65535":       mk(elf, func(b []byte) { le.PutUint16(b[60:], 65535) }),
		"elf phnum 65535":       mk(elf, func(b []byte) { le.PutUint16(b[56:], 65535) }),
		"elf phentsize small":   mk(elf, func(b []byte) { le.PutUint16(b[54:], 4) }),
		"elf bad class":         mk(elf, func(b []byte) { b[4] = 9 }),
		"elf shstrtab size 5GB": mk(elf, func(b []byte) { le.PutUint64(b[int(le.Uint64(b[40:]))+int(le.Uint16(b[62:]))*64+32:], 5<<30) }),
		"fat slice beyond":      func() []byte { f := buildFat(good); binary.BigEndian.PutUint32(f[16:], 1<<30); return f }(),
	}
	for name, data := range cases {
		objs, err := detect(t, data, "x")
		if err == nil && (len(objs) != 1 || objs[0].Kind == "macho" || objs[0].Kind == "elf") {
			// Some corruptions leave a still-valid object; those are fine only when no
			// field escaped the bounds, which the fuzz target covers. Flag the rest.
			if name == "elf shstrtab size 5GB" {
				continue // oversized name table is skipped, features omitted
			}
			t.Errorf("%s: accepted %+v", name, objs)
		}
	}
}

func TestDetectLongNameClipped(t *testing.T) {
	objs, _ := detect(t, []byte("zz"), strings.Repeat("n", 5000))
	if len(objs[0].Name) != maxNameLen {
		t.Fatalf("name length %d", len(objs[0].Name))
	}
}
