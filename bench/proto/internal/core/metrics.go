package core

import "fmt"

// Metrics are the additive per-bucket numbers stored in rollups. Visitors
// are exact per site-local day; because visitor IDs rotate daily they also
// add up across days (memory F1).
type Metrics struct {
	Visitors   int64 `json:"visitors"`
	Visits     int64 `json:"visits"`
	Pageviews  int64 `json:"pageviews"`
	Bounces    int64 `json:"bounces"`
	DurationMs int64 `json:"duration_ms"`
}

// Add accumulates o into m.
func (m *Metrics) Add(o Metrics) {
	m.Visitors += o.Visitors
	m.Visits += o.Visits
	m.Pageviews += o.Pageviews
	m.Bounces += o.Bounces
	m.DurationMs += o.DurationMs
}

// BounceRate is bounces/visits (0 when no visits).
func (m Metrics) BounceRate() float64 {
	if m.Visits == 0 {
		return 0
	}
	return float64(m.Bounces) / float64(m.Visits)
}

// AvgDurationS is the mean visit duration in seconds.
func (m Metrics) AvgDurationS() float64 {
	if m.Visits == 0 {
		return 0
	}
	return float64(m.DurationMs) / 1000 / float64(m.Visits)
}

// DimRow is one value of a breakdown.
type DimRow struct {
	Value string `json:"value"`
	Metrics
}

// Dim is a breakdown dimension. Codes are stored in rollup_day_dim.dim.
type Dim int16

const (
	DimPath Dim = iota + 1
	DimSource
	DimCountry
	DimBrowser
	DimOS
	DimDevice
	DimEntry
	DimExit
)

// AllDims lists every breakdown dimension.
var AllDims = []Dim{DimPath, DimSource, DimCountry, DimBrowser, DimOS, DimDevice, DimEntry, DimExit}

var dimNames = map[Dim]string{
	DimPath: "path", DimSource: "source", DimCountry: "country", DimBrowser: "browser",
	DimOS: "os", DimDevice: "device", DimEntry: "entry", DimExit: "exit",
}

func (d Dim) String() string { return dimNames[d] }

// ParseDim maps a name ("path", "pages", "countries", ...) to a Dim.
func ParseDim(s string) (Dim, error) {
	switch s {
	case "path", "page", "pages":
		return DimPath, nil
	case "source", "sources":
		return DimSource, nil
	case "country", "countries":
		return DimCountry, nil
	case "browser", "browsers":
		return DimBrowser, nil
	case "os":
		return DimOS, nil
	case "device", "devices":
		return DimDevice, nil
	case "entry", "entry_page", "entry_pages":
		return DimEntry, nil
	case "exit", "exit_page", "exit_pages":
		return DimExit, nil
	}
	return 0, fmt.Errorf("unknown dimension %q", s)
}

// SessionValue returns the session's value for a session-level dimension.
func (s *Session) SessionValue(d Dim) string {
	switch d {
	case DimSource:
		return s.Source()
	case DimCountry:
		return s.Country()
	case DimBrowser:
		return s.Browser()
	case DimOS:
		return s.OS()
	case DimDevice:
		return s.Device()
	case DimEntry:
		return s.Entry()
	case DimExit:
		return s.Exit()
	}
	panic("not a session dimension: " + d.String())
}

// Filter is an equality filter, e.g. country=IN or path=/pricing.
type Filter struct {
	Key   string `json:"key"`
	Value string `json:"value"`
}

// FunnelSteps is funnel F from the CONTRACT: "/" → "/pricing" → signup.
var FunnelSteps = []FunnelStep{
	{Name: "pageview", Path: "/"},
	{Name: "pageview", Path: "/pricing"},
	{Name: "signup"},
}

// FunnelStep matches an event by name and (for pageviews) path.
type FunnelStep struct {
	Name string `json:"name"`
	Path string `json:"path,omitempty"`
}

// RollupRow is one rollup row; Dim 0 is the day total.
type RollupRow struct {
	Site  int32
	Day   Day
	Dim   Dim
	Value string
	Metrics
}
