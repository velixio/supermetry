package installer

import (
	"net/url"
	"testing"
)

func TestBuildDatabaseURLEscapes(t *testing.T) {
	t.Parallel()

	const password = "p@ss:w/rd#% x"
	conn := (DBConfig{
		Host:     "127.0.0.1",
		Port:     "5432",
		User:     "supermetry",
		Password: password,
		Name:     "supermetry",
	}).ConnString()

	parsed, err := url.Parse(conn)
	if err != nil {
		t.Fatal(err)
	}
	gotPassword, ok := parsed.User.Password()
	if !ok || gotPassword != password {
		t.Fatalf("password = %q, %v; want %q, true", gotPassword, ok, password)
	}
	if got := parsed.Query().Get("sslmode"); got != "disable" {
		t.Fatalf("sslmode = %q, want disable", got)
	}
}

func TestDBConfigConnString(t *testing.T) {
	t.Parallel()

	tests := []struct {
		name string
		cfg  DBConfig
		want string
	}{
		{name: "URL wins", cfg: DBConfig{URL: "postgres://example/unchanged", Host: "ignored"}, want: "postgres://example/unchanged"},
		{name: "remote requires TLS", cfg: DBConfig{Host: "db.example", Port: "5432", User: "u", Password: "p", Name: "db"}, want: "require"},
		{name: "IPv6 loopback disables TLS", cfg: DBConfig{Host: "::1", Port: "5432", User: "u", Password: "p", Name: "db"}, want: "disable"},
	}
	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			got := tt.cfg.ConnString()
			if tt.cfg.URL != "" {
				if got != tt.want {
					t.Fatalf("ConnString() = %q, want %q", got, tt.want)
				}
				return
			}
			parsed, err := url.Parse(got)
			if err != nil {
				t.Fatal(err)
			}
			if sslmode := parsed.Query().Get("sslmode"); sslmode != tt.want {
				t.Fatalf("sslmode = %q, want %q", sslmode, tt.want)
			}
		})
	}
}
