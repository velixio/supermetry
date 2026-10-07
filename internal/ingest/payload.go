// Package ingest validates and accepts analytics intake requests.
package ingest

import (
	"encoding/json"
	"errors"
	"math"
	"unicode/utf8"
)

const (
	maxBodyBytes      = 8 << 10
	maxProps          = 30
	maxKeyBytes       = 300
	maxValueBytes     = 2000
	maxURLBytes       = 2000
	maxSiteBytes      = 300
	maxEventNameBytes = 300
)

// Payload is the public event intake format. Unknown JSON fields are ignored,
// in particular any client-supplied visitor or session identifiers.
type Payload struct {
	Site         string            `json:"site"`
	Name         string            `json:"name"`
	URL          string            `json:"url"`
	Referrer     string            `json:"referrer"`
	ScreenWidth  int               `json:"screen_width"`
	Props        map[string]string `json:"props"`
	EngagementMS int               `json:"engagement_ms"`
	ScrollPct    int               `json:"scroll_pct"`
}

// ParsePayload decodes and bounds an intake payload. It deliberately returns
// non-specific errors so request data never appears in a response or log.
func ParsePayload(body []byte) (Payload, error) {
	var payload Payload
	if len(body) == 0 || len(body) > maxBodyBytes || !utf8.Valid(body) {
		return payload, errors.New("invalid payload")
	}
	if err := json.Unmarshal(body, &payload); err != nil {
		return Payload{}, errors.New("invalid payload")
	}
	if !validText(payload.Site, maxSiteBytes) ||
		!validText(payload.Name, maxEventNameBytes) ||
		!validText(payload.URL, maxURLBytes) ||
		!validText(payload.Referrer, maxValueBytes) {
		return Payload{}, errors.New("invalid payload")
	}
	if len(payload.Props) > maxProps {
		return Payload{}, errors.New("invalid payload")
	}
	for key, value := range payload.Props {
		if key == "" || !validText(key, maxKeyBytes) || !validText(value, maxValueBytes) {
			return Payload{}, errors.New("invalid payload")
		}
	}
	if payload.ScreenWidth < 0 || payload.ScreenWidth > math.MaxInt16 ||
		payload.EngagementMS < 0 || payload.EngagementMS > math.MaxInt32 ||
		payload.ScrollPct < 0 || payload.ScrollPct > 100 {
		return Payload{}, errors.New("invalid payload")
	}
	return payload, nil
}

func validText(value string, maxBytes int) bool {
	return len(value) <= maxBytes && utf8.ValidString(value)
}
