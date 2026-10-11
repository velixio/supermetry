package installer

import "testing"

func TestRenderNginx(t *testing.T) {
	t.Parallel()

	want := `server {
    listen 80;
    server_name analytics.example.com;

    client_max_body_size 10m;

    location / {
        proxy_pass http://127.0.0.1:8787;
        proxy_set_header Host $host;
        proxy_set_header X-Forwarded-For $proxy_add_x_forwarded_for;
        proxy_set_header X-Forwarded-Proto $scheme;
        proxy_set_header X-Real-IP $remote_addr;
    }
}
`
	if got := string(RenderNginx("analytics.example.com", "127.0.0.1:8787")); got != want {
		t.Fatalf("RenderNginx() mismatch\n--- got ---\n%s--- want ---\n%s", got, want)
	}
}

func TestRenderCaddy(t *testing.T) {
	t.Parallel()

	want := `analytics.example.com {
    reverse_proxy 127.0.0.1:8787
}
`
	if got := string(RenderCaddy("analytics.example.com", "127.0.0.1:8787")); got != want {
		t.Fatalf("RenderCaddy() mismatch\n--- got ---\n%s--- want ---\n%s", got, want)
	}
}
