package config

import (
	"encoding/base64"
	"net/netip"
	"reflect"
	"strings"
	"testing"
	"time"
)

func validSecretKey() string {
	return base64.StdEncoding.EncodeToString(make([]byte, 32))
}

func baseEnv() map[string]string {
	return map[string]string{
		"SUPERMETRY_DATABASE_URL": "postgres://x",
		"SUPERMETRY_BASE_URL":     "https://a.example",
		"SUPERMETRY_SECRET_KEY":   validSecretKey(),
	}
}

func env(m map[string]string) func(string) string {
	return func(k string) string { return m[k] }
}

func TestLoadTrustedProxies(t *testing.T) {
	c, err := Load(env(map[string]string{
		"SUPERMETRY_DATABASE_URL":    "postgres://x",
		"SUPERMETRY_BASE_URL":        "https://a.example",
		"SUPERMETRY_SECRET_KEY":      validSecretKey(),
		"SUPERMETRY_TRUSTED_PROXIES": "192.0.2.1, 2001:db8::/32",
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
		"SUPERMETRY_DATABASE_URL":    "postgres://x",
		"SUPERMETRY_BASE_URL":        "https://a.example",
		"SUPERMETRY_SECRET_KEY":      validSecretKey(),
		"SUPERMETRY_TRUSTED_PROXIES": "not-an-address",
	})); err == nil {
		t.Fatal("invalid trusted proxy accepted")
	}
}

