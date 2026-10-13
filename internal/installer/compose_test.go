package installer

import (
	"archive/tar"
	"bytes"
	"compress/gzip"
	"os"
	"path/filepath"
	"strings"
	"testing"
)

func TestReleaseWorkflowUsesDeploymentDockerfile(t *testing.T) {
	t.Parallel()

	workflow := readReleaseWorkflow(t)
	if !strings.Contains(workflow, "file: deploy/Dockerfile") {
		t.Fatal("release image build does not use deploy/Dockerfile")
	}
}

func TestReleaseWorkflowPassesResolvedVersionToDockerBuild(t *testing.T) {
	t.Parallel()

	workflow := readReleaseWorkflow(t)
	if !strings.Contains(workflow, "build-args: |\n            VERSION=${{ steps.version.outputs.version }}") {
		t.Fatal("release image build does not pass the resolved version as the VERSION build argument")
	}
}

func readReleaseWorkflow(t *testing.T) string {
	t.Helper()

	workflow, err := os.ReadFile(filepath.Join("..", "..", ".github", "workflows", "release.yml"))
	if err != nil {
		t.Fatalf("read release workflow: %v", err)
	}
	return string(workflow)
}

func TestRenderCompose(t *testing.T) {
	t.Parallel()

	tests := []struct {
		name  string
		image string
		app   string
	}{
		{name: "download", image: "download", app: "    image: ghcr.io/velixio/supermetry:v1.2.3\n"},
		{name: "build", image: "build", app: "    build: ./src\n"},
	}
	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			want := `services:
  supermetry:
` + tt.app + `    env_file: .env
    environment:
      SUPERMETRY_DATABASE_HOST: postgres
      SUPERMETRY_DATABASE_USER: supermetry
      SUPERMETRY_DATABASE_PASSWORD: ${POSTGRES_PASSWORD}
      SUPERMETRY_DATABASE_NAME: supermetry
      SUPERMETRY_DATABASE_SSLMODE: disable
      SUPERMETRY_DATA_DIR: /data
    ports:
      - "127.0.0.1:8787:8787"
    volumes:
      - supermetry-data:/data
    depends_on:
      postgres:
        condition: service_healthy
  postgres:
    image: postgres:18-alpine
    environment:
      POSTGRES_USER: supermetry
      POSTGRES_PASSWORD: ${POSTGRES_PASSWORD}
      POSTGRES_DB: supermetry
    volumes:
      - postgres-data:/var/lib/postgresql
    healthcheck:
      test: ["CMD-SHELL", "pg_isready -U supermetry -d supermetry"]
      interval: 5s
      timeout: 5s
      retries: 10

volumes:
  supermetry-data:
  postgres-data:
`
			got := string(RenderCompose(ComposeValues{Version: "v1.2.3", Image: tt.image, ListenHost: "127.0.0.1", Port: "8787"}))
			if got != want {
				t.Fatalf("RenderCompose() mismatch\n--- got ---\n%s--- want ---\n%s", got, want)
			}
		})
	}
}

func TestRenderComposeManagedUpdaterAccess(t *testing.T) {
	t.Parallel()

	got := string(RenderCompose(ComposeValues{
		Version: "v1.2.3", Image: "download", ListenHost: "127.0.0.1", Port: "8787",
		Updater: true, UpdaterGID: "987",
	}))
	for _, want := range []string{
		`group_add:`, `- "987"`,
		`/run/supermetry-updater:/run/supermetry-updater:ro`,
		`/etc/supermetry/deployment.json:/etc/supermetry/deployment.json:ro`,
	} {
		if !strings.Contains(got, want) {
			t.Errorf("managed Compose output missing %q:\n%s", want, got)
		}
	}
	if strings.Contains(got, "/var/run/docker.sock") {
		t.Fatalf("managed Compose exposes Docker socket:\n%s", got)
	}

	localBuild := string(RenderCompose(ComposeValues{
		Version: "v1.2.3", Image: "build", ListenHost: "127.0.0.1", Port: "8787",
	}))
	if strings.Contains(localBuild, "supermetry-updater") || strings.Contains(localBuild, "group_add") {
		t.Fatalf("local-build Compose unexpectedly enables managed updates:\n%s", localBuild)
	}
}

func TestExtractSourceArchive(t *testing.T) {
	t.Parallel()
	var archive bytes.Buffer
	gz := gzip.NewWriter(&archive)
	tw := tar.NewWriter(gz)
	content := []byte("FROM scratch\n")
	if err := tw.WriteHeader(&tar.Header{Name: "supermetry-v1.2.3/Dockerfile", Mode: 0o644, Size: int64(len(content)), Typeflag: tar.TypeReg}); err != nil {
		t.Fatal(err)
	}
	if _, err := tw.Write(content); err != nil {
		t.Fatal(err)
	}
	if err := tw.Close(); err != nil {
		t.Fatal(err)
	}
	if err := gz.Close(); err != nil {
		t.Fatal(err)
	}
	dir := t.TempDir()
	if err := extractSourceArchive(bytes.NewReader(archive.Bytes()), dir); err != nil {
		t.Fatal(err)
	}
	got, err := os.ReadFile(filepath.Join(dir, "Dockerfile"))
	if err != nil {
		t.Fatal(err)
	}
	if string(got) != string(content) {
		t.Fatalf("Dockerfile = %q", got)
	}
}

func TestExtractSourceArchiveRejectsTraversal(t *testing.T) {
	t.Parallel()
	var archive bytes.Buffer
	gz := gzip.NewWriter(&archive)
	tw := tar.NewWriter(gz)
	content := []byte("unsafe")
	if err := tw.WriteHeader(&tar.Header{Name: "root/../../outside", Mode: 0o644, Size: int64(len(content)), Typeflag: tar.TypeReg}); err != nil {
		t.Fatal(err)
	}
	if _, err := tw.Write(content); err != nil {
		t.Fatal(err)
	}
	tw.Close()
	gz.Close()
	err := extractSourceArchive(bytes.NewReader(archive.Bytes()), t.TempDir())
	if err == nil || !strings.Contains(err.Error(), "unsafe source archive path") {
		t.Fatalf("error = %v", err)
	}
}
