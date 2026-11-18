package siteexport

import (
	"archive/zip"
	"crypto/sha256"
	"encoding/hex"
	"encoding/json"
	"hash"
	"io"
	"strings"
	"time"
)

// archive wraps the ZIP writer and records a checksum, size and row count for
// every entry so the manifest can be written last.
type archive struct {
	zw    *zip.Writer
	files []*entry
}

// entry is one archive file being written.
type entry struct {
	w       io.Writer
	path    string
	header  []string
	rows    int
	bytes   int64
	sum     hash.Hash
	isTable bool
}

func (e *entry) Write(p []byte) (int, error) {
	n, err := e.w.Write(p)
	e.sum.Write(p[:n])
	e.bytes += int64(n)
	return n, err
}

// Create starts a new entry; the previous one is complete once it is called.
func (a *archive) Create(name string) (*entry, error) {
	w, err := a.zw.Create(name)
	if err != nil {
		return nil, err
	}
	e := &entry{w: w, path: name, sum: sha256.New(), isTable: strings.HasSuffix(name, ".csv")}
	a.files = append(a.files, e)
	return e, nil
}

// ManifestFile is one entry of manifest.json.
type ManifestFile struct {
	Path    string   `json:"path"`
	Format  string   `json:"format"`
	Rows    *int     `json:"rows,omitempty"`
	Bytes   int64    `json:"bytes"`
	SHA256  string   `json:"sha256"`
	Columns []string `json:"columns,omitempty"`
	Schema  string   `json:"schema,omitempty"`
}

// Manifest is manifest.json: the entry point of every export.
type Manifest struct {
	SchemaVersion int            `json:"schema_version"`
	GeneratedAt   string         `json:"generated_at"`
	Generator     string         `json:"generator"`
	Range         ManifestRange  `json:"range"`
	Site          ManifestSite   `json:"site"`
	Files         []ManifestFile `json:"files"`
}

type ManifestRange struct {
	From string `json:"from"`
	To   string `json:"to"`
}

type ManifestSite struct {
	ID       int32  `json:"id"`
	PublicID string `json:"public_id"`
	Name     string `json:"name"`
	Kind     string `json:"kind"`
	Timezone string `json:"timezone"`
}

func (a *archive) manifest(now time.Time, site ManifestSite, from, to time.Time) Manifest {
	m := Manifest{
		SchemaVersion: SchemaVersion, GeneratedAt: now.UTC().Format(time.RFC3339), Generator: "supermetry",
		Range: ManifestRange{From: date(from), To: date(to)}, Site: site, Files: []ManifestFile{},
	}
	for _, e := range a.files {
		f := ManifestFile{Path: e.path, Format: "binary", Bytes: e.bytes, SHA256: hex.EncodeToString(e.sum.Sum(nil))}
		if e.isTable {
			rows := e.rows
			f.Format, f.Rows, f.Columns = "csv", &rows, e.header
			if t, ok := TableFor(e.path); ok {
				f.Schema = SchemaFile(t)
			}
		}
		m.Files = append(m.Files, f)
	}
	return m
}

func (a *archive) writeManifest(m Manifest) error {
	w, err := a.zw.Create(ManifestName)
	if err != nil {
		return err
	}
	enc := json.NewEncoder(w)
	enc.SetIndent("", "  ")
	return enc.Encode(m)
}
