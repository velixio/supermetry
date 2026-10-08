package api

import "time"

// EventExploreRequest is the query-engine contract behind the events endpoint.
type EventExploreRequest struct {
	SiteID  int32
	From    time.Time
	To      time.Time
	Name    string
	Filters []Filter
	Cursor  *string
	Limit   int
}

// PropertyExploreRequest is the query-engine contract behind property-key exploration.
type PropertyExploreRequest struct {
	SiteID int32
	From   time.Time
	To     time.Time
	Event  string
}

// PropertyValueExploreRequest is the query-engine contract behind property-value exploration.
type PropertyValueExploreRequest struct {
	SiteID int32
	Key    string
	From   time.Time
	To     time.Time
	Event  string
}

// SuggestionExploreRequest is the query-engine contract behind filter suggestions.
type SuggestionExploreRequest struct {
	SiteID    int32
	Dimension string
	Query     string
	From      time.Time
	To        time.Time
}
