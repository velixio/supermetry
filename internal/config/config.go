package config

import (
	"encoding/base64"
	"errors"
	"fmt"
	"net/netip"
	"net/url"
	"strconv"
	"strings"
	"time"

	"supermetry/internal/privacy"
)

const (
	defaultListen           = ":8080"
	defaultEventPath        = "/api/public/v1/events"
	defaultTrackerPath      = "/js/script.js"
	defaultQueueSize        = 65536
	defaultMaxOrgsPerUser   = 3
	defaultMaxSitesPerOrg   = 10
	defaultDedupEntries     = 250000
	defaultFlushEvery       = time.Second
	defaultSessionFlush     = 10 * time.Second
	defaultQueryTimeout     = 10 * time.Second
	defaultIngestRateIP     = 1_000
	defaultIngestRateSite   = 5_000
	defaultIngestRateGlobal = 10_000
	defaultHTTPReadTimeout  = 30 * time.Second
	defaultHTTPWriteTimeout = 60 * time.Second
	defaultSignup           = "invite"
	defaultSMTPPort         = 587
	defaultDataDir          = "./data"
	defaultMaxMind          = "GeoLite2-City,GeoLite2-ASN"
	defaultHomepageURL      = "https://supermetry.com"
)

var allowedMaxMindEditions = map[string]bool{
	"GeoLite2-City":    true,
	"GeoLite2-ASN":     true,
	"GeoLite2-Country": true,
}

type SMTP struct {
	Host     string
	Port     int
	User     string
	Pass     string
	From     string
	StartTLS bool
}

type Config struct {
	Tracking          TrackingMode
	TrackingPaths     privacy.PathPolicy
	DatabaseURL       string
	Listen            string
	BaseURL           string
	HomepageURL       string
	EventPath         string
	TrackerPath       string
	GeoIPPath         string
	DataDir           string
	MaxMindAccountID  string
	MaxMindLicenseKey string
	MaxMindEditions   []string
	SitesFile         string
	QueueSize         int
	// MaxOrgsPerUser and MaxSitesPerOrg limit what a user who is not an instance
	// admin may create. Instance admins are unlimited.
	MaxOrgsPerUser       int
	MaxSitesPerOrg       int
	DedupEntries         int
	FlushEvery           time.Duration
	SessionFlush         time.Duration
	QueryTimeout         time.Duration
	IngestRateIP         int
	IngestRateSite       int
	IngestRateGlobal     int
	HTTPReadTimeout      time.Duration
	HTTPWriteTimeout     time.Duration
	AdminAPIKey          string
	MetricsToken         string
	TrustedProxies       []netip.Prefix
	SecretKey            []byte
	Signup               string
	SMTP                 SMTP
	AllowPrivateWebhooks bool
	Replay               ReplayEnv
}

type ReplayEnv struct {
	Enabled    bool
	Mask       *bool
	SampleRate *int
	Retention  *int
	QuotaBytes *int64
	Dir        string
	S3         *S3Env
}

type S3Env struct {
	Endpoint  string
	Bucket    string
	Region    string
	AccessKey string
	SecretKey string
	Prefix    string
}

// Load accepts value-only lookup functions. Use LoadLookup for process settings
// so explicitly empty tracking values cannot be mistaken for absent values.
func Load(getenv func(string) string) (Config, error) {
	return LoadLookup(func(key string) (string, bool) { value := getenv(key); return value, value != "" })
}