func TestLoadMetricsToken(t *testing.T) {
	values := baseEnv()
	values["SUPERMETRY_METRICS_TOKEN"] = "metrics-secret"
	c, err := Load(env(values))
	if err != nil {
		t.Fatal(err)
	}
	if c.MetricsToken != "metrics-secret" {
		t.Fatalf("MetricsToken = %q, want configured token", c.MetricsToken)
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
	if c.Listen != ":8080" || c.EventPath != "/api/public/v1/events" || c.TrackerPath != "/js/script.js" || c.QueueSize != 65536 || c.DedupEntries != 250000 || c.FlushEvery != time.Second || c.QueryTimeout != 10*time.Second || c.Signup != "invite" {
		t.Fatalf("bad defaults: %+v", c)
	}
	if c.HTTPReadTimeout != 30*time.Second || c.HTTPWriteTimeout != 60*time.Second {
		t.Fatalf("HTTP timeouts = (%s, %s), want (30s, 1m)", c.HTTPReadTimeout, c.HTTPWriteTimeout)
	}
	if c.DataDir != "./data" {
		t.Errorf("DataDir = %q, want ./data", c.DataDir)
	}
	if c.HomepageURL != "https://supermetry.com" {
		t.Errorf("HomepageURL = %q, want canonical default", c.HomepageURL)
	}
	if want := []string{"GeoLite2-City", "GeoLite2-ASN"}; !reflect.DeepEqual(c.MaxMindEditions, want) {
		t.Errorf("MaxMindEditions = %v, want %v", c.MaxMindEditions, want)
	}
}

func TestLoadHTTPTimeouts(t *testing.T) {
	values := baseEnv()
	values["SUPERMETRY_HTTP_READ_TIMEOUT"] = "12s"
	values["SUPERMETRY_HTTP_WRITE_TIMEOUT"] = "2m"
	got, err := Load(env(values))
	if err != nil {
		t.Fatal(err)
	}
	if got.HTTPReadTimeout != 12*time.Second || got.HTTPWriteTimeout != 2*time.Minute {
		t.Fatalf("HTTP timeouts = (%s, %s), want (12s, 2m)", got.HTTPReadTimeout, got.HTTPWriteTimeout)
	}

	for _, name := range []string{"SUPERMETRY_HTTP_READ_TIMEOUT", "SUPERMETRY_HTTP_WRITE_TIMEOUT"} {
		t.Run(name, func(t *testing.T) {
			invalid := baseEnv()
			invalid[name] = "0s"
			if _, err := Load(env(invalid)); err == nil || !strings.Contains(err.Error(), name) {
				t.Fatalf("Load() error = %v, want %s validation", err, name)
			}
		})
	}
}

func TestLoadHomepageURL(t *testing.T) {
	values := baseEnv()
	values["SUPERMETRY_HOMEPAGE_URL"] = "https://analytics.example/about"
	c, err := Load(env(values))
	if err != nil {
		t.Fatal(err)
	}
	if c.HomepageURL != values["SUPERMETRY_HOMEPAGE_URL"] {
		t.Fatalf("HomepageURL = %q", c.HomepageURL)
	}
	values["SUPERMETRY_HOMEPAGE_URL"] = "javascript:alert(1)"
	if _, err := Load(env(values)); err == nil || !strings.Contains(err.Error(), "SUPERMETRY_HOMEPAGE_URL") {
		t.Fatalf("unsafe homepage URL error = %v", err)
	}
}

func TestLoadRejectsLegacyPrefixAsConfiguration(t *testing.T) {
	legacyPrefix := "PAN" + "EL_"
	values := map[string]string{
		legacyPrefix + "DATABASE_URL": "postgres://legacy.example/analytics",
		legacyPrefix + "BASE_URL":     "https://legacy.example",
		legacyPrefix + "SECRET_KEY":   validSecretKey(),
	}
	_, err := Load(env(values))
	if err == nil {
		t.Fatal("legacy-only configuration must not start the service")
	}
	for _, name := range []string{"SUPERMETRY_DATABASE_URL", "SUPERMETRY_BASE_URL", "SUPERMETRY_SECRET_KEY"} {
		if !strings.Contains(err.Error(), name) {
			t.Errorf("error %q does not identify missing %s", err, name)
		}
	}
}

func TestLoadDoesNotUseLegacyValuesToOverridePolicy(t *testing.T) {
	legacyPrefix := "PAN" + "EL_"
	values := baseEnv()
	values["SUPERMETRY_TRACKING_MODE"] = "anonymous"
	values["SUPERMETRY_TRACKING_PUBLIC_PATHS"] = `[]`
	values["SUPERMETRY_REPLAY_ENABLED"] = "false"
	values[legacyPrefix+"TRACKING_MODE"] = "identified"
	values[legacyPrefix+"TRACKING_PUBLIC_PATHS"] = `["/private"]`
	values[legacyPrefix+"REPLAY_ENABLED"] = "true"

	c, err := LoadLookup(func(key string) (string, bool) {
		value, ok := values[key]
		return value, ok
	})
	if err != nil {
		t.Fatal(err)
	}
	if c.Tracking.Mode.String() != "anonymous" || !c.Tracking.Explicit {
		t.Fatalf("tracking mode = %+v, want explicit anonymous", c.Tracking)
	}
	if c.Replay.Enabled {
		t.Fatal("legacy replay setting overrode the Supermetry privacy ceiling")
	}
	if got := c.TrackingPaths.PublicPath("/private"); got != "/[redacted]" {
		t.Fatalf("legacy public path resolved to %q, want redacted", got)
	}
}

func TestLoadMaxMind(t *testing.T) {
	tests := []struct {
		name     string
		account  string
		key      string
		editions string
		want     []string
		wantErr  string
	}{
		{name: "credentials and allowed editions", account: "1234", key: "top-secret", editions: "GeoLite2-Country, GeoLite2-ASN", want: []string{"GeoLite2-Country", "GeoLite2-ASN"}},
		{name: "account without key", account: "1234", wantErr: "both be set"},
		{name: "key without account", key: "top-secret", wantErr: "both be set"},
		{name: "unknown edition", account: "1234", key: "top-secret", editions: "GeoIP2-City", wantErr: "SUPERMETRY_MAXMIND_EDITIONS"},
		{name: "empty edition", account: "1234", key: "top-secret", editions: "GeoLite2-City,", wantErr: "SUPERMETRY_MAXMIND_EDITIONS"},
	}
	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			values := baseEnv()
			values["SUPERMETRY_MAXMIND_ACCOUNT_ID"] = tt.account
			values["SUPERMETRY_MAXMIND_LICENSE_KEY"] = tt.key
			if tt.editions != "" {
				values["SUPERMETRY_MAXMIND_EDITIONS"] = tt.editions
			}
			values["SUPERMETRY_DATA_DIR"] = "/var/lib/supermetry"

			got, err := Load(env(values))
			if tt.wantErr != "" {
				if err == nil || !strings.Contains(err.Error(), tt.wantErr) {
					t.Fatalf("Load() error = %v, want containing %q", err, tt.wantErr)
				}
				if strings.Contains(err.Error(), tt.key) && tt.key != "" {
					t.Fatalf("Load() error leaked license key: %v", err)
				}
				return
			}
			if err != nil {
				t.Fatal(err)
			}
			if got.MaxMindAccountID != tt.account || got.MaxMindLicenseKey != tt.key || got.DataDir != "/var/lib/supermetry" {
				t.Fatalf("MaxMind config = %+v", got)
			}
			if !reflect.DeepEqual(got.MaxMindEditions, tt.want) {
				t.Errorf("MaxMindEditions = %v, want %v", got.MaxMindEditions, tt.want)
			}
		})
	}
}

