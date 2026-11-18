package imports

import (
	"context"
	"errors"
	"fmt"
	"strings"
	"time"
)

// Pull imports fetch daily aggregates from another analytics service and feed
// them through the same Aggregate, Summarize, Preview, Store and undo
// (Delete) pipeline as file imports. A pull is split into date-range chunks;
// each stored chunk advances a cursor so an interrupted pull resumes there.

// PullMethod names how a pull reaches the remote service.
type PullMethod string

const (
	MethodPlausibleAPI PullMethod = "plausible_api"
	MethodMatomoAPI    PullMethod = "matomo_api"
	MethodGA4API       PullMethod = "ga4_api"
)

// Pull limits.
const (
	MaxPullDays        = 3660 // about ten years
	MaxPreviewPullDays = 120  // a dry run fetches synchronously, so it is smaller
)

var (
	ErrPullInvalid    = errors.New("invalid pull request")
	ErrPullNotEnabled = errors.New("pull imports are not enabled")
)

// PullRequest describes one pull. Secret is never persisted in config and is
// never serialised.
type PullRequest struct {
	Method PullMethod `json:"method"`
	// BaseURL is the Plausible or Matomo origin; Plausible defaults to
	// https://plausible.io when empty.
	BaseURL string `json:"base_url,omitempty"`
	// RemoteSiteID is the Plausible site_id (the domain) or the Matomo idSite.
	RemoteSiteID string `json:"remote_site_id,omitempty"`
	// Property is the GA4 property resource name, "properties/<id>".
	Property     string `json:"property,omitempty"`
	ConnectionID int64  `json:"connection_id,omitempty"`
	From         string `json:"from"`
	To           string `json:"to"`
	// Secret is the Plausible API key or the Matomo token_auth.
	Secret string `json:"-"`
}

// Source is the import source label a pull is stored under.
func (r PullRequest) Source() Source {
	switch r.Method {
	case MethodPlausibleAPI:
		return SourcePlausible
	case MethodMatomoAPI:
		return SourceMatomo
	default:
		return SourceGA4
	}
}

// Range returns the inclusive civil-day range.
func (r PullRequest) Range() (DayRange, error) {
	from, err := time.Parse(time.DateOnly, r.From)
	if err != nil {
		return DayRange{}, fmt.Errorf("%w: from must be a YYYY-MM-DD date", ErrPullInvalid)
	}
	to, err := time.Parse(time.DateOnly, r.To)
	if err != nil {
		return DayRange{}, fmt.Errorf("%w: to must be a YYYY-MM-DD date", ErrPullInvalid)
	}
	if from.After(to) {
		return DayRange{}, fmt.Errorf("%w: from must not be after to", ErrPullInvalid)
	}
	if days := int(to.Sub(from).Hours()/24) + 1; days > MaxPullDays {
		return DayRange{}, fmt.Errorf("%w: a pull covers at most %d days", ErrPullInvalid, MaxPullDays)
	}
	return DayRange{From: from, To: to}, nil
}

// FileLabel is the human label shown in the import list.
func (r PullRequest) FileLabel() string {
	switch r.Method {
	case MethodPlausibleAPI:
		return "Plausible API: " + truncateLabel(r.RemoteSiteID)
	case MethodMatomoAPI:
		return "Matomo API: site " + truncateLabel(r.RemoteSiteID)
	default:
		return "Google Analytics API: " + truncateLabel(r.Property)
	}
}

func truncateLabel(value string) string {
	if len(value) > 80 {
		return value[:80]
	}
	return value
}

// Progress reports a pull's advancement; days are civil days of the range.
type Progress struct {
	TotalDays int       `json:"total_days"`
	DoneDays  int       `json:"done_days"`
	Rows      int64     `json:"rows"`
	BadRows   int64     `json:"bad_rows"`
	CursorDay *string   `json:"cursor_day"`
	UpdatedAt time.Time `json:"updated_at"`
}

// DayRange is an inclusive range of civil days.
type DayRange struct {
	From, To time.Time
}

// Days counts the civil days in the range.
func (r DayRange) Days() int { return int(r.To.Sub(r.From).Hours()/24) + 1 }

// Contains reports whether day lies inside the range.
func (r DayRange) Contains(day time.Time) bool { return !day.Before(r.From) && !day.After(r.To) }

// ChunkRange splits a range into consecutive chunks of at most size days.
func ChunkRange(r DayRange, size int) []DayRange {
	if size < 1 {
		size = 1
	}
	var chunks []DayRange
	for from := r.From; !from.After(r.To); {
		to := from.AddDate(0, 0, size-1)
		if to.After(r.To) {
			to = r.To
		}
		chunks = append(chunks, DayRange{From: from, To: to})
		from = to.AddDate(0, 0, 1)
	}
	return chunks
}

// Puller fetches the aggregate of one chunk. Implementations must return only
// aggregate values, never visitor identifiers.
type Puller interface {
	ChunkDays() int
	Fetch(ctx context.Context, chunk DayRange) (Aggregate, ParseStats, error)
}

// clip drops aggregate entries outside the chunk, so a misbehaving remote can
// never write days the request did not ask for.
func clip(aggregate Aggregate, chunk DayRange) Aggregate {
	result := newAggregate()
	for day, metrics := range aggregate.Days {
		if chunk.Contains(day) {
			result.Days[day] = metrics
		}
	}
	for key, metrics := range aggregate.Dimensions {
		if chunk.Contains(key.Day) {
			result.Dimensions[key] = metrics
		}
	}
	return result
}

// mergeInto adds src into dst.
func mergeInto(dst, src Aggregate) {
	for day, metrics := range src.Days {
		dst.addDay(day, metrics)
	}
	for key, metrics := range src.Dimensions {
		dst.addDimension(key, metrics)
	}
}

// ratioCount converts a percentage (0..100) of a count into a whole count.
func ratioCount(count int64, percent float64) int64 {
	if percent <= 0 || count <= 0 {
		return 0
	}
	if percent > 100 {
		percent = 100
	}
	return min(count, int64(float64(count)*percent/100+0.5))
}

// totalSeconds converts an average duration in seconds over count visits.
func totalSeconds(count int64, average float64) int64 {
	if average <= 0 || count <= 0 {
		return 0
	}
	return int64(float64(count)*average + 0.5)
}

func normalizeSourceName(name string) string {
	name = strings.TrimSpace(name)
	switch strings.ToLower(name) {
	case "", "(not set)", "(other)":
		return ""
	case "direct / none", "direct", "direct entry", "(direct)":
		return "Direct / None"
	}
	return name
}

// normalizePagePath keeps a page's path and query, adding the leading slash.
func normalizePagePath(value string) string {
	value = strings.TrimSpace(value)
	if value == "" {
		return ""
	}
	if i := strings.Index(value, "://"); i >= 0 {
		rest := value[i+3:]
		if j := strings.IndexAny(rest, "/?"); j >= 0 {
			value = rest[j:]
		} else {
			value = "/"
		}
	}
	if !strings.HasPrefix(value, "/") {
		value = "/" + value
	}
	return value
}