// LoadLookup preserves environment presence, including explicitly empty values.
func LoadLookup(lookup func(string) (string, bool)) (Config, error) {
	getenv := func(key string) string { value, _ := lookup(key); return value }
	tracking, trackingErr := ParseTrackingMode(lookup(TrackingModeEnv))
	trackingPaths, trackingPathsErr := ParseTrackingPublicPaths(lookup(TrackingPublicPathsEnv))
	geo, geoErr := LoadGeoIP(getenv)
	c := Config{
		Tracking:             tracking,
		TrackingPaths:        trackingPaths,
		DatabaseURL:          getenv("SUPERMETRY_DATABASE_URL"),
		Listen:               valueOrDefault(getenv("SUPERMETRY_LISTEN"), defaultListen),
		BaseURL:              getenv("SUPERMETRY_BASE_URL"),
		HomepageURL:          valueOrDefault(getenv("SUPERMETRY_HOMEPAGE_URL"), defaultHomepageURL),
		EventPath:            valueOrDefault(getenv("SUPERMETRY_EVENT_PATH"), defaultEventPath),
		TrackerPath:          valueOrDefault(getenv("SUPERMETRY_TRACKER_PATH"), defaultTrackerPath),
		GeoIPPath:            geo.GeoIPPath,
		DataDir:              geo.DataDir,
		MaxMindAccountID:     geo.MaxMindAccountID,
		MaxMindLicenseKey:    geo.MaxMindLicenseKey,
		MaxMindEditions:      geo.MaxMindEditions,
		SitesFile:            getenv("SUPERMETRY_SITES_FILE"),
		QueueSize:            defaultQueueSize,
		MaxOrgsPerUser:       defaultMaxOrgsPerUser,
		MaxSitesPerOrg:       defaultMaxSitesPerOrg,
		DedupEntries:         defaultDedupEntries,
		FlushEvery:           defaultFlushEvery,
		SessionFlush:         defaultSessionFlush,
		QueryTimeout:         defaultQueryTimeout,
		IngestRateIP:         defaultIngestRateIP,
		IngestRateSite:       defaultIngestRateSite,
		IngestRateGlobal:     defaultIngestRateGlobal,
		HTTPReadTimeout:      defaultHTTPReadTimeout,
		HTTPWriteTimeout:     defaultHTTPWriteTimeout,
		AdminAPIKey:          getenv("SUPERMETRY_ADMIN_API_KEY"),
		MetricsToken:         getenv("SUPERMETRY_METRICS_TOKEN"),
		Signup:               valueOrDefault(getenv("SUPERMETRY_SIGNUP"), defaultSignup),
		AllowPrivateWebhooks: false,
		SMTP: SMTP{
			Host:     getenv("SUPERMETRY_SMTP_HOST"),
			Port:     defaultSMTPPort,
			User:     getenv("SUPERMETRY_SMTP_USER"),
			Pass:     getenv("SUPERMETRY_SMTP_PASS"),
			From:     getenv("SUPERMETRY_SMTP_FROM"),
			StartTLS: true,
		},
	}
	var errs []error
	if trackingPathsErr != nil {
		errs = append(errs, trackingPathsErr)
	}
	if trackingErr != nil {
		errs = append(errs, trackingErr)
	}
	if geoErr != nil {
		errs = append(errs, geoErr)
	}
	if c.DatabaseURL == "" {
		errs = append(errs, errors.New("SUPERMETRY_DATABASE_URL is required"))
	} else if err := validateURL(c.DatabaseURL, "SUPERMETRY_DATABASE_URL", nil); err != nil {
		errs = append(errs, err)
	}
	if c.BaseURL == "" {
		errs = append(errs, errors.New("SUPERMETRY_BASE_URL is required"))
	} else if err := validateURL(c.BaseURL, "SUPERMETRY_BASE_URL", map[string]bool{"http": true, "https": true}); err != nil {
		errs = append(errs, err)
	}
	if err := validateURL(c.HomepageURL, "SUPERMETRY_HOMEPAGE_URL", map[string]bool{"http": true, "https": true}); err != nil {
		errs = append(errs, err)
	}
	secretKey, err := base64.StdEncoding.Strict().DecodeString(getenv("SUPERMETRY_SECRET_KEY"))
	if err != nil {
		errs = append(errs, errors.New("SUPERMETRY_SECRET_KEY must be valid base64"))
	} else if len(secretKey) < 32 {
		errs = append(errs, errors.New("SUPERMETRY_SECRET_KEY must decode to at least 32 bytes"))
	} else {
		c.SecretKey = secretKey
	}
	if c.Signup != "invite" && c.Signup != "open" {
		errs = append(errs, errors.New("SUPERMETRY_SIGNUP must be invite or open"))
	}
	if raw := getenv("SUPERMETRY_ALLOW_PRIVATE_WEBHOOKS"); raw != "" {
		value, err := strconv.ParseBool(raw)
		if err != nil {
			errs = append(errs, errors.New("SUPERMETRY_ALLOW_PRIVATE_WEBHOOKS must be a boolean"))
		} else {
			c.AllowPrivateWebhooks = value
		}
	}
	parseOptionalBool(getenv("SUPERMETRY_REPLAY_ENABLED"), "SUPERMETRY_REPLAY_ENABLED", &c.Replay.Enabled, &errs)
	parseOptionalBoolPointer(getenv("SUPERMETRY_REPLAY_MASK"), "SUPERMETRY_REPLAY_MASK", &c.Replay.Mask, &errs)
	parseOptionalIntPointer(getenv("SUPERMETRY_REPLAY_SAMPLE_RATE"), "SUPERMETRY_REPLAY_SAMPLE_RATE", 0, 100, &c.Replay.SampleRate, &errs)
	parseOptionalIntPointer(getenv("SUPERMETRY_REPLAY_RETENTION_DAYS"), "SUPERMETRY_REPLAY_RETENTION_DAYS", 1, 365, &c.Replay.Retention, &errs)
	if raw := getenv("SUPERMETRY_REPLAY_QUOTA"); raw != "" {
		value, err := ParseSize(raw)
		if err != nil {
			errs = append(errs, fmt.Errorf("SUPERMETRY_REPLAY_QUOTA: %w", err))
		} else {
			c.Replay.QuotaBytes = &value
		}
	}
	c.Replay.Dir = getenv("SUPERMETRY_REPLAY_DIR")
	s3 := S3Env{
		Endpoint:  getenv("SUPERMETRY_REPLAY_S3_ENDPOINT"),
		Bucket:    getenv("SUPERMETRY_REPLAY_S3_BUCKET"),
		Region:    getenv("SUPERMETRY_REPLAY_S3_REGION"),
		AccessKey: getenv("SUPERMETRY_REPLAY_S3_ACCESS_KEY"),
		SecretKey: getenv("SUPERMETRY_REPLAY_S3_SECRET_KEY"),
		Prefix:    getenv("SUPERMETRY_REPLAY_S3_PREFIX"),
	}
	if raw := getenv("SUPERMETRY_REPLAY_S3_URL"); raw != "" {
		parsed, err := parseS3URL(raw)
		if err != nil {
			errs = append(errs, fmt.Errorf("SUPERMETRY_REPLAY_S3_URL: %w", err))
		} else {
			s3 = parsed
		}
	}
	// The prefix is optional. A partial S3 setup is an error rather than a
	// silent fall back to disk.
	required := []string{s3.Endpoint, s3.Bucket, s3.Region, s3.AccessKey, s3.SecretKey}
	set := 0
	for _, v := range required {
		if v != "" {
			set++
		}
	}
	switch {
	case set == len(required):
		c.Replay.S3 = &s3
	case set > 0 || s3.Prefix != "":
		errs = append(errs, errors.New("SUPERMETRY_REPLAY_S3_ENDPOINT, _BUCKET, _REGION, _ACCESS_KEY and _SECRET_KEY must all be set together"))
	}

	for name, path := range map[string]string{
		"SUPERMETRY_EVENT_PATH":   c.EventPath,
		"SUPERMETRY_TRACKER_PATH": c.TrackerPath,
	} {
		if !strings.HasPrefix(path, "/") {
			errs = append(errs, fmt.Errorf("%s must start with /", name))
		}
	}

	for _, limit := range []struct {
		name   string
		target *int
	}{
		{"SUPERMETRY_MAX_ORGS_PER_USER", &c.MaxOrgsPerUser},
		{"SUPERMETRY_MAX_SITES_PER_ORG", &c.MaxSitesPerOrg},
	} {
		if raw := getenv(limit.name); raw != "" {
			value, err := strconv.Atoi(raw)
			if err != nil || value <= 0 {
				errs = append(errs, fmt.Errorf("%s must be a positive integer", limit.name))
			} else {
				*limit.target = value
			}
		}
	}
	if raw := getenv("SUPERMETRY_QUEUE_SIZE"); raw != "" {
		value, err := strconv.Atoi(raw)
		if err != nil || value <= 0 {
			errs = append(errs, fmt.Errorf("SUPERMETRY_QUEUE_SIZE must be a positive integer"))
		} else {
			c.QueueSize = value
		}
	}
	if raw := getenv("SUPERMETRY_DEDUP_ENTRIES"); raw != "" {
		value, err := strconv.Atoi(raw)
		if err != nil || value <= 0 {
			errs = append(errs, fmt.Errorf("SUPERMETRY_DEDUP_ENTRIES must be a positive integer"))
		} else {
			c.DedupEntries = value
		}
	}
	if raw := getenv("SUPERMETRY_SMTP_PORT"); raw != "" {
		value, err := strconv.Atoi(raw)
		if err != nil || value < 1 || value > 65535 {
			errs = append(errs, errors.New("SUPERMETRY_SMTP_PORT must be an integer from 1 to 65535"))
		} else {
			c.SMTP.Port = value
		}
	}
	if raw := getenv("SUPERMETRY_SMTP_STARTTLS"); raw != "" {
		value, err := strconv.ParseBool(raw)
		if err != nil {
			errs = append(errs, errors.New("SUPERMETRY_SMTP_STARTTLS must be a boolean"))
		} else {
			c.SMTP.StartTLS = value
		}
	}
	if c.SMTP.Host != "" && c.SMTP.From == "" {
		errs = append(errs, errors.New("SUPERMETRY_SMTP_FROM is required when SUPERMETRY_SMTP_HOST is set"))
	}

	parsePositiveDuration(getenv("SUPERMETRY_FLUSH_EVERY"), "SUPERMETRY_FLUSH_EVERY", &c.FlushEvery, &errs)
	parsePositiveDuration(getenv("SUPERMETRY_SESSION_FLUSH"), "SUPERMETRY_SESSION_FLUSH", &c.SessionFlush, &errs)
	parseDurationRange(getenv("SUPERMETRY_QUERY_TIMEOUT"), "SUPERMETRY_QUERY_TIMEOUT", time.Second, 5*time.Minute, &c.QueryTimeout, &errs)
	parsePositiveInt(getenv("SUPERMETRY_INGEST_RATE_IP"), "SUPERMETRY_INGEST_RATE_IP", &c.IngestRateIP, &errs)
	parsePositiveInt(getenv("SUPERMETRY_INGEST_RATE_SITE"), "SUPERMETRY_INGEST_RATE_SITE", &c.IngestRateSite, &errs)
	parsePositiveInt(getenv("SUPERMETRY_INGEST_RATE_GLOBAL"), "SUPERMETRY_INGEST_RATE_GLOBAL", &c.IngestRateGlobal, &errs)
	parsePositiveDuration(getenv("SUPERMETRY_HTTP_READ_TIMEOUT"), "SUPERMETRY_HTTP_READ_TIMEOUT", &c.HTTPReadTimeout, &errs)
	parsePositiveDuration(getenv("SUPERMETRY_HTTP_WRITE_TIMEOUT"), "SUPERMETRY_HTTP_WRITE_TIMEOUT", &c.HTTPWriteTimeout, &errs)
	if raw := getenv("SUPERMETRY_TRUSTED_PROXIES"); raw != "" {
		for _, item := range strings.Split(raw, ",") {
			proxy, err := parseTrustedProxy(strings.TrimSpace(item))
			if err != nil {
				errs = append(errs, fmt.Errorf("SUPERMETRY_TRUSTED_PROXIES: %w", err))
				continue
			}
			c.TrustedProxies = append(c.TrustedProxies, proxy)
		}
	}

	return c, errors.Join(errs...)
}

