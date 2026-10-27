package replay

import (
	"bytes"
	"encoding/base64"
	"encoding/json"
	"errors"
	"fmt"
	"io"
	"path/filepath"

	"supermetry/internal/config"
	platformcrypto "supermetry/internal/platform/crypto"
	"supermetry/internal/sites"
)

const (
	defaultSampleRate    = 15
	defaultRetentionDays = 30
	defaultQuotaBytes    = int64(5 * 1024 * 1024 * 1024)
	replayS3SecretInfo   = "supermetry/replay-s3/v1"
)

type Value[T any] struct {
	V           T      `json:"value"`
	Locked      bool   `json:"locked"`
	Configured  *T     `json:"configured,omitempty"`
	Maximum     *T     `json:"maximum,omitempty"`
	Restriction string `json:"restriction,omitempty"`
}

type Triggers struct {
	Error       bool     `json:"error"`
	RageClick   bool     `json:"rage_click"`
	URL         bool     `json:"url"`
	Manual      bool     `json:"manual"`
	URLPatterns []string `json:"url_patterns"`
}

type SiteSettings struct {
	Enabled           bool         `json:"enabled"`
	ConfiguredEnabled bool         `json:"configured_enabled"`
	InstanceEnabled   bool         `json:"instance_enabled"`
	Restrictions      []string     `json:"restrictions"`
	Mask              Value[bool]  `json:"mask"`
	SampleRate        Value[int]   `json:"sample_rate"`
	RetentionDays     Value[int]   `json:"retention_days"`
	QuotaBytes        Value[int64] `json:"quota_bytes"`
	Triggers          Triggers     `json:"triggers"`
	Consent           string       `json:"consent"`
}

type S3Settings struct {
	Endpoint  string `json:"endpoint"`
	Bucket    string `json:"bucket"`
	Region    string `json:"region"`
	AccessKey string `json:"access_key"`
	SecretKey string `json:"-"`
	Prefix    string `json:"prefix"`
}

type StoredStorage struct {
	Backend string     `json:"backend,omitempty"`
	Dir     string     `json:"dir,omitempty"`
	S3      S3Settings `json:"s3,omitempty"`
}

type StorageSettings struct {
	Backend Value[string]     `json:"backend"`
	Dir     Value[string]     `json:"dir"`
	S3      Value[S3Settings] `json:"s3"`
}

type storedStorageJSON struct {
	Backend string       `json:"backend,omitempty"`
	Dir     string       `json:"dir,omitempty"`
	S3      storedS3JSON `json:"s3,omitempty"`
}

type storedS3JSON struct {
	Endpoint  string `json:"endpoint,omitempty"`
	Bucket    string `json:"bucket,omitempty"`
	Region    string `json:"region,omitempty"`
	AccessKey string `json:"access_key,omitempty"`
	SecretKey string `json:"secret_key,omitempty"`
	Prefix    string `json:"prefix,omitempty"`
}

func ResolveSite(env config.ReplayEnv, in sites.ReplaySiteInput) SiteSettings {
	settings := SiteSettings{
		Enabled:           env.Enabled && in.Enabled,
		ConfiguredEnabled: in.Enabled,
		InstanceEnabled:   env.Enabled,
		Restrictions:      []string{},
		Mask:              resolveMask(in.Mask, env.Mask),
		SampleRate:        resolveCap(defaultSampleRate, in.SampleRate, env.SampleRate, "instance_sample_rate_cap"),
		RetentionDays:     resolveCap(defaultRetentionDays, in.RetentionDays, env.Retention, "instance_retention_days_cap"),
		QuotaBytes:        resolveCap(defaultQuotaBytes, in.QuotaBytes, env.QuotaBytes, "instance_quota_bytes_cap"),
		Triggers: Triggers{
			Error: true, RageClick: true, URL: true, Manual: true,
			URLPatterns: append([]string{}, in.Triggers.URLPatterns...), // [] not null in JSON
		},
		Consent: "auto",
	}
	applyOrgRetentionCap(&settings, in.OrgRetentionCapDays)
	if !env.Enabled {
		settings.Restrictions = append(settings.Restrictions, "instance_replay_disabled")
	}
	if !in.Enabled {
		settings.Restrictions = append(settings.Restrictions, "project_replay_disabled")
	}
	settings.Triggers.Error = pick(in.Triggers.Error, settings.Triggers.Error)
	settings.Triggers.RageClick = pick(in.Triggers.RageClick, settings.Triggers.RageClick)
	settings.Triggers.URL = pick(in.Triggers.URL, settings.Triggers.URL)
	settings.Triggers.Manual = pick(in.Triggers.Manual, settings.Triggers.Manual)
	if in.Consent != nil {
		settings.Consent = *in.Consent
	}
	return settings
}

func ResolveStorage(env config.ReplayEnv, dataDir string, in StoredStorage) StorageSettings {
	backend := in.Backend
	if backend == "" {
		backend = "disk"
	}
	dir := in.Dir
	if dir == "" {
		dir = filepath.Join(dataDir, "replay")
	}
	settings := StorageSettings{
		Backend: Value[string]{V: backend},
		Dir:     Value[string]{V: dir},
		S3:      Value[S3Settings]{V: in.S3},
	}
	if env.Dir != "" {
		settings.Dir = Value[string]{V: env.Dir, Locked: true}
	}
	if env.S3 != nil {
		settings.Backend = Value[string]{V: "s3", Locked: true}
		settings.S3 = Value[S3Settings]{V: S3Settings{
			Endpoint: env.S3.Endpoint, Bucket: env.S3.Bucket, Region: env.S3.Region,
			AccessKey: env.S3.AccessKey, SecretKey: env.S3.SecretKey, Prefix: env.S3.Prefix,
		}, Locked: true}
	}
	return settings
}

