package config

import (
	"encoding/base64"
	"net/netip"
	"testing"
	"time"
)

func validSecretKey() string {
	return base64.StdEncoding.EncodeToString(make([]byte, 32))
}

func baseEnv() map[string]string {
	return map[string]string{
		"PANEL_DATABASE_URL": "postgres://x",
		"PANEL_BASE_URL":     "https://a.example",
		"PANEL_SECRET_KEY":   validSecretKey(),
	}
}

func env(m map[string]string) func(string) string {
	return func(k string) string { return m[k] }
}

func TestLoadTrustedProxies(t *testing.T) {
	c, err := Load(env(map[string]string{
		"PANEL_DATABASE_URL":    "postgres://x",
		"PANEL_BASE_URL":        "https://a.example",
		"PANEL_SECRET_KEY":      validSecretKey(),
		"PANEL_TRUSTED_PROXIES": "192.0.2.1, 2001:db8::/32",
	}))
	if err != nil {
		t.Fatal(err)
	}
	want := []netip.Prefix{netip.MustParsePrefix("192.0.2.1/32"), netip.MustParsePrefix("2001:db8::/32")}
	if len(c.TrustedProxies) != len(want) {
		t.Fatalf("TrustedProxies=%v, want %v", c.TrustedProxies, want)
	}
	for i := range want {
		if c.TrustedProxies[i] != want[i] {
			t.Fatalf("TrustedProxies[%d]=%v, want %v", i, c.TrustedProxies[i], want[i])
		}
	}

	if _, err := Load(env(map[string]string{
		"PANEL_DATABASE_URL":    "postgres://x",
		"PANEL_BASE_URL":        "https://a.example",
		"PANEL_SECRET_KEY":      validSecretKey(),
		"PANEL_TRUSTED_PROXIES": "not-an-address",
	})); err == nil {
		t.Fatal("invalid trusted proxy accepted")
	}
}

func TestLoadDefaultsAndRequired(t *testing.T) {
	_, err := Load(env(map[string]string{}))
	if err == nil {
		t.Fatal("want error for missing required settings")
	}
	c, err := Load(env(baseEnv()))
	if err != nil {
		t.Fatal(err)
	}
	if c.Listen != ":8080" || c.EventPath != "/api/public/v1/events" || c.TrackerPath != "/js/script.js" || c.QueueSize != 65536 || c.FlushEvery != time.Second || c.QueryTimeout != 10*time.Second || c.Signup != "invite" {
		t.Fatalf("bad defaults: %+v", c)
	}
}

func TestLoadRejectsBadValues(t *testing.T) {
	base := baseEnv()
	for k, v := range map[string]string{"PANEL_QUEUE_SIZE": "0", "PANEL_EVENT_PATH": "no-slash", "PANEL_FLUSH_EVERY": "-1s", "PANEL_BASE_URL": "ftp://x"} {
		m := map[string]string{}
		for a, b := range base {
			m[a] = b
		}
		m[k] = v
		if _, err := Load(env(m)); err == nil {
			t.Errorf("%s=%q accepted", k, v)
		}
	}
}

func TestConfigRequiresSecretKey(t *testing.T) {
	tests := []struct {
		name string
		key  string
	}{
		{name: "missing"},
		{name: "invalid base64", key: "not-base64"},
		{name: "too short", key: base64.StdEncoding.EncodeToString(make([]byte, 16))},
	}
	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			values := baseEnv()
			values["PANEL_SECRET_KEY"] = tt.key
			if _, err := Load(env(values)); err == nil {
				t.Fatal("invalid secret key accepted")
			}
		})
	}

	values := baseEnv()
	values["PANEL_SIGNUP"] = "maybe"
	if _, err := Load(env(values)); err == nil {
		t.Fatal("invalid signup mode accepted")
	}
}

func TestLoadSMTP(t *testing.T) {
	values := baseEnv()
	values["PANEL_SIGNUP"] = "open"
	values["PANEL_SMTP_HOST"] = "smtp.example.com"
	values["PANEL_SMTP_PORT"] = "2525"
	values["PANEL_SMTP_USER"] = "mailer"
	values["PANEL_SMTP_PASS"] = "secret"
	values["PANEL_SMTP_FROM"] = "Panel <panel@example.com>"
	values["PANEL_SMTP_STARTTLS"] = "true"
	c, err := Load(env(values))
	if err != nil {
		t.Fatal(err)
	}
	if c.Signup != "open" || c.SMTP.Host != "smtp.example.com" || c.SMTP.Port != 2525 || c.SMTP.User != "mailer" || c.SMTP.Pass != "secret" || c.SMTP.From != "Panel <panel@example.com>" || !c.SMTP.StartTLS {
		t.Fatalf("bad platform config: %+v", c)
	}
}