func TestLoadGeoIPDoesNotRequireServerConfiguration(t *testing.T) {
	got, err := LoadGeoIP(env(map[string]string{
		"SUPERMETRY_MAXMIND_ACCOUNT_ID":  "1234",
		"SUPERMETRY_MAXMIND_LICENSE_KEY": "top-secret",
		"SUPERMETRY_DATA_DIR":            "/var/lib/supermetry",
	}))
	if err != nil {
		t.Fatal(err)
	}
	if got.DatabaseURL != "" || got.BaseURL != "" || got.MaxMindAccountID != "1234" || got.DataDir != "/var/lib/supermetry" {
		t.Fatalf("LoadGeoIP() = %+v", got)
	}
}

func TestLoadRejectsBadValues(t *testing.T) {
	base := baseEnv()
	for k, v := range map[string]string{"SUPERMETRY_QUEUE_SIZE": "0", "SUPERMETRY_DEDUP_ENTRIES": "-1", "SUPERMETRY_EVENT_PATH": "no-slash", "SUPERMETRY_FLUSH_EVERY": "-1s", "SUPERMETRY_BASE_URL": "ftp://x"} {
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
			values["SUPERMETRY_SECRET_KEY"] = tt.key
			if _, err := Load(env(values)); err == nil {
				t.Fatal("invalid secret key accepted")
			}
		})
	}

	values := baseEnv()
	values["SUPERMETRY_SIGNUP"] = "maybe"
	if _, err := Load(env(values)); err == nil {
		t.Fatal("invalid signup mode accepted")
	}
}

func TestLoadSMTP(t *testing.T) {
	values := baseEnv()
	values["SUPERMETRY_SIGNUP"] = "open"
	values["SUPERMETRY_SMTP_HOST"] = "smtp.example.com"
	values["SUPERMETRY_SMTP_PORT"] = "2525"
	values["SUPERMETRY_SMTP_USER"] = "mailer"
	values["SUPERMETRY_SMTP_PASS"] = "secret"
	values["SUPERMETRY_SMTP_FROM"] = "Supermetry <supermetry@example.com>"
	values["SUPERMETRY_SMTP_STARTTLS"] = "true"
	c, err := Load(env(values))
	if err != nil {
		t.Fatal(err)
	}
	if c.Signup != "open" || c.SMTP.Host != "smtp.example.com" || c.SMTP.Port != 2525 || c.SMTP.User != "mailer" || c.SMTP.Pass != "secret" || c.SMTP.From != "Supermetry <supermetry@example.com>" || !c.SMTP.StartTLS {
		t.Fatalf("bad platform config: %+v", c)
	}
}

func TestLoadAllowPrivateWebhooks(t *testing.T) {
	values := baseEnv()
	values["SUPERMETRY_ALLOW_PRIVATE_WEBHOOKS"] = "true"
	c, err := Load(env(values))
	if err != nil {
		t.Fatal(err)
	}
	if !c.AllowPrivateWebhooks {
		t.Fatal("AllowPrivateWebhooks = false, want true")
	}

	values["SUPERMETRY_ALLOW_PRIVATE_WEBHOOKS"] = "not-a-boolean"
	if _, err := Load(env(values)); err == nil || !strings.Contains(err.Error(), "SUPERMETRY_ALLOW_PRIVATE_WEBHOOKS") {
		t.Fatalf("Load() error = %v, want webhook boolean validation", err)
	}
}

