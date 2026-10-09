package replay

import (
	"bytes"
	"encoding/json"
	"path/filepath"
	"reflect"
	"strings"
	"testing"

	"supermetry/internal/config"
	"supermetry/internal/sites"
)

func ptr[T any](value T) *T { return &value }

func TestResolveSitePrivacyBounds(t *testing.T) {
	for _, tc := range []struct {
		name              string
		env               config.ReplayEnv
		in                sites.ReplaySiteInput
		enabled, mask     bool
		sample, retention int
		quota             int64
	}{
		{name: "safe defaults", sample: 15, retention: 30, quota: defaultQuotaBytes},
		{name: "instance permission alone", env: config.ReplayEnv{Enabled: true}, sample: 15, retention: 30, quota: defaultQuotaBytes},
		{name: "project opt in alone", in: sites.ReplaySiteInput{Enabled: true}, sample: 15, retention: 30, quota: defaultQuotaBytes},
		{name: "both switches", env: config.ReplayEnv{Enabled: true}, in: sites.ReplaySiteInput{Enabled: true}, enabled: true, sample: 15, retention: 30, quota: defaultQuotaBytes},
		{name: "project stricter than environment", env: config.ReplayEnv{Enabled: true, Mask: ptr(false), SampleRate: ptr(75), Retention: ptr(120), QuotaBytes: ptr(int64(2048))}, in: sites.ReplaySiteInput{Enabled: true, Mask: ptr(true), SampleRate: ptr(40), RetentionDays: ptr(60), QuotaBytes: ptr(int64(1024))}, enabled: true, mask: true, sample: 40, retention: 60, quota: 1024},
		{name: "environment caps project", env: config.ReplayEnv{Enabled: true, Mask: ptr(true), SampleRate: ptr(5), Retention: ptr(7), QuotaBytes: ptr(int64(512))}, in: sites.ReplaySiteInput{Enabled: true, Mask: ptr(false), SampleRate: ptr(40), RetentionDays: ptr(60), QuotaBytes: ptr(int64(1024))}, enabled: true, mask: true, sample: 5, retention: 7, quota: 512},
	} {
		t.Run(tc.name, func(t *testing.T) {
			got := ResolveSite(tc.env, tc.in)
			if got.Enabled != tc.enabled || got.Mask.V != tc.mask || got.SampleRate.V != tc.sample || got.RetentionDays.V != tc.retention || got.QuotaBytes.V != tc.quota {
				t.Fatalf("unexpected effective settings: %+v", got)
			}
		})
	}
}

func TestResolveStoragePrecedence(t *testing.T) {
	dataDir := filepath.Join("var", "lib", "supermetry")
	stored := StoredStorage{
		Backend: "s3",
		Dir:     "/ui/replay",
		S3:      S3Settings{Endpoint: "https://ui.example", Bucket: "ui", Region: "ui-1", AccessKey: "ui-access", SecretKey: "ui-secret", Prefix: "ui"},
	}
	got := ResolveStorage(config.ReplayEnv{}, dataDir, stored)
	if got.Backend != (Value[string]{V: "s3"}) || got.Dir != (Value[string]{V: "/ui/replay"}) || got.S3 != (Value[S3Settings]{V: stored.S3}) {
		t.Fatalf("ResolveStorage(UI) = %+v", got)
	}

	envS3 := &config.S3Env{Endpoint: "https://env.example", Bucket: "env", Region: "env-1", AccessKey: "env-access", SecretKey: "env-secret", Prefix: "env"}
	got = ResolveStorage(config.ReplayEnv{Dir: "/env/replay", S3: envS3}, dataDir, stored)
	wantS3 := S3Settings{Endpoint: envS3.Endpoint, Bucket: envS3.Bucket, Region: envS3.Region, AccessKey: envS3.AccessKey, SecretKey: envS3.SecretKey, Prefix: envS3.Prefix}
	if got.Backend != (Value[string]{V: "s3", Locked: true}) || got.Dir != (Value[string]{V: "/env/replay", Locked: true}) || got.S3 != (Value[S3Settings]{V: wantS3, Locked: true}) {
		t.Fatalf("ResolveStorage(env) = %+v", got)
	}

	got = ResolveStorage(config.ReplayEnv{}, dataDir, StoredStorage{})
	if got.Backend != (Value[string]{V: "disk"}) || got.Dir != (Value[string]{V: filepath.Join(dataDir, "replay")}) {
		t.Fatalf("ResolveStorage(defaults) = %+v", got)
	}
}