// LoadGeoIP loads only the settings needed by the geoip command. It does not
// require database or HTTP server configuration.
func LoadGeoIP(getenv func(string) string) (Config, error) {
	c := Config{
		GeoIPPath:         getenv("SUPERMETRY_GEOIP_PATH"),
		DataDir:           valueOrDefault(getenv("SUPERMETRY_DATA_DIR"), defaultDataDir),
		MaxMindAccountID:  getenv("SUPERMETRY_MAXMIND_ACCOUNT_ID"),
		MaxMindLicenseKey: getenv("SUPERMETRY_MAXMIND_LICENSE_KEY"),
		MaxMindEditions:   strings.Split(valueOrDefault(getenv("SUPERMETRY_MAXMIND_EDITIONS"), defaultMaxMind), ","),
	}
	var errs []error
	if (c.MaxMindAccountID == "") != (c.MaxMindLicenseKey == "") {
		errs = append(errs, errors.New("SUPERMETRY_MAXMIND_ACCOUNT_ID and SUPERMETRY_MAXMIND_LICENSE_KEY must both be set or both be empty"))
	}
	seenEditions := make(map[string]bool, len(c.MaxMindEditions))
	for index, edition := range c.MaxMindEditions {
		edition = strings.TrimSpace(edition)
		c.MaxMindEditions[index] = edition
		if !allowedMaxMindEditions[edition] || seenEditions[edition] {
			errs = append(errs, fmt.Errorf("SUPERMETRY_MAXMIND_EDITIONS contains invalid or duplicate edition %q", edition))
			continue
		}
		seenEditions[edition] = true
	}
	return c, errors.Join(errs...)
}