func ParseSize(raw string) (int64, error) {
	return config.ParseSize(raw)
}

// SealStoredStorage encodes storage settings for instance_settings. S3 secret
// keys are authenticated and encrypted before they enter the JSON document.
func SealStoredStorage(masterKey []byte, in StoredStorage) ([]byte, error) {
	wire := storedStorageJSON{
		Backend: in.Backend,
		Dir:     in.Dir,
		S3: storedS3JSON{
			Endpoint: in.S3.Endpoint, Bucket: in.S3.Bucket, Region: in.S3.Region,
			AccessKey: in.S3.AccessKey, Prefix: in.S3.Prefix,
		},
	}
	if in.S3.SecretKey != "" {
		box, err := platformcrypto.NewSecretBox(masterKey, replayS3SecretInfo)
		if err != nil {
			return nil, fmt.Errorf("create replay S3 secret box: %w", err)
		}
		wire.S3.SecretKey = base64.StdEncoding.EncodeToString(box.Seal([]byte(in.S3.SecretKey)))
	}
	encoded, err := json.Marshal(wire)
	if err != nil {
		return nil, fmt.Errorf("encode replay storage settings: %w", err)
	}
	return encoded, nil
}

// OpenStoredStorage strictly decodes instance_settings and authenticates its
// sealed S3 secret key.
func OpenStoredStorage(masterKey, raw []byte) (StoredStorage, error) {
	if len(bytes.TrimSpace(raw)) == 0 {
		return StoredStorage{}, nil
	}
	var wire storedStorageJSON
	decoder := json.NewDecoder(bytes.NewReader(raw))
	decoder.DisallowUnknownFields()
	if err := decoder.Decode(&wire); err != nil {
		return StoredStorage{}, fmt.Errorf("decode replay storage settings: %w", err)
	}
	if _, err := decoder.Token(); !errors.Is(err, io.EOF) {
		return StoredStorage{}, errors.New("unexpected data after replay storage settings")
	}

	result := StoredStorage{
		Backend: wire.Backend,
		Dir:     wire.Dir,
		S3: S3Settings{
			Endpoint: wire.S3.Endpoint, Bucket: wire.S3.Bucket, Region: wire.S3.Region,
			AccessKey: wire.S3.AccessKey, Prefix: wire.S3.Prefix,
		},
	}
	if wire.S3.SecretKey == "" {
		return result, nil
	}
	sealed, err := base64.StdEncoding.Strict().DecodeString(wire.S3.SecretKey)
	if err != nil {
		return StoredStorage{}, errors.New("decode sealed replay S3 secret")
	}
	box, err := platformcrypto.NewSecretBox(masterKey, replayS3SecretInfo)
	if err != nil {
		return StoredStorage{}, fmt.Errorf("create replay S3 secret box: %w", err)
	}
	plain, err := box.Open(sealed)
	if err != nil {
		return StoredStorage{}, fmt.Errorf("open replay S3 secret: %w", err)
	}
	result.S3.SecretKey = string(plain)
	return result, nil
}

func resolve[T any](fallback T, ui, environment *T) Value[T] {
	if environment != nil {
		return Value[T]{V: *environment, Locked: true}
	}
	if ui != nil {
		return Value[T]{V: *ui}
	}
	return Value[T]{V: fallback}
}

func pick[T any](value *T, fallback T) T {
	if value != nil {
		return *value
	}
	return fallback
}

// A mandatory mask combines with project preferences using OR; numeric instance
// values are ceilings, never replacements for stricter project preferences.
func resolveMask(project, instance *bool) Value[bool] {
	configured := pick(project, false)
	value := Value[bool]{V: configured, Configured: &configured}
	if instance != nil && *instance {
		value.V = true
		value.Locked = true
		if !configured {
			value.Restriction = "instance_mask_required"
		}
	}
	return value
}

func resolveCap[T ~int | ~int64](fallback T, project, instance *T, reason string) Value[T] {
	configured := pick(project, fallback)
	value := Value[T]{V: configured, Configured: &configured}
	if instance != nil {
		maximum := *instance
		value.Maximum = &maximum
		value.Locked = true
		if maximum < configured {
			value.V = maximum
			value.Restriction = reason
		}
	}
	return value
}

// OrgRetentionCapped is the restriction reported when the owning organization's
// cap lowers the effective retention below what the site and instance allow.
const OrgRetentionCapped = "org_retention_capped"

// applyOrgRetentionCap lowers retention to the organization's cap. The effective
// value is min(instance cap, organization cap, site setting); the maximum shown
// to the UI is the smaller of the instance and organization ceilings. A nil cap
// changes nothing.
func applyOrgRetentionCap(settings *SiteSettings, orgCap *int) {
	if orgCap == nil {
		return
	}
	limit := *orgCap
	value := &settings.RetentionDays
	if value.Maximum == nil || limit < *value.Maximum {
		value.Maximum = &limit
	}
	if limit < value.V {
		value.V = limit
		value.Restriction = OrgRetentionCapped
		settings.Restrictions = append(settings.Restrictions, OrgRetentionCapped)
	}
}
