package sitesvc

import (
	"math"
	"time"
)

// Thresholds for the project status line. Each is tested at its boundary in
// highlights_test.go; change them here only.
const (
	maxHighlights = 3

	highlightQuietAfter           = 24 * time.Hour
	highlightCrashFreeDropPoints  = 1.0
	highlightChangeMinPercent     = 10.0
	highlightChangeMinPrevious    = 20
	highlightTopSourceMinShare    = 0.25
	highlightTopSourceMinVisitors = 20
	highlightKindApp              = "app"
	HighlightQuiet                = "quiet"
	HighlightNewIssues            = "new_issues"
	HighlightCrashFreeDrop        = "crash_free_drop"
	HighlightVisitorsChange       = "visitors_change"
	HighlightTopSource            = "top_source"
	HighlightSteady               = "steady"
)

// Highlight is one typed fact about a project. The server never sends prose,
// so the client can translate it. Only the fields of the kind are set.
type Highlight struct {
	Kind    string   `json:"kind"`
	Percent *float64 `json:"percent,omitempty"`
	Name    string   `json:"name,omitempty"`
	Share   *float64 `json:"share,omitempty"`
	Count   *int64   `json:"count,omitempty"`
	Hours   *int64   `json:"hours,omitempty"`
	Points  *float64 `json:"points,omitempty"`
}

type highlightInput struct {
	Kind              string
	Now               time.Time
	LastEventAt       *time.Time
	Visitors          int64
	Previous          int64
	NewIssues         int64
	CrashFreeCurrent  *float64 // fraction 0..1; nil when unknown
	CrashFreePrevious *float64
	TopSource         string
	TopSourceVisitors int64
	SourceTotal       int64
}

// buildHighlights returns the first maxHighlights applicable facts in priority
// order. It is a pure function of already computed figures.
func buildHighlights(in highlightInput) []Highlight {
	out := make([]Highlight, 0, maxHighlights)
	app := in.Kind == highlightKindApp
	if in.LastEventAt != nil && in.Previous > 0 && in.Now.Sub(*in.LastEventAt) >= highlightQuietAfter {
		hours := int64(in.Now.Sub(*in.LastEventAt) / time.Hour)
		out = append(out, Highlight{Kind: HighlightQuiet, Hours: &hours})
	}
	if app && in.NewIssues > 0 {
		count := in.NewIssues
		out = append(out, Highlight{Kind: HighlightNewIssues, Count: &count})
	}
	if app && in.CrashFreeCurrent != nil && in.CrashFreePrevious != nil {
		// Round to micro-points so 0.995 to 0.985 counts as exactly one point.
		drop := math.Round((*in.CrashFreePrevious-*in.CrashFreeCurrent)*100*1e6) / 1e6
		if drop >= highlightCrashFreeDropPoints {
			out = append(out, Highlight{Kind: HighlightCrashFreeDrop, Points: &drop})
		}
	}
	if in.Previous >= highlightChangeMinPrevious {
		change := math.Round(float64(in.Visitors-in.Previous)/float64(in.Previous)*100*1e6) / 1e6
		if math.Abs(change) >= highlightChangeMinPercent {
			percent := math.Round(change*10) / 10
			out = append(out, Highlight{Kind: HighlightVisitorsChange, Percent: &percent})
		}
	}
	if in.TopSource != "" && in.SourceTotal > 0 && in.TopSourceVisitors >= highlightTopSourceMinVisitors {
		share := float64(in.TopSourceVisitors) / float64(in.SourceTotal)
		if share >= highlightTopSourceMinShare {
			share = math.Min(1, math.Round(share*1000)/1000)
			out = append(out, Highlight{Kind: HighlightTopSource, Name: in.TopSource, Share: &share})
		}
	}
	if len(out) == 0 && in.Visitors > 0 {
		out = append(out, Highlight{Kind: HighlightSteady})
	}
	if len(out) > maxHighlights {
		out = out[:maxHighlights]
	}
	return out
}
