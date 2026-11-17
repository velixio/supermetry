package debugfile

import (
	"bytes"
	"testing"
)

// countingReader records the largest single read and the total bytes read, to
// prove Detect never allocates or reads more than its caps regardless of what
// the headers claim.
type countingReader struct {
	r     *bytes.Reader
	total int64
	max   int
}

func (c *countingReader) ReadAt(p []byte, off int64) (int, error) {
	c.max = max(c.max, len(p))
	c.total += int64(len(p))
	return c.r.ReadAt(p, off)
}

func FuzzDetect(f *testing.F) {
	elf := buildELF(elfSpec{machine: 183, buildID: []byte("0123456789abcdef0123"), text: []byte("code"), symtab: true, debugInfo: true})
	macho := buildMacho(machoSpec{cpu: 0x0100000c, uuid: [16]byte{1, 2, 3}, dwarf: true, symtab: true})
	fat := buildFat(macho, buildMacho(machoSpec{cpu: 0x01000007, uuid: [16]byte{9}}))
	seeds := [][]byte{
		elf, macho, fat, elf[:len(elf)/2], macho[:40], fat[:100],
		[]byte("MODULE Linux x86_64 DE7FADA356F73CE7B81E0B6E964459C10 a\n"),
		[]byte("# c\ncom.a.B -> a.b:\n"), []byte("SYSB"), []byte("MZ"), nil,
	}
	for _, s := range seeds {
		f.Add(s, "mapping.txt")
	}
	f.Add(macho, "/proguard/6a1b8c0e-3d5f-3e2a-9b47-0c1d2e3f4a5b.txt")
	f.Fuzz(func(t *testing.T, data []byte, name string) {
		cr := &countingReader{r: bytes.NewReader(data)}
		objs, err := Detect(cr, int64(len(data)), name)
		if err == nil && len(objs) == 0 {
			t.Fatal("no objects and no error")
		}
		if len(objs) > maxObjects {
			t.Fatalf("%d objects", len(objs))
		}
		if cr.max > maxTable {
			t.Fatalf("single read of %d bytes", cr.max)
		}
		for _, o := range objs {
			if len(o.Name) > maxNameLen || len(o.Arch) > 32 || len(o.DebugID) > 64 || len(o.CodeID) > 128 {
				t.Fatalf("unbounded field in %+v", o)
			}
		}
	})
}