func TestReplayDisabledByDefault(t *testing.T) {
	got, err := Load(env(baseEnv()))
	if err != nil {
		t.Fatal(err)
	}
	if got.Replay.Enabled {
		t.Fatal("Replay.Enabled = true, want false")
	}
	if got.Replay.Mask != nil || got.Replay.SampleRate != nil || got.Replay.Retention != nil || got.Replay.QuotaBytes != nil || got.Replay.Dir != "" || got.Replay.S3 != nil {
		t.Fatalf("unset replay environment = %+v, want only zero values", got.Replay)
	}
}

func TestS3EnvRequiresAllFields(t *testing.T) {
	values := baseEnv()
	values["SUPERMETRY_REPLAY_S3_ENDPOINT"] = "https://s3.example.test"
	values["SUPERMETRY_REPLAY_S3_REGION"] = "test-1"
	values["SUPERMETRY_REPLAY_S3_ACCESS_KEY"] = "access"
	values["SUPERMETRY_REPLAY_S3_SECRET_KEY"] = "secret"
	values["SUPERMETRY_REPLAY_S3_PREFIX"] = "supermetry"

	if _, err := Load(env(values)); err == nil {
		t.Fatal("Load accepted S3 settings with a missing bucket")
	}

	values["SUPERMETRY_REPLAY_S3_BUCKET"] = "replays"
	delete(values, "SUPERMETRY_REPLAY_S3_PREFIX")
	got, err := Load(env(values))
	if err != nil {
		t.Fatal(err)
	}
	if got.Replay.S3 == nil || got.Replay.S3.Prefix != "" {
		t.Fatalf("Replay.S3 = %+v without a prefix, want S3 with an empty prefix", got.Replay.S3)
	}

	values["SUPERMETRY_REPLAY_S3_PREFIX"] = "supermetry"
	got, err = Load(env(values))
	if err != nil {
		t.Fatal(err)
	}
	if got.Replay.S3 == nil || got.Replay.S3.Bucket != "replays" || got.Replay.S3.SecretKey != "secret" {
		t.Fatalf("Replay.S3 = %+v, want complete environment", got.Replay.S3)
	}
}

func TestLoadReplayEnvironment(t *testing.T) {
	values := baseEnv()
	values["SUPERMETRY_REPLAY_ENABLED"] = "true"
	values["SUPERMETRY_REPLAY_MASK"] = "false"
	values["SUPERMETRY_REPLAY_SAMPLE_RATE"] = "25"
	values["SUPERMETRY_REPLAY_RETENTION_DAYS"] = "90"
	values["SUPERMETRY_REPLAY_QUOTA"] = "500MB"
	values["SUPERMETRY_REPLAY_DIR"] = "/srv/replay"

	got, err := Load(env(values))
	if err != nil {
		t.Fatal(err)
	}
	if !got.Replay.Enabled || got.Replay.Mask == nil || *got.Replay.Mask || got.Replay.SampleRate == nil || *got.Replay.SampleRate != 25 || got.Replay.Retention == nil || *got.Replay.Retention != 90 || got.Replay.QuotaBytes == nil || *got.Replay.QuotaBytes != 500*1024*1024 || got.Replay.Dir != "/srv/replay" {
		t.Fatalf("Replay = %+v", got.Replay)
	}
}

func TestLoadRejectsInvalidReplayEnvironment(t *testing.T) {
	tests := []struct {
		name  string
		value string
	}{
		{name: "SUPERMETRY_REPLAY_ENABLED", value: "sometimes"},
		{name: "SUPERMETRY_REPLAY_MASK", value: "sometimes"},
		{name: "SUPERMETRY_REPLAY_SAMPLE_RATE", value: "101"},
		{name: "SUPERMETRY_REPLAY_RETENTION_DAYS", value: "0"},
		{name: "SUPERMETRY_REPLAY_QUOTA", value: "5XB"},
	}
	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			values := baseEnv()
			values[tt.name] = tt.value
			if _, err := Load(env(values)); err == nil || !strings.Contains(err.Error(), tt.name) {
				t.Fatalf("Load() error = %v, want %s validation error", err, tt.name)
			}
		})
	}
}
