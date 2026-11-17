package sourcemap

import (
	"archive/zip"
	"bytes"
	"encoding/binary"
	"encoding/json"
	"errors"
	"fmt"
	"io"
	"slices"
	"strings"
)

// Caps bound what a bundle may declare and what reading it may produce.
type Caps struct {
	MaxTotal int64 // sum of uncompressed sizes
	MaxFiles int
	MaxFile  int64 // largest single uncompressed file
}

// DefaultCaps are the limits from the phase plan: 512 MiB total, 10,000
// files, 64 MiB per file.
func DefaultCaps() Caps {
	return Caps{MaxTotal: 512 << 20, MaxFiles: 10000, MaxFile: 64 << 20}
}

// ErrCap is wrapped by every error caused by exceeding a cap.
var ErrCap = errors.New("sourcemap: bundle exceeds cap")

const maxManifest = 8 << 20

// BundleFile is one manifest entry that exists in the archive.
type BundleFile struct {
	Path         string // path inside the ZIP
	URL          string
	Type         string // source, minified_source, source_map, indexed_ram_bundle (lowercase), or the lowercased unknown value
	DebugID      string // normalised, from the debug-id header
	SourceMapRef string // sourcemap header
	Size         uint64 // declared uncompressed size
}

// Bundle is a parsed artifact bundle.
type Bundle struct {
	Org, Project, Release, Dist string
	Files                       []BundleFile

	zr   *zip.Reader
	caps Caps
	idx  map[string]*zip.File
}

type rawManifest struct {
	Org        string                  `json:"org"`
	Project    string                  `json:"project"`
	Release    string                  `json:"release"`
	Dist       string                  `json:"dist"`
	Attributes *rawAttrs               `json:"attributes"`
	Files      map[string]rawManifestF `json:"files"`
}

type rawAttrs struct {
	Org     string `json:"org"`
	Project string `json:"project"`
	Release string `json:"release"`
	Dist    string `json:"dist"`
}

type rawManifestF struct {
	URL     string         `json:"url"`
	Type    string         `json:"type"`
	Headers map[string]any `json:"headers"`
}

// ParseBundle reads an artifact bundle: a ZIP, optionally preceded by the
// 8-byte header "SYSB" + little-endian version 2. Only the manifest is
// decompressed. Declared sizes are checked against caps; ReadFile enforces
// the per-file cap on the bytes actually produced.
func ParseBundle(r io.ReaderAt, size int64, caps Caps) (*Bundle, error) {
	if size < 22 {
		return nil, errors.New("sourcemap: bundle too small")
	}
	var head [8]byte
	if _, err := r.ReadAt(head[:], 0); err == nil && string(head[:4]) == "SYSB" {
		if v := binary.LittleEndian.Uint32(head[4:]); v != 2 {
			return nil, fmt.Errorf("sourcemap: unsupported bundle version %d", v)
		}
	}
	zr, err := zip.NewReader(r, size)
	if err != nil {
		return nil, fmt.Errorf("sourcemap: bundle: %w", err)
	}
	if len(zr.File) > caps.MaxFiles {
		return nil, fmt.Errorf("%w: %d files", ErrCap, len(zr.File))
	}
	var total uint64
	idx := make(map[string]*zip.File, len(zr.File))
	for _, f := range zr.File {
		if f.UncompressedSize64 > uint64(caps.MaxFile) {
			return nil, fmt.Errorf("%w: file %q declares %d bytes", ErrCap, f.Name, f.UncompressedSize64)
		}
		total += f.UncompressedSize64
		if total > uint64(caps.MaxTotal) {
			return nil, fmt.Errorf("%w: total uncompressed size", ErrCap)
		}
		if _, dup := idx[f.Name]; !dup {
			idx[f.Name] = f
		}
	}
	mf := idx["manifest.json"]
	if mf == nil {
		return nil, errors.New("sourcemap: bundle has no manifest.json")
	}
	b := &Bundle{zr: zr, caps: caps, idx: idx}
	mb, err := b.readLimited(mf, min(caps.MaxFile, maxManifest))
	if err != nil {
		return nil, fmt.Errorf("sourcemap: manifest: %w", err)
	}
	var m rawManifest
	if err := json.Unmarshal(mb, &m); err != nil {
		return nil, fmt.Errorf("sourcemap: manifest: %w", err)
	}
	b.Org, b.Project, b.Release, b.Dist = m.Org, m.Project, m.Release, m.Dist
	if a := m.Attributes; a != nil {
		b.Org, b.Project = firstNonEmpty(a.Org, b.Org), firstNonEmpty(a.Project, b.Project)
		b.Release, b.Dist = firstNonEmpty(a.Release, b.Release), firstNonEmpty(a.Dist, b.Dist)
	}
	if len(m.Files) > caps.MaxFiles {
		return nil, fmt.Errorf("%w: %d manifest entries", ErrCap, len(m.Files))
	}
	for path, e := range m.Files {
		zf := idx[path]
		if zf == nil {
			continue
		}
		bf := BundleFile{Path: path, URL: e.URL, Type: normType(e.Type), Size: zf.UncompressedSize64}
		for k, v := range e.Headers {
			s, _ := v.(string)
			switch strings.ToLower(k) {
			case "debug-id", "debug_id":
				bf.DebugID = NormalizeDebugID(s)
			case "sourcemap":
				bf.SourceMapRef = s
			}
		}
		b.Files = append(b.Files, bf)
	}
	sortFiles(b.Files)
	return b, nil
}

func sortFiles(f []BundleFile) {
	slices.SortFunc(f, func(a, b BundleFile) int { return strings.Compare(a.Path, b.Path) })
}

func normType(t string) string {
	t = strings.ToLower(strings.NewReplacer("_", "", "-", "", " ", "").Replace(t))
	switch t {
	case "source":
		return "source"
	case "minifiedsource":
		return "minified_source"
	case "sourcemap":
		return "source_map"
	case "indexedrambundle":
		return "indexed_ram_bundle"
	}
	return t
}

// ReadFile returns the contents of a file of the archive. The per-file cap
// applies to the bytes actually decompressed, whatever the header declared.
func (b *Bundle) ReadFile(path string) ([]byte, error) {
	f := b.idx[path]
	if f == nil {
		return nil, fmt.Errorf("sourcemap: no file %q in bundle", path)
	}
	return b.readLimited(f, b.caps.MaxFile)
}

func (b *Bundle) readLimited(f *zip.File, limit int64) ([]byte, error) {
	rc, err := f.Open()
	if err != nil {
		return nil, err
	}
	defer rc.Close()
	var buf bytes.Buffer
	n, err := io.Copy(&buf, io.LimitReader(rc, limit+1))
	if err != nil {
		return nil, err
	}
	if n > limit {
		return nil, fmt.Errorf("%w: file %q", ErrCap, f.Name)
	}
	return buf.Bytes(), nil
}
