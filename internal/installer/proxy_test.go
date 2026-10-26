package installer

import (
	"os"
	"strings"
	"testing"
)

func TestRenderNginx(t *testing.T) {
	t.Parallel()

	want := `server {
    listen 80;
    server_name analytics.supermetry.com;

    client_max_body_size 10m;

    location / {
        # Supermetry owns response security headers so policies can vary by route.
        proxy_pass http://127.0.0.1:8787;
        proxy_set_header Host $host;
        proxy_set_header X-Forwarded-For $remote_addr;
        proxy_set_header X-Forwarded-Proto $scheme;
        proxy_set_header X-Real-IP $remote_addr;
    }
}
`
	if got := string(RenderNginx("analytics.supermetry.com", "127.0.0.1:8787")); got != want {
		t.Fatalf("RenderNginx() mismatch\n--- got ---\n%s--- want ---\n%s", got, want)
	}
}

func TestNginxConfigurationsLeaveSecurityHeadersToApplication(t *testing.T) {
	t.Parallel()

	files := map[string][]byte{"installer template": []byte(nginxTemplate)}
	example, err := os.ReadFile("../../deploy/nginx/supermetry.example.conf")
	if err != nil {
		t.Fatal(err)
	}
	files["deployment example"] = example
	for name, body := range files {
		t.Run(name, func(t *testing.T) {
			for _, header := range []string{"Strict-Transport-Security", "X-Content-Type-Options", "Referrer-Policy", "Content-Security-Policy", "Permissions-Policy", "Cross-Origin-Resource-Policy", "Cross-Origin-Opener-Policy", "X-Frame-Options"} {
				if strings.Contains(string(body), "add_header "+header) {
					t.Errorf("nginx config adds application-owned %s", header)
				}
			}
		})
	}
}

func TestRenderCaddy(t *testing.T) {
	t.Parallel()

	want := `# BEGIN SUPERMETRY
analytics.supermetry.com {
    reverse_proxy 127.0.0.1:8787
}
# END SUPERMETRY
`
	if got := string(RenderCaddy("analytics.supermetry.com", "127.0.0.1:8787")); got != want {
		t.Fatalf("RenderCaddy() mismatch\n--- got ---\n%s--- want ---\n%s", got, want)
	}
}
