package release_test

import (
	"archive/tar"
	"archive/zip"
	"bytes"
	"compress/gzip"
	"context"
	"crypto/sha256"
	"fmt"
	"os"
	"strings"
	"testing"

	"supermetry/internal/updater/release"
)

func TestValidateArchiveAcceptsOfficialTarGzipAndZipLayouts(t *testing.T) {
	t.Parallel()
	tarArchive := makeTarGzip(t,
		tarFile{name: "supermetry", body: []byte("binary")},
		tarFile{name: "LICENSE", body: []byte("license")},
		tarFile{name: "NOTICE", body: []byte("notice")},
		tarFile{name: "supermetry.service", body: []byte("unit")},
	)
	zipArchive := makeZip(t,
		zipFile{name: "supermetry.exe", body: []byte("binary")},
		zipFile{name: "LICENSE", body: []byte("license")},
		zipFile{name: "NOTICE", body: []byte("notice")},
	)

	for _, tt := range []struct {
		name     string
		archive  []byte
		artifact release.Artifact
	}{
		{name: "tar gzip", archive: tarArchive, artifact: archiveArtifact("supermetry_1.2.0_linux_amd64.tar.gz", "linux", tarArchive)},
		{name: "zip", archive: zipArchive, artifact: archiveArtifact("supermetry_1.2.0_windows_amd64.zip", "windows", zipArchive)},
	} {
		t.Run(tt.name, func(t *testing.T) {
			if err := release.ValidateArchive(context.Background(), bytes.NewReader(tt.archive), tt.artifact); err != nil {
				t.Fatal(err)
			}
		})
	}
}

func TestValidateArchiveRejectsTraversalLinksDuplicatesUnexpectedAndBombs(t *testing.T) {
	t.Parallel()
	tests := []struct {
		name    string
		archive func(*testing.T) []byte
		want    string
	}{
		{name: "traversal", archive: func(t *testing.T) []byte { return makeTarGzip(t, tarFile{name: "../supermetry", body: []byte("bad")}) }, want: "unsafe"},
		{name: "symlink", archive: func(t *testing.T) []byte {
			return makeTarGzip(t, tarFile{name: "supermetry", kind: tar.TypeSymlink, link: "/bin/sh"})
		}, want: "regular"},
		{name: "duplicate", archive: func(t *testing.T) []byte {
			return makeTarGzip(t, tarFile{name: "supermetry", body: []byte("one")}, tarFile{name: "supermetry", body: []byte("two")})
		}, want: "duplicate"},
		{name: "unexpected", archive: func(t *testing.T) []byte { return makeTarGzip(t, tarFile{name: "install.sh", body: []byte("bad")}) }, want: "unexpected"},
		{name: "decompression bomb", archive: makeOversizedTarGzip, want: "uncompressed"},
	}
	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			archive := tt.archive(t)
			artifact := archiveArtifact("supermetry_1.2.0_linux_amd64.tar.gz", "linux", archive)
			err := release.ValidateArchive(context.Background(), bytes.NewReader(archive), artifact)
			if err == nil || !strings.Contains(strings.ToLower(err.Error()), tt.want) {
				t.Fatalf("ValidateArchive() error = %v, want %q", err, tt.want)
			}
		})
	}
}

func TestValidateArchiveRejectsZipLinksAndDuplicateEntries(t *testing.T) {
	t.Parallel()
	for _, tt := range []struct {
		name    string
		entries []zipFile
		want    string
	}{
		{name: "symlink", entries: []zipFile{{name: "supermetry.exe", body: []byte("target"), mode: os.ModeSymlink | 0o777}}, want: "regular"},
		{name: "duplicate", entries: []zipFile{{name: "supermetry.exe"}, {name: "supermetry.exe"}}, want: "duplicate"},
	} {
		t.Run(tt.name, func(t *testing.T) {
			archive := makeZip(t, tt.entries...)
			artifact := archiveArtifact("supermetry_1.2.0_windows_amd64.zip", "windows", archive)
			err := release.ValidateArchive(context.Background(), bytes.NewReader(archive), artifact)
			if err == nil || !strings.Contains(strings.ToLower(err.Error()), tt.want) {
				t.Fatalf("ValidateArchive() error = %v, want %q", err, tt.want)
			}
		})
	}
}

func TestValidateArchiveHonorsCancellation(t *testing.T) {
	t.Parallel()
	archive := makeTarGzip(t, tarFile{name: "supermetry", body: []byte("binary")})
	ctx, cancel := context.WithCancel(context.Background())
	cancel()
	err := release.ValidateArchive(ctx, bytes.NewReader(archive), archiveArtifact("supermetry_1.2.0_linux_amd64.tar.gz", "linux", archive))
	if err == nil || !strings.Contains(strings.ToLower(err.Error()), "canceled") {
		t.Fatalf("ValidateArchive() error = %v, want cancellation", err)
	}
}

type tarFile struct {
	name string
	body []byte
	kind byte
	link string
}

func makeTarGzip(t *testing.T, files ...tarFile) []byte {
	t.Helper()
	var result bytes.Buffer
	gzipWriter := gzip.NewWriter(&result)
	tarWriter := tar.NewWriter(gzipWriter)
	for _, file := range files {
		kind := file.kind
		if kind == 0 {
			kind = tar.TypeReg
		}
		size := int64(len(file.body))
		if kind != tar.TypeReg && kind != tar.TypeRegA {
			size = 0
		}
		if err := tarWriter.WriteHeader(&tar.Header{Name: file.name, Mode: 0o755, Size: size, Typeflag: kind, Linkname: file.link}); err != nil {
			t.Fatal(err)
		}
		if size > 0 {
			if _, err := tarWriter.Write(file.body); err != nil {
				t.Fatal(err)
			}
		}
	}
	if err := tarWriter.Close(); err != nil {
		t.Fatal(err)
	}
	if err := gzipWriter.Close(); err != nil {
		t.Fatal(err)
	}
	return result.Bytes()
}

func makeOversizedTarGzip(t *testing.T) []byte {
	t.Helper()
	var result bytes.Buffer
	gzipWriter := gzip.NewWriter(&result)
	tarWriter := tar.NewWriter(gzipWriter)
	if err := tarWriter.WriteHeader(&tar.Header{Name: "supermetry", Mode: 0o755, Size: (512 << 20) + 1, Typeflag: tar.TypeReg}); err != nil {
		t.Fatal(err)
	}
	_ = tarWriter.Close()
	if err := gzipWriter.Close(); err != nil {
		t.Fatal(err)
	}
	return result.Bytes()
}

type zipFile struct {
	name string
	body []byte
	mode os.FileMode
}

func makeZip(t *testing.T, files ...zipFile) []byte {
	t.Helper()
	var result bytes.Buffer
	writer := zip.NewWriter(&result)
	for _, file := range files {
		header := &zip.FileHeader{Name: file.name, Method: zip.Deflate}
		if file.mode != 0 {
			header.SetMode(file.mode)
		}
		entry, err := writer.CreateHeader(header)
		if err != nil {
			t.Fatal(err)
		}
		if _, err := entry.Write(file.body); err != nil {
			t.Fatal(err)
		}
	}
	if err := writer.Close(); err != nil {
		t.Fatal(err)
	}
	return result.Bytes()
}

func archiveArtifact(name, goos string, contents []byte) release.Artifact {
	digest := sha256.Sum256(contents)
	return release.Artifact{Name: name, OS: goos, Arch: "amd64", Size: int64(len(contents)), SHA256: fmt.Sprintf("%x", digest)}
}
