package siteexport_test

import (
	"archive/zip"
	"bytes"
	"crypto/sha256"
	"encoding/hex"
	"encoding/json"
	"io"
	"os"
	"path/filepath"
	"slices"
	"strings"
	"testing"

	"supermetry/internal/siteexport"
)

const schemaDir = "../../docs/export-schemas"

// TestPublishedSchemasMatchRegistry keeps docs/export-schemas in step with the
// table registry. Run with UPDATE_EXPORT_SCHEMAS=1 to regenerate the files.
func TestPublishedSchemasMatchRegistry(t *testing.T) {
	want := siteexport.AllSchemas()
	if os.Getenv("UPDATE_EXPORT_SCHEMAS") != "" {
		if err := os.MkdirAll(schemaDir, 0o755); err != nil {
			t.Fatal(err)
		}
		for name, body := range want {
			if err := os.WriteFile(filepath.Join(schemaDir, name), body, 0o644); err != nil {
				t.Fatal(err)
			}
		}
	}
	entries, err := os.ReadDir(schemaDir)
	if err != nil {
		t.Fatal(err)
	}
	seen := map[string]bool{}
	for _, e := range entries {
		seen[e.Name()] = true
		body, ok := want[e.Name()]
		if !ok {
			t.Errorf("%s is published but not in the registry; delete it or UPDATE_EXPORT_SCHEMAS=1", e.Name())
			continue
		}
		got, err := os.ReadFile(filepath.Join(schemaDir, e.Name()))
		if err != nil {
			t.Fatal(err)
		}
		if !bytes.Equal(got, body) {
			t.Errorf("%s is out of date; run UPDATE_EXPORT_SCHEMAS=1 go test ./internal/siteexport -run PublishedSchemas", e.Name())
		}
		if !json.Valid(got) {
			t.Errorf("%s is not valid JSON", e.Name())
		}
	}
	for name := range want {
		if !seen[name] {
			t.Errorf("%s missing from docs/export-schemas", name)
		}
	}
}

// TestExportMatchesRegistryAndManifest exports an app site (the widest set of
// files) and checks that every CSV header equals the registry, every registry
// table is exported, and manifest rows, sizes and checksums match the content.
func TestExportMatchesRegistryAndManifest(t *testing.T) {
	e, ctx := newAppExport(t)
	var out bytes.Buffer
	if err := e.Write(ctx, &out, 7, day("2026-10-01"), day("2026-10-03")); err != nil {
		t.Fatal(err)
	}
	zr, err := zip.NewReader(bytes.NewReader(out.Bytes()), int64(out.Len()))
	if err != nil {
		t.Fatal(err)
	}
	if last := zr.File[len(zr.File)-1].Name; last != siteexport.ManifestName {
		t.Fatalf("last entry = %s, want manifest.json", last)
	}
	var manifest siteexport.Manifest
	contents := map[string][]byte{}
	for _, f := range zr.File {
		rc, err := f.Open()
		if err != nil {
			t.Fatal(err)
		}
		b, _ := io.ReadAll(rc)
		rc.Close()
		contents[f.Name] = b
	}
	if err := json.Unmarshal(contents[siteexport.ManifestName], &manifest); err != nil {
		t.Fatal(err)
	}
	if manifest.SchemaVersion != siteexport.SchemaVersion || manifest.Generator != "supermetry" {
		t.Fatalf("manifest header = %+v", manifest)
	}
	if manifest.Site.ID != 7 || manifest.Site.Kind != "app" || manifest.Range.From != "2026-10-01" || manifest.Range.To != "2026-10-03" {
		t.Fatalf("manifest site/range = %+v %+v", manifest.Site, manifest.Range)
	}
	listed := map[string]siteexport.ManifestFile{}
	for _, f := range manifest.Files {
		listed[f.Path] = f
	}
	if len(listed) != len(zr.File)-1 {
		t.Fatalf("manifest lists %d files, archive has %d besides the manifest", len(listed), len(zr.File)-1)
	}
	for name, body := range contents {
		if name == siteexport.ManifestName {
			continue
		}
		f, ok := listed[name]
		if !ok {
			t.Fatalf("%s missing from manifest", name)
		}
		sum := sha256.Sum256(body)
		if f.SHA256 != hex.EncodeToString(sum[:]) || f.Bytes != int64(len(body)) {
			t.Errorf("%s: manifest sha/bytes %s/%d do not match content", name, f.SHA256, f.Bytes)
		}
		if f.Format == "csv" {
			rows := readCSV(t, mustFile(t, zr, name))
			if f.Rows == nil || *f.Rows != len(rows)-1 {
				t.Errorf("%s: manifest rows %v, want %d", name, f.Rows, len(rows)-1)
			}
			table, ok := siteexport.TableFor(name)
			if !ok {
				t.Errorf("%s is exported but has no schema in the registry: add it to siteexport.Tables and regenerate docs/export-schemas", name)
				continue
			}
			header := rows[0]
			want := make([]string, len(table.Columns))
			for i, c := range table.Columns {
				want[i] = c.Name
			}
			if !slices.Equal(header, want) {
				t.Errorf("%s header %v differs from schema columns %v: update siteexport.Tables and docs/export-schemas", name, header, want)
			}
			if !slices.Equal(f.Columns, header) || f.Schema != siteexport.SchemaFile(table) {
				t.Errorf("%s: manifest columns/schema = %v / %s", name, f.Columns, f.Schema)
			}
		} else if f.Format != "binary" || !strings.HasPrefix(name, "app_attachments/") {
			t.Errorf("unexpected non-CSV entry %s (%s)", name, f.Format)
		}
	}
	for _, table := range siteexport.Tables() {
		if _, ok := listed[table.File]; !ok {
			t.Errorf("registry table %s was not exported for an app site", table.File)
		}
	}
}

func mustFile(t *testing.T, zr *zip.Reader, name string) *zip.File {
	t.Helper()
	for _, f := range zr.File {
		if f.Name == name {
			return f
		}
	}
	t.Fatalf("%s not in archive", name)
	return nil
}
