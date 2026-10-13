package installer

import (
	"archive/tar"
	"bytes"
	"compress/gzip"
	"context"
	_ "embed"
	"fmt"
	"io"
	"net/http"
	"os"
	"path/filepath"
	"strings"
	"text/template"
)

//go:embed templates/docker-compose.yml.tmpl
var composeTemplate string

// ComposeValues controls the generated Docker Compose file.
type ComposeValues struct {
	Version    string
	Image      string
	ListenHost string
	Port       string
	Updater    bool
	UpdaterGID string
}

// RenderCompose renders the bundled app and PostgreSQL services.
func RenderCompose(c ComposeValues) []byte {
	if c.Version == "" {
		c.Version = "latest"
	}
	tmpl := template.Must(template.New("compose").Option("missingkey=error").Parse(composeTemplate))
	var out bytes.Buffer
	if err := tmpl.Execute(&out, c); err != nil {
		panic(err)
	}
	return out.Bytes()
}

func prepareDockerBuildSource(ctx context.Context, opts Options) error {
	_, sourceURL, sourceDir := dockerSource(opts)
	req, err := http.NewRequestWithContext(ctx, http.MethodGet, sourceURL, nil)
	if err != nil {
		return err
	}
	resp, err := http.DefaultClient.Do(req)
	if err != nil {
		return fmt.Errorf("download Docker build source: %w", err)
	}
	defer resp.Body.Close()
	if resp.StatusCode != http.StatusOK {
		return fmt.Errorf("download Docker build source: HTTP %d", resp.StatusCode)
	}
	if err := os.RemoveAll(sourceDir); err != nil {
		return fmt.Errorf("replace Docker build source: %w", err)
	}
	if err := os.MkdirAll(sourceDir, 0o755); err != nil {
		return fmt.Errorf("create Docker build source directory: %w", err)
	}
	return extractSourceArchive(resp.Body, sourceDir)
}

func dockerSource(opts Options) (archive, sourceURL, sourceDir string) {
	version := opts.Version
	if version == "" || version == "latest" {
		version = "main"
	}
	if version != "main" && !strings.HasPrefix(version, "v") {
		version = "v" + version
	}
	archive = filepath.Join(opts.Dir, "supermetry-"+version+".tar.gz")
	sourceDir = filepath.Join(opts.Dir, "src")
	sourceURL = "https://github.com/velixio/supermetry/archive/refs/heads/main.tar.gz"
	if version != "main" {
		sourceURL = "https://github.com/velixio/supermetry/archive/refs/tags/" + version + ".tar.gz"
	}
	return archive, sourceURL, sourceDir
}

func extractSourceArchive(r io.Reader, destination string) error {
	gz, err := gzip.NewReader(r)
	if err != nil {
		return fmt.Errorf("open source archive: %w", err)
	}
	defer gz.Close()
	tree := tar.NewReader(gz)
	for {
		header, err := tree.Next()
		if err == io.EOF {
			return nil
		}
		if err != nil {
			return fmt.Errorf("read source archive: %w", err)
		}
		_, relative, ok := strings.Cut(filepath.ToSlash(header.Name), "/")
		if !ok || relative == "" {
			continue
		}
		relative = filepath.Clean(filepath.FromSlash(relative))
		if relative == "." || filepath.IsAbs(relative) || relative == ".." || strings.HasPrefix(relative, ".."+string(filepath.Separator)) {
			return fmt.Errorf("unsafe source archive path %q", header.Name)
		}
		target := filepath.Join(destination, relative)
		switch header.Typeflag {
		case tar.TypeDir:
			if err := os.MkdirAll(target, os.FileMode(header.Mode)&0o755); err != nil {
				return err
			}
		case tar.TypeReg, tar.TypeRegA:
			if err := os.MkdirAll(filepath.Dir(target), 0o755); err != nil {
				return err
			}
			file, err := os.OpenFile(target, os.O_CREATE|os.O_TRUNC|os.O_WRONLY, os.FileMode(header.Mode)&0o755)
			if err != nil {
				return err
			}
			_, copyErr := io.Copy(file, tree)
			closeErr := file.Close()
			if copyErr != nil {
				return copyErr
			}
			if closeErr != nil {
				return closeErr
			}
		case tar.TypeSymlink:
			link := filepath.Clean(filepath.FromSlash(header.Linkname))
			if filepath.IsAbs(link) || link == ".." || strings.HasPrefix(link, ".."+string(filepath.Separator)) {
				return fmt.Errorf("unsafe source archive link %q", header.Linkname)
			}
			if err := os.MkdirAll(filepath.Dir(target), 0o755); err != nil {
				return err
			}
			if err := os.Symlink(link, target); err != nil {
				return err
			}
		}
	}
}
