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
)

const (
	defaultListen       = ":8080"
	defaultEventPath    = "/api/public/v1/events"
	defaultTrackerPath  = "/js/script.js"
	defaultQueueSize    = 65536
	defaultFlushEvery   = time.Second
	defaultSessionFlush = 10 * time.Second
	defaultQueryTimeout = 10 * time.Second
	defaultSignup       = "invite"
	defaultSMTPPort     = 587
	defaultDataDir      = "./data"
	defaultMaxMind      = "GeoLite2-City,GeoLite2-ASN"
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
	DatabaseURL          string
	Listen               string
	BaseURL              string
	EventPath            string
	TrackerPath          string
	GeoIPPath            string
	DataDir              string
	MaxMindAccountID     string
	MaxMindLicenseKey    string
	MaxMindEditions      []string
	SitesFile            string
	QueueSize            int
	FlushEvery           time.Duration
	SessionFlush         time.Duration
	QueryTimeout         time.Duration
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

func Load(getenv func(string) string) (Config, error) {
	geo, geoErr := LoadGeoIP(getenv)
	c := Config{
		DatabaseURL:          getenv("PANEL_DATABASE_URL"),
		Listen:               valueOrDefault(getenv("PANEL_LISTEN"), defaultListen),
		BaseURL:              getenv("PANEL_BASE_URL"),
		EventPath:            valueOrDefault(getenv("PANEL_EVENT_PATH"), defaultEventPath),
		TrackerPath:          valueOrDefault(getenv("PANEL_TRACKER_PATH"), defaultTrackerPath),
		GeoIPPath:            geo.GeoIPPath,
		DataDir:              geo.DataDir,
		MaxMindAccountID:     geo.MaxMindAccountID,
		MaxMindLicenseKey:    geo.MaxMindLicenseKey,
		MaxMindEditions:      geo.MaxMindEditions,
		SitesFile:            getenv("PANEL_SITES_FILE"),
		QueueSize:            defaultQueueSize,
		FlushEvery:           defaultFlushEvery,
		SessionFlush:         defaultSessionFlush,
		QueryTimeout:         defaultQueryTimeout,
		AdminAPIKey:          getenv("PANEL_ADMIN_API_KEY"),
		MetricsToken:         getenv("PANEL_METRICS_TOKEN"),
		Signup:               valueOrDefault(getenv("PANEL_SIGNUP"), defaultSignup),
		AllowPrivateWebhooks: false,
		SMTP: SMTP{
			Host:     getenv("PANEL_SMTP_HOST"),
			Port:     defaultSMTPPort,
			User:     getenv("PANEL_SMTP_USER"),
			Pass:     getenv("PANEL_SMTP_PASS"),
			From:     getenv("PANEL_SMTP_FROM"),
			StartTLS: true,
		},
	}
	var errs []error
	if geoErr != nil {
		errs = append(errs, geoErr)
	}
	if c.DatabaseURL == "" {
		errs = append(errs, errors.New("PANEL_DATABASE_URL is required"))
	} else if err := validateURL(c.DatabaseURL, "PANEL_DATABASE_URL", nil); err != nil {
		errs = append(errs, err)
	}
	if c.BaseURL == "" {
		errs = append(errs, errors.New("PANEL_BASE_URL is required"))
	} else if err := validateURL(c.BaseURL, "PANEL_BASE_URL", map[string]bool{"http": true, "https": true}); err != nil {
		errs = append(errs, err)
	}
	secretKey, err := base64.StdEncoding.Strict().DecodeString(getenv("PANEL_SECRET_KEY"))
	if err != nil {
		errs = append(errs, errors.New("PANEL_SECRET_KEY must be valid base64"))
	} else if len(secretKey) < 32 {
		errs = append(errs, errors.New("PANEL_SECRET_KEY must decode to at least 32 bytes"))
	} else {
		c.SecretKey = secretKey
	}
	if c.Signup != "invite" && c.Signup != "open" {
		errs = append(errs, errors.New("PANEL_SIGNUP must be invite or open"))
	}
	if raw := getenv("PANEL_ALLOW_PRIVATE_WEBHOOKS"); raw != "" {
		value, err := strconv.ParseBool(raw)
		if err != nil {
			errs = append(errs, errors.New("PANEL_ALLOW_PRIVATE_WEBHOOKS must be a boolean"))
		} else {
			c.AllowPrivateWebhooks = value
		}
	}
	parseOptionalBool(getenv("PANEL_REPLAY_ENABLED"), "PANEL_REPLAY_ENABLED", &c.Replay.Enabled, &errs)
	parseOptionalBoolPointer(getenv("PANEL_REPLAY_MASK"), "PANEL_REPLAY_MASK", &c.Replay.Mask, &errs)
	parseOptionalIntPointer(getenv("PANEL_REPLAY_SAMPLE_RATE"), "PANEL_REPLAY_SAMPLE_RATE", 0, 100, &c.Replay.SampleRate, &errs)
	parseOptionalIntPointer(getenv("PANEL_REPLAY_RETENTION_DAYS"), "PANEL_REPLAY_RETENTION_DAYS", 1, 365, &c.Replay.Retention, &errs)
	if raw := getenv("PANEL_REPLAY_QUOTA"); raw != "" {
		value, err := ParseSize(raw)
		if err != nil {
			errs = append(errs, fmt.Errorf("PANEL_REPLAY_QUOTA: %w", err))
		} else {
			c.Replay.QuotaBytes = &value
		}
	}
	c.Replay.Dir = getenv("PANEL_REPLAY_DIR")
	s3 := S3Env{
		Endpoint:  getenv("PANEL_REPLAY_S3_ENDPOINT"),
		Bucket:    getenv("PANEL_REPLAY_S3_BUCKET"),
		Region:    getenv("PANEL_REPLAY_S3_REGION"),
		AccessKey: getenv("PANEL_REPLAY_S3_ACCESS_KEY"),
		SecretKey: getenv("PANEL_REPLAY_S3_SECRET_KEY"),
		Prefix:    getenv("PANEL_REPLAY_S3_PREFIX"),
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
		errs = append(errs, errors.New("PANEL_REPLAY_S3_ENDPOINT, _BUCKET, _REGION, _ACCESS_KEY and _SECRET_KEY must all be set together"))
	}

	for name, path := range map[string]string{
		"PANEL_EVENT_PATH":   c.EventPath,
		"PANEL_TRACKER_PATH": c.TrackerPath,
	} {
		if !strings.HasPrefix(path, "/") {
			errs = append(errs, fmt.Errorf("%s must start with /", name))
		}
	}

	if raw := getenv("PANEL_QUEUE_SIZE"); raw != "" {
		value, err := strconv.Atoi(raw)
		if err != nil || value <= 0 {
			errs = append(errs, fmt.Errorf("PANEL_QUEUE_SIZE must be a positive integer"))
		} else {
			c.QueueSize = value
		}
	}
	if raw := getenv("PANEL_SMTP_PORT"); raw != "" {
		value, err := strconv.Atoi(raw)
		if err != nil || value < 1 || value > 65535 {
			errs = append(errs, errors.New("PANEL_SMTP_PORT must be an integer from 1 to 65535"))
		} else {
			c.SMTP.Port = value
		}
	}
	if raw := getenv("PANEL_SMTP_STARTTLS"); raw != "" {
		value, err := strconv.ParseBool(raw)
		if err != nil {
			errs = append(errs, errors.New("PANEL_SMTP_STARTTLS must be a boolean"))
		} else {
			c.SMTP.StartTLS = value
		}
	}
	if c.SMTP.Host != "" && c.SMTP.From == "" {
		errs = append(errs, errors.New("PANEL_SMTP_FROM is required when PANEL_SMTP_HOST is set"))
	}

	parsePositiveDuration(getenv("PANEL_FLUSH_EVERY"), "PANEL_FLUSH_EVERY", &c.FlushEvery, &errs)
	parsePositiveDuration(getenv("PANEL_SESSION_FLUSH"), "PANEL_SESSION_FLUSH", &c.SessionFlush, &errs)
	parsePositiveDuration(getenv("PANEL_QUERY_TIMEOUT"), "PANEL_QUERY_TIMEOUT", &c.QueryTimeout, &errs)
	if raw := getenv("PANEL_TRUSTED_PROXIES"); raw != "" {
		for _, item := range strings.Split(raw, ",") {
			proxy, err := parseTrustedProxy(strings.TrimSpace(item))
			if err != nil {
				errs = append(errs, fmt.Errorf("PANEL_TRUSTED_PROXIES: %w", err))
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
		GeoIPPath:         getenv("PANEL_GEOIP_PATH"),
		DataDir:           valueOrDefault(getenv("PANEL_DATA_DIR"), defaultDataDir),
		MaxMindAccountID:  getenv("PANEL_MAXMIND_ACCOUNT_ID"),
		MaxMindLicenseKey: getenv("PANEL_MAXMIND_LICENSE_KEY"),
		MaxMindEditions:   strings.Split(valueOrDefault(getenv("PANEL_MAXMIND_EDITIONS"), defaultMaxMind), ","),
	}
	var errs []error
	if (c.MaxMindAccountID == "") != (c.MaxMindLicenseKey == "") {
		errs = append(errs, errors.New("PANEL_MAXMIND_ACCOUNT_ID and PANEL_MAXMIND_LICENSE_KEY must both be set or both be empty"))
	}
	seenEditions := make(map[string]bool, len(c.MaxMindEditions))
	for index, edition := range c.MaxMindEditions {
		edition = strings.TrimSpace(edition)
		c.MaxMindEditions[index] = edition
		if !allowedMaxMindEditions[edition] || seenEditions[edition] {
			errs = append(errs, fmt.Errorf("PANEL_MAXMIND_EDITIONS contains invalid or duplicate edition %q", edition))
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
