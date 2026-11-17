package debugfile

import (
	"bytes"
	"encoding/binary"
)

// elfSpec describes a tiny synthetic 64-bit little-endian ELF.
type elfSpec struct {
	machine    uint16
	buildID    []byte // nil: no PT_NOTE
	text       []byte // nil: no .text
	symtab     bool
	debugInfo  bool
	debugNoBit bool
}

func buildELF(s elfSpec) []byte {
	le := binary.LittleEndian
	var body bytes.Buffer
	// ELF header (64) + one program header (56) come first; data after.
	const hdrLen, phLen = 64, 56
	body.Write(make([]byte, hdrLen+phLen))

	var note []byte
	if s.buildID != nil {
		note = make([]byte, 0, 64)
		note = le.AppendUint32(note, 4)
		note = le.AppendUint32(note, uint32(len(s.buildID)))
		note = le.AppendUint32(note, 3)
		note = append(note, "GNU\x00"...)
		note = append(note, s.buildID...)
		for len(note)%4 != 0 {
			note = append(note, 0)
		}
	}
	noteOff := body.Len()
	body.Write(note)
	textOff := body.Len()
	body.Write(s.text)

	type sec struct {
		name      string
		typ       uint32
		off, size uint64
		nameOff   uint32
	}
	secs := []sec{{}} // null section
	if s.text != nil {
		secs = append(secs, sec{name: ".text", typ: 1, off: uint64(textOff), size: uint64(len(s.text))})
	}
	if s.symtab {
		secs = append(secs, sec{name: ".symtab", typ: 2, off: uint64(textOff), size: 24})
	}
	if s.debugInfo {
		typ := uint32(1)
		if s.debugNoBit {
			typ = 8
		}
		secs = append(secs, sec{name: ".debug_info", typ: typ, off: uint64(textOff), size: 16})
	}
	var strtab bytes.Buffer
	strtab.WriteByte(0)
	for i := range secs {
		if secs[i].name != "" {
			secs[i].nameOff = uint32(strtab.Len())
			strtab.WriteString(secs[i].name)
			strtab.WriteByte(0)
		}
	}
	shstrName := uint32(strtab.Len())
	strtab.WriteString(".shstrtab\x00")
	strOff := body.Len()
	body.Write(strtab.Bytes())
	secs = append(secs, sec{typ: 3, off: uint64(strOff), size: uint64(strtab.Len()), nameOff: shstrName})
	shoff := body.Len()
	for _, c := range secs {
		e := make([]byte, 64)
		le.PutUint32(e[0:], c.nameOff)
		le.PutUint32(e[4:], c.typ)
		le.PutUint64(e[24:], c.off)
		le.PutUint64(e[32:], c.size)
		body.Write(e)
	}
	out := body.Bytes()
	copy(out, "\x7fELF\x02\x01\x01")
	le.PutUint16(out[16:], 3)
	le.PutUint16(out[18:], s.machine)
	le.PutUint64(out[32:], hdrLen)
	le.PutUint64(out[40:], uint64(shoff))
	le.PutUint16(out[52:], hdrLen)
	le.PutUint16(out[54:], phLen)
	phnum := uint16(0)
	if note != nil {
		phnum = 1
		ph := out[hdrLen:]
		le.PutUint32(ph[0:], 4)
		le.PutUint64(ph[8:], uint64(noteOff))
		le.PutUint64(ph[32:], uint64(len(note)))
	}
	le.PutUint16(out[56:], phnum)
	le.PutUint16(out[58:], 64)
	le.PutUint16(out[60:], uint16(len(secs)))
	le.PutUint16(out[62:], uint16(len(secs)-1))
	return out
}

// machoSpec describes a tiny synthetic 64-bit little-endian Mach-O.
type machoSpec struct {
	cpu, sub uint32
	uuid     [16]byte
	noUUID   bool
	dwarf    bool
	symtab   bool
	ncmds    uint32 // override; 0 = real
}

func buildMacho(s machoSpec) []byte {
	le := binary.LittleEndian
	var cmds bytes.Buffer
	n := uint32(0)
	if !s.noUUID {
		c := make([]byte, 24)
		le.PutUint32(c, 0x1b)
		le.PutUint32(c[4:], 24)
		copy(c[8:], s.uuid[:])
		cmds.Write(c)
		n++
	}
	if s.symtab {
		c := make([]byte, 24)
		le.PutUint32(c, 2)
		le.PutUint32(c[4:], 24)
		le.PutUint32(c[12:], 5)
		cmds.Write(c)
		n++
	}
	if s.dwarf {
		c := make([]byte, 72+80)
		le.PutUint32(c, 0x19)
		le.PutUint32(c[4:], uint32(len(c)))
		copy(c[8:], "__DWARF")
		le.PutUint32(c[64:], 1)
		copy(c[72:], "__debug_info")
		copy(c[88:], "__DWARF")
		le.PutUint64(c[72+40:], 100)
		cmds.Write(c)
		n++
	}
	if s.ncmds != 0 {
		n = s.ncmds
	}
	h := make([]byte, 32)
	le.PutUint32(h, 0xfeedfacf)
	le.PutUint32(h[4:], s.cpu)
	le.PutUint32(h[8:], s.sub)
	le.PutUint32(h[12:], 10) // MH_DSYM
	le.PutUint32(h[16:], n)
	le.PutUint32(h[20:], uint32(cmds.Len()))
	return append(h, cmds.Bytes()...)
}

// buildFat wraps slices in a fat header (32-bit offsets).
func buildFat(slices ...[]byte) []byte {
	be := binary.BigEndian
	hdr := make([]byte, 8+20*len(slices))
	be.PutUint32(hdr, 0xcafebabe)
	be.PutUint32(hdr[4:], uint32(len(slices)))
	var data []byte
	off := (len(hdr) + 4095) &^ 4095
	for i, sl := range slices {
		e := hdr[8+20*i:]
		cpu := binary.LittleEndian.Uint32(sl[4:])
		be.PutUint32(e, cpu)
		be.PutUint32(e[8:], uint32(off+len(data)))
		be.PutUint32(e[12:], uint32(len(sl)))
		data = append(data, sl...)
		for len(data)%4096 != 0 {
			data = append(data, 0)
		}
	}
	out := append(hdr, make([]byte, off-len(hdr))...)
	return append(out, data...)
}
