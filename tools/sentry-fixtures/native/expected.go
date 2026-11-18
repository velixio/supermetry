//go:build ignore

// expected.go writes the fat Mach-O fixture and expected.json, the oracle
// answers the symbol cache tests compare against: go tool addr2line for the
// Go objects and binutils addr2line -i for the C library.
//
// Usage: go run expected.go <testdata dir>
package main

import (
	"bytes"
	"debug/elf"
	"debug/macho"
	"encoding/binary"
	"encoding/json"
	"fmt"
	"os"
	"os/exec"
	"path/filepath"
	"sort"
	"strconv"
	"strings"
)

type frame struct {
	Function string `json:"function"`
	File     string `json:"file"`
	Line     int    `json:"line"`
}

type kase struct {
	Addr   uint64  `json:"addr"`
	Frames []frame `json:"frames"` // innermost first
}

type object struct {
	File  string `json:"file"`
	Base  uint64 `json:"base"`
	Cases []kase `json:"cases"`
}

func must(err error) {
	if err != nil {
		fmt.Fprintln(os.Stderr, err)
		os.Exit(1)
	}
}

func main() {
	dir := os.Args[1]
	var objs []object
	for _, n := range []string{"fix-linux-arm64", "fix-linux-amd64", "fix-darwin-arm64", "fix-darwin-amd64"} {
		objs = append(objs, goObject(dir, n))
	}
	objs = append(objs, cObject(dir, "libfix.so"))
	must(writeFat(dir, "fix-darwin-fat", "fix-darwin-amd64", "fix-darwin-arm64"))
	b, err := json.MarshalIndent(map[string]any{"objects": objs}, "", " ")
	must(err)
	must(os.WriteFile(filepath.Join(dir, "expected.json"), append(b, '\n'), 0o644))
}

type fn struct {
	name       string
	start, end uint64
}

func lowestLoad(f *elf.File) uint64 {
	base := ^uint64(0)
	for _, p := range f.Progs {
		if p.Type == elf.PT_LOAD && p.Vaddr < base {
			base = p.Vaddr
		}
	}
	return base
}

func goObject(dir, name string) object {
	path := filepath.Join(dir, name)
	var fns []fn
	var base uint64
	if strings.Contains(name, "darwin") {
		f, err := macho.Open(path)
		must(err)
		base = f.Segment("__TEXT").Addr
		syms := f.Symtab.Syms
		sort.Slice(syms, func(i, j int) bool { return syms[i].Value < syms[j].Value })
		for i, s := range syms {
			n := strings.TrimPrefix(s.Name, "_")
			if strings.HasPrefix(n, "main.") && s.Value != 0 && i+1 < len(syms) {
				fns = append(fns, fn{n, s.Value, syms[i+1].Value})
			}
		}
	} else {
		f, err := elf.Open(path)
		must(err)
		base = lowestLoad(f)
		syms, err := f.Symbols()
		must(err)
		for _, s := range syms {
			if strings.HasPrefix(s.Name, "main.") && s.Size > 0 && elf.ST_TYPE(s.Info) == elf.STT_FUNC {
				fns = append(fns, fn{s.Name, s.Value, s.Value + s.Size})
			}
		}
	}
	sort.Slice(fns, func(i, j int) bool { return fns[i].start < fns[j].start })
	var addrs []uint64
	for _, f := range fns {
		off := uint64(8)
		if f.end-f.start <= off {
			off = 1
		}
		addrs = append(addrs, f.start+off)
		if f.end-f.start > 40 {
			addrs = append(addrs, f.start+32)
		}
	}
	if len(addrs) > 8 {
		addrs = addrs[:8]
	}
	var in bytes.Buffer
	for _, a := range addrs {
		fmt.Fprintf(&in, "%x\n", a)
	}
	cmd := exec.Command("go", "tool", "addr2line", path)
	cmd.Stdin = &in
	out, err := cmd.Output()
	must(err)
	lines := strings.Split(strings.TrimSpace(string(out)), "\n")
	o := object{File: name, Base: base}
	for i, a := range addrs {
		file, line := splitFileLine(lines[2*i+1])
		o.Cases = append(o.Cases, kase{a, []frame{{lines[2*i], file, line}}})
	}
	return o
}

func splitFileLine(s string) (string, int) {
	i := strings.LastIndex(s, ":")
	n, err := strconv.Atoi(strings.Fields(s[i+1:])[0])
	must(err)
	return s[:i], n
}

func cObject(dir, name string) object {
	path := filepath.Join(dir, name)
	f, err := elf.Open(path)
	must(err)
	syms, err := f.Symbols()
	must(err)
	var addrs []uint64
	for _, s := range syms {
		if strings.HasPrefix(s.Name, "fix_") && s.Size > 0 {
			for a := s.Value; a < s.Value+s.Size; a++ {
				addrs = append(addrs, a)
			}
		}
	}
	args := []string{"-a", "-f", "-i", "-C", "-e", path}
	for _, a := range addrs {
		args = append(args, fmt.Sprintf("%x", a))
	}
	out, err := exec.Command("addr2line", args...).Output()
	must(err)
	var all []kase
	pendingFn := ""
	for _, ln := range strings.Split(strings.TrimSpace(string(out)), "\n") {
		if strings.HasPrefix(ln, "0x") {
			a, err := strconv.ParseUint(ln[2:], 16, 64)
			must(err)
			all = append(all, kase{Addr: a})
			pendingFn = ""
			continue
		}
		k := &all[len(all)-1]
		if pendingFn == "" {
			pendingFn = ln
			continue
		}
		file, line := splitFileLine(ln)
		k.Frames = append(k.Frames, frame{pendingFn, file, line})
		pendingFn = ""
	}
	o := object{File: name, Base: lowestLoad(f)}
	var prev string
	for _, k := range all {
		key, _ := json.Marshal(k.Frames)
		if string(key) != prev {
			o.Cases = append(o.Cases, k)
			prev = string(key)
		}
	}
	return o
}

func writeFat(dir, out string, thin ...string) error {
	type arch struct {
		cpu, sub uint32
		data     []byte
		off      uint32
	}
	var as []arch
	for _, n := range thin {
		d, err := os.ReadFile(filepath.Join(dir, n))
		if err != nil {
			return err
		}
		as = append(as, arch{cpu: binary.LittleEndian.Uint32(d[4:]), sub: binary.LittleEndian.Uint32(d[8:]), data: d})
	}
	const align = 1 << 14
	pos := uint32(8 + 20*len(as))
	for i := range as {
		pos = (pos + align - 1) &^ (align - 1)
		as[i].off = pos
		pos += uint32(len(as[i].data))
	}
	var b bytes.Buffer
	be := binary.BigEndian
	hdr := make([]byte, 8)
	be.PutUint32(hdr, 0xcafebabe)
	be.PutUint32(hdr[4:], uint32(len(as)))
	b.Write(hdr)
	for _, a := range as {
		e := make([]byte, 20)
		be.PutUint32(e, a.cpu)
		be.PutUint32(e[4:], a.sub)
		be.PutUint32(e[8:], a.off)
		be.PutUint32(e[12:], uint32(len(a.data)))
		be.PutUint32(e[16:], 14)
		b.Write(e)
	}
	for _, a := range as {
		b.Write(make([]byte, int(a.off)-b.Len()))
		b.Write(a.data)
	}
	return os.WriteFile(filepath.Join(dir, out), b.Bytes(), 0o644)
}