func parseTrustedProxy(raw string) (netip.Prefix, error) {
	if prefix, err := netip.ParsePrefix(raw); err == nil {
		return prefix.Masked(), nil
	}
	addr, err := netip.ParseAddr(raw)
	if err != nil {
		return netip.Prefix{}, fmt.Errorf("%q is not an IP address or prefix", raw)
	}
	return netip.PrefixFrom(addr, addr.BitLen()), nil
}

func valueOrDefault(value, fallback string) string {
	if value == "" {
		return fallback
	}
	return value
}

func validateURL(raw, name string, schemes map[string]bool) error {
	parsed, err := url.Parse(raw)
	if err != nil || parsed.Scheme == "" || parsed.Host == "" {
		return fmt.Errorf("%s must be a valid URL", name)
	}
	if schemes != nil && !schemes[parsed.Scheme] {
		return fmt.Errorf("%s must use http or https", name)
	}
	return nil
}

func parsePositiveDuration(raw, name string, target *time.Duration, errs *[]error) {
	if raw == "" {
		return
	}
	value, err := time.ParseDuration(raw)
	if err != nil || value <= 0 {
		*errs = append(*errs, fmt.Errorf("%s must be a positive duration", name))
		return
	}
	*target = value
}

func parseDurationRange(raw, name string, minimum, maximum time.Duration, target *time.Duration, errs *[]error) {
	if raw == "" {
		return
	}
	value, err := time.ParseDuration(raw)
	if err != nil || value < minimum || value > maximum {
		*errs = append(*errs, fmt.Errorf("%s must be a duration from %s to %s", name, minimum, maximum))
		return
	}
	*target = value
}