func TestParseSize(t *testing.T) {
	tests := []struct {
		name    string
		raw     string
		want    int64
		wantErr bool
	}{
		{name: "gigabytes", raw: "5GB", want: 5 * 1024 * 1024 * 1024},
		{name: "megabytes", raw: "500MB", want: 500 * 1024 * 1024},
		{name: "short lowercase gigabytes", raw: "1g", want: 1024 * 1024 * 1024},
		{name: "negative", raw: "-1GB", wantErr: true},
		{name: "unknown unit", raw: "5XB", wantErr: true},
	}
	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			got, err := ParseSize(tt.raw)
			if tt.wantErr {
				if err == nil {
					t.Fatalf("ParseSize(%q) = %d, want error", tt.raw, got)
				}
				return
			}
			if err != nil || got != tt.want {
				t.Fatalf("ParseSize(%q) = (%d, %v), want (%d, nil)", tt.raw, got, err, tt.want)
			}
		})
	}
}

func TestStoredStorageSealsS3Secret(t *testing.T) {
	masterKey := bytes.Repeat([]byte{1}, 32)
	want := StoredStorage{
		Backend: "s3",
		Dir:     "/srv/replay",
		S3: S3Settings{
			Endpoint: "https://s3.example.test", Bucket: "replays", Region: "test-1",
			AccessKey: "access", SecretKey: "plain-secret", Prefix: "supermetry",
		},
	}

	sealed, err := SealStoredStorage(masterKey, want)
	if err != nil {
		t.Fatal(err)
	}
	if bytes.Contains(sealed, []byte(want.S3.SecretKey)) {
		t.Fatalf("sealed storage contains plaintext secret: %s", sealed)
	}
	got, err := OpenStoredStorage(masterKey, sealed)
	if err != nil {
		t.Fatal(err)
	}
	if !reflect.DeepEqual(got, want) {
		t.Fatalf("OpenStoredStorage() = %#v, want %#v", got, want)
	}
	if _, err := OpenStoredStorage(bytes.Repeat([]byte{2}, 32), sealed); err == nil {
		t.Fatal("OpenStoredStorage() accepted the wrong master key")
	}
}

func TestResolveSiteEncodesEmptyURLPatternsAsArray(t *testing.T) {
	encoded, err := json.Marshal(ResolveSite(config.ReplayEnv{Enabled: true}, sites.ReplaySiteInput{}))
	if err != nil {
		t.Fatal(err)
	}
	if !strings.Contains(string(encoded), `"url_patterns":[]`) {
		t.Fatalf("encoded settings = %s, want url_patterns as an empty array", encoded)
	}
}

func TestResolveSiteReportsConfiguredValuesAndRestrictions(t *testing.T) {
	got := ResolveSite(config.ReplayEnv{Mask: ptr(true), SampleRate: ptr(5)}, sites.ReplaySiteInput{Enabled: true, Mask: ptr(false), SampleRate: ptr(50)})
	if got.Enabled || !got.ConfiguredEnabled || got.InstanceEnabled || !reflect.DeepEqual(got.Restrictions, []string{"instance_replay_disabled"}) {
		t.Fatalf("gate metadata=%+v", got)
	}
	if got.Mask.Configured == nil || *got.Mask.Configured || !got.Mask.V || !got.Mask.Locked || got.Mask.Restriction != "instance_mask_required" {
		t.Fatalf("mask metadata=%+v", got.Mask)
	}
	if got.SampleRate.Configured == nil || *got.SampleRate.Configured != 50 || got.SampleRate.Maximum == nil || *got.SampleRate.Maximum != 5 || got.SampleRate.V != 5 || got.SampleRate.Restriction != "instance_sample_rate_cap" {
		t.Fatalf("sample metadata=%+v", got.SampleRate)
	}
}
