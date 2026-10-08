package replay

import (
	"bytes"
	"path/filepath"
	"reflect"
	"testing"

	"panel/internal/config"
	"panel/internal/sites"
)

func ptr[T any](value T) *T { return &value }

func TestResolveSitePrecedence(t *testing.T) {
	const defaultQuota = int64(5 * 1024 * 1024 * 1024)
	tests := []struct {
		name string
		env  config.ReplayEnv
		in   sites.ReplaySiteInput
		want SiteSettings
	}{
		{
			name: "environment and UI unset use defaults",
			want: SiteSettings{
				Mask:          Value[bool]{V: false},
				SampleRate:    Value[int]{V: 15},
				RetentionDays: Value[int]{V: 30},
				QuotaBytes:    Value[int64]{V: defaultQuota},
				Triggers: Triggers{
					Error: true, RageClick: true, URL: true, Manual: true,
				},
				Consent: "auto",
			},
		},
		{
			name: "UI values override defaults",
			env:  config.ReplayEnv{Enabled: true},
			in: sites.ReplaySiteInput{
				Enabled: true, Mask: ptr(true), SampleRate: ptr(40), RetentionDays: ptr(60), QuotaBytes: ptr(int64(1024)),
				Triggers: sites.ReplayTriggersInput{Error: ptr(false), RageClick: ptr(false), URL: ptr(false), Manual: ptr(false), URLPatterns: []string{"/checkout/*"}},
				Consent:  ptr("always"),
			},
			want: SiteSettings{
				Enabled:       true,
				Mask:          Value[bool]{V: true},
				SampleRate:    Value[int]{V: 40},
				RetentionDays: Value[int]{V: 60},
				QuotaBytes:    Value[int64]{V: 1024},
				Triggers:      Triggers{Error: false, RageClick: false, URL: false, Manual: false, URLPatterns: []string{"/checkout/*"}},
				Consent:       "always",
			},
		},
		{
			name: "environment values override and lock UI values",
			env: config.ReplayEnv{
				Enabled: true, Mask: ptr(false), SampleRate: ptr(75), Retention: ptr(120), QuotaBytes: ptr(int64(2048)),
			},
			in: sites.ReplaySiteInput{Enabled: true, Mask: ptr(true), SampleRate: ptr(40), RetentionDays: ptr(60), QuotaBytes: ptr(int64(1024))},
			want: SiteSettings{
				Enabled:       true,
				Mask:          Value[bool]{V: false, Locked: true},
				SampleRate:    Value[int]{V: 75, Locked: true},
				RetentionDays: Value[int]{V: 120, Locked: true},
				QuotaBytes:    Value[int64]{V: 2048, Locked: true},
				Triggers:      Triggers{Error: true, RageClick: true, URL: true, Manual: true},
				Consent:       "auto",
			},
		},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			if got := ResolveSite(tt.env, tt.in); !reflect.DeepEqual(got, tt.want) {
				t.Fatalf("ResolveSite() = %#v, want %#v", got, tt.want)
			}
		})
	}
}

func TestResolveStoragePrecedence(t *testing.T) {
	dataDir := filepath.Join("var", "lib", "panel")
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
			AccessKey: "access", SecretKey: "plain-secret", Prefix: "panel",
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
