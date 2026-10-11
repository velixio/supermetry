package installer

import "testing"

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
    ports:
      - "127.0.0.1:8787:8787"
    volumes:
      - supermetry-data:/var/lib/supermetry
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
