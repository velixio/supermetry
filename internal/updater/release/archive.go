package release

import (
	"archive/tar"
	"archive/zip"
	"compress/gzip"
	"context"
	"errors"
	"fmt"
	"io"
	"path"
	"strings"
)

const maxUncompressedArchiveBytes = int64(512 << 20)

func ValidateArchive(ctx context.Context, archive io.ReaderAt, artifact Artifact) error {
	if archive == nil {
		return errors.New("release archive is required")
	}
	if err := validateArtifact(artifact); err != nil {
		return err
	}
	if err := ctx.Err(); err != nil {
		return err
	}
	required, err := requiredArchiveFiles(artifact)
	if err != nil {
		return err
	}
	switch {
	case strings.HasSuffix(artifact.Name, ".tar.gz"):
		return validateTarGzip(ctx, archive, artifact.Size, required)
	case strings.HasSuffix(artifact.Name, ".zip"):
		return validateZip(ctx, archive, artifact.Size, required)
	default:
		return fmt.Errorf("release artifact %q has unsupported archive format", artifact.Name)
	}
}

func requiredArchiveFiles(artifact Artifact) (map[string]bool, error) {
	switch artifact.OS {
	case "windows":
		if !strings.HasSuffix(artifact.Name, ".zip") {
			return nil, errors.New("Windows release artifact must be a zip archive")
		}
		return map[string]bool{"supermetry.exe": false, "LICENSE": false, "NOTICE": false}, nil
	case "linux", "darwin":
		if !strings.HasSuffix(artifact.Name, ".tar.gz") {
			return nil, fmt.Errorf("%s release artifact must be a tar.gz archive", artifact.OS)
		}
		return map[string]bool{"supermetry": false, "LICENSE": false, "NOTICE": false, "supermetry.service": false}, nil
	default:
		return nil, fmt.Errorf("unsupported release platform %s/%s", artifact.OS, artifact.Arch)
	}
}

func validateTarGzip(ctx context.Context, archive io.ReaderAt, size int64, required map[string]bool) error {
	compressed := io.NewSectionReader(archive, 0, size)
	gzipReader, err := gzip.NewReader(compressed)
	if err != nil {
		return fmt.Errorf("open release tar.gz: %w", err)
	}
	gzipReader.Multistream(false)
	defer gzipReader.Close()

	reader := tar.NewReader(&contextReader{ctx: ctx, reader: gzipReader})
	var total int64
	seen := make(map[string]struct{}, len(required))
	for {
		header, err := reader.Next()
		if errors.Is(err, io.EOF) {
			break
		}
		if err != nil {
			return fmt.Errorf("read release tar.gz: %w", err)
		}
		if err := validateArchiveName(header.Name); err != nil {
			return err
		}
		if header.Typeflag != tar.TypeReg && header.Typeflag != tar.TypeRegA {
			return fmt.Errorf("release archive entry %q is not a regular file", header.Name)
		}
		if _, ok := required[header.Name]; !ok {
			return fmt.Errorf("release archive contains unexpected entry %q", header.Name)
		}
		if _, duplicate := seen[header.Name]; duplicate {
			return fmt.Errorf("release archive contains duplicate entry %q", header.Name)
		}
		if header.Size < 0 || header.Size > maxUncompressedArchiveBytes-total {
			return errors.New("release archive exceeds uncompressed size limit")
		}
		total += header.Size
		seen[header.Name] = struct{}{}
		required[header.Name] = true
		written, err := io.Copy(io.Discard, reader)
		if err != nil {
			return fmt.Errorf("read release archive entry %q: %w", header.Name, err)
		}
		if written != header.Size {
			return fmt.Errorf("release archive entry %q size mismatch", header.Name)
		}
	}
	if err := ctx.Err(); err != nil {
		return err
	}
	return checkRequiredEntries(required)
}

func validateZip(ctx context.Context, archive io.ReaderAt, size int64, required map[string]bool) error {
	reader, err := zip.NewReader(archive, size)
	if err != nil {
		return fmt.Errorf("open release zip: %w", err)
	}
	var total int64
	seen := make(map[string]struct{}, len(required))
	for _, file := range reader.File {
		if err := ctx.Err(); err != nil {
			return err
		}
		if err := validateArchiveName(file.Name); err != nil {
			return err
		}
		if !file.Mode().IsRegular() {
			return fmt.Errorf("release archive entry %q is not a regular file", file.Name)
		}
		if _, ok := required[file.Name]; !ok {
			return fmt.Errorf("release archive contains unexpected entry %q", file.Name)
		}
		if _, duplicate := seen[file.Name]; duplicate {
			return fmt.Errorf("release archive contains duplicate entry %q", file.Name)
		}
		entrySize := int64(file.UncompressedSize64)
		if entrySize < 0 || entrySize > maxUncompressedArchiveBytes-total {
			return errors.New("release archive exceeds uncompressed size limit")
		}
		total += entrySize
		seen[file.Name] = struct{}{}
		required[file.Name] = true
		entry, err := file.Open()
		if err != nil {
			return fmt.Errorf("open release archive entry %q: %w", file.Name, err)
		}
		written, copyErr := io.Copy(io.Discard, &contextReader{ctx: ctx, reader: entry})
		closeErr := entry.Close()
		if copyErr != nil || closeErr != nil {
			return fmt.Errorf("read release archive entry %q: %w", file.Name, errors.Join(copyErr, closeErr))
		}
		if written != entrySize {
			return fmt.Errorf("release archive entry %q size mismatch", file.Name)
		}
	}
	return checkRequiredEntries(required)
}

func validateArchiveName(name string) error {
	if name == "" || name == "." || path.Base(name) != name || strings.ContainsAny(name, `/\\`) {
		return fmt.Errorf("release archive contains unsafe path %q", name)
	}
	return nil
}

func checkRequiredEntries(required map[string]bool) error {
	for name, present := range required {
		if !present {
			return fmt.Errorf("release archive is missing required entry %q", name)
		}
	}
	return nil
}

type contextReader struct {
	ctx    context.Context
	reader io.Reader
}

func (r *contextReader) Read(buffer []byte) (int, error) {
	if err := r.ctx.Err(); err != nil {
		return 0, err
	}
	return r.reader.Read(buffer)
}