func parsePositiveInt(raw, name string, target *int, errs *[]error) {
	if raw == "" {
		return
	}
	value, err := strconv.Atoi(raw)
	if err != nil || value <= 0 || value > int(^uint(0)>>1)/2 {
		*errs = append(*errs, fmt.Errorf("%s must be a positive integer no greater than %d", name, int(^uint(0)>>1)/2))
		return
	}
	*target = value
}

func parseOptionalBool(raw, name string, target *bool, errs *[]error) {
	if raw == "" {
		return
	}
	value, err := strconv.ParseBool(raw)
	if err != nil {
		*errs = append(*errs, fmt.Errorf("%s must be a boolean", name))
		return
	}
	*target = value
}

func parseOptionalBoolPointer(raw, name string, target **bool, errs *[]error) {
	if raw == "" {
		return
	}
	value, err := strconv.ParseBool(raw)
	if err != nil {
		*errs = append(*errs, fmt.Errorf("%s must be a boolean", name))
		return
	}
	*target = &value
}

func parseOptionalIntPointer(raw, name string, minimum, maximum int, target **int, errs *[]error) {
	if raw == "" {
		return
	}
	value, err := strconv.Atoi(raw)
	if err != nil || value < minimum || value > maximum {
		*errs = append(*errs, fmt.Errorf("%s must be an integer from %d to %d", name, minimum, maximum))
		return
	}
	*target = &value
}

