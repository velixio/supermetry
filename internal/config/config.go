package config

import (
	"errors"
	"fmt"
	"net/url"
	"strconv"
	"strings"
	"time"
)

const (
	defaultListen       = ":8080"
	defaultEventPath    = "/api/event"
	defaultTrackerPath  = "/js/script.js"
	defaultQueueSize    = 65536
	defaultFlushEvery   = time.Second
	defaultSessionFlush = 10 * time.Second
	defaultQueryTimeout = 10 * time.Second
)

type Config struct {
	DatabaseURL  string
	Listen       string
	BaseURL      string
	EventPath    string
	TrackerPath  string
	GeoIPPath    string
	SitesFile    string
	QueueSize    int
	FlushEvery   time.Duration
	SessionFlush time.Duration
	QueryTimeout time.Duration
	AdminAPIKey  string
}

func Load(getenv func(string) string) (Config, error) {
	c := Config{
		DatabaseURL:  getenv("PANEL_DATABASE_URL"),
		Listen:       valueOrDefault(getenv("PANEL_LISTEN"), defaultListen),
		BaseURL:      getenv("PANEL_BASE_URL"),
		EventPath:    valueOrDefault(getenv("PANEL_EVENT_PATH"), defaultEventPath),
		TrackerPath:  valueOrDefault(getenv("PANEL_TRACKER_PATH"), defaultTrackerPath),
		GeoIPPath:    getenv("PANEL_GEOIP_PATH"),
		SitesFile:    getenv("PANEL_SITES_FILE"),
		QueueSize:    defaultQueueSize,
		FlushEvery:   defaultFlushEvery,
		SessionFlush: defaultSessionFlush,
		QueryTimeout: defaultQueryTimeout,
		AdminAPIKey:  getenv("PANEL_ADMIN_API_KEY"),
	}

	var errs []error
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

	parsePositiveDuration(getenv("PANEL_FLUSH_EVERY"), "PANEL_FLUSH_EVERY", &c.FlushEvery, &errs)
	parsePositiveDuration(getenv("PANEL_SESSION_FLUSH"), "PANEL_SESSION_FLUSH", &c.SessionFlush, &errs)
	parsePositiveDuration(getenv("PANEL_QUERY_TIMEOUT"), "PANEL_QUERY_TIMEOUT", &c.QueryTimeout, &errs)

	return c, errors.Join(errs...)
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