// ParseSize parses a positive binary size written with a B, KB, MB, GB or TB
// suffix. The short K, M, G and T suffixes are accepted as aliases.
func ParseSize(raw string) (int64, error) {
	value := strings.ToUpper(strings.TrimSpace(raw))
	if value == "" {
		return 0, errors.New("size must not be empty")
	}

	digitEnd := 0
	for digitEnd < len(value) && value[digitEnd] >= '0' && value[digitEnd] <= '9' {
		digitEnd++
	}
	if digitEnd == 0 {
		return 0, fmt.Errorf("invalid size %q", raw)
	}
	number, err := strconv.ParseInt(value[:digitEnd], 10, 64)
	if err != nil || number <= 0 {
		return 0, fmt.Errorf("invalid size %q", raw)
	}
	multipliers := map[string]int64{
		"B":  1,
		"K":  1 << 10,
		"KB": 1 << 10,
		"M":  1 << 20,
		"MB": 1 << 20,
		"G":  1 << 30,
		"GB": 1 << 30,
		"T":  1 << 40,
		"TB": 1 << 40,
	}
	multiplier, ok := multipliers[value[digitEnd:]]
	if !ok || number > int64(^uint64(0)>>1)/multiplier {
		return 0, fmt.Errorf("invalid size %q", raw)
	}
	return number * multiplier, nil
}

// parseS3URL reads https://ACCESS:SECRET@host/bucket/optional/prefix?region=r.
// Credentials are percent-decoded. The error never echoes the raw value.
func parseS3URL(raw string) (S3Env, error) {
	u, err := url.Parse(raw)
	if err != nil {
		return S3Env{}, errors.New("not a valid URL")
	}
	if (u.Scheme != "http" && u.Scheme != "https") || u.Host == "" {
		return S3Env{}, errors.New("must look like https://ACCESS_KEY:SECRET_KEY@host/bucket[/prefix]?region=REGION")
	}
	secret, hasSecret := u.User.Password()
	bucket, prefix, _ := strings.Cut(strings.Trim(u.EscapedPath(), "/"), "/")
	bucket, err = url.PathUnescape(bucket)
	if err != nil {
		return S3Env{}, errors.New("bucket is not valid percent-encoding")
	}
	prefix, err = url.PathUnescape(strings.Trim(prefix, "/"))
	if err != nil {
		return S3Env{}, errors.New("prefix is not valid percent-encoding")
	}
	out := S3Env{
		Endpoint:  u.Scheme + "://" + u.Host,
		Bucket:    bucket,
		Region:    u.Query().Get("region"),
		AccessKey: u.User.Username(),
		SecretKey: secret,
		Prefix:    prefix,
	}
	if !hasSecret || out.AccessKey == "" || out.Bucket == "" || out.Region == "" {
		return S3Env{}, errors.New("needs access key, secret key, bucket and a region query parameter")
	}
	return out, nil
}
