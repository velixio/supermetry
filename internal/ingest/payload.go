// Package ingest validates and accepts analytics intake requests.
package ingest

import (
	"bytes"
	"encoding/json"
	"errors"
	"math"
	"strconv"
	"strings"
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

var webVitalCaps = map[string]uint64{"lcp": 60000, "cls": 10000, "inp": 60000}

// Payload is the public event intake format. Unknown JSON fields are ignored,
// in particular any client-supplied visitor or session identifiers.
type Payload struct {
	Site            string            `json:"site"`
	Name            string            `json:"name"`
	URL             string            `json:"url"`
	Referrer        string            `json:"referrer"`
	ScreenWidth     int               `json:"screen_width"`
	Props           map[string]string `json:"props"`
	EngagementMS    int               `json:"engagement_ms"`
	ScrollPct       int               `json:"scroll_pct"`
	Revenue         json.RawMessage   `json:"revenue"`
	RevenueMicros   int64             `json:"-"`
	RevenueCurrency string            `json:"-"`
	RevenueInvalid  bool              `json:"-"`
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
	payload.normalizeWebVitals()
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
	payload.normalizeRevenue()
	return payload, nil
}

func (p *Payload) normalizeWebVitals() {
	if p.Name != "Web Vitals" {
		return
	}
	for metric, cap := range webVitalCaps {
		value, ok := p.Props[metric]
		if !ok {
			continue
		}
		if value == "" {
			delete(p.Props, metric)
			continue
		}
		for i := range len(value) {
			if value[i] < '0' || value[i] > '9' {
				delete(p.Props, metric)
				break
			}
		}
		if _, ok := p.Props[metric]; !ok {
			continue
		}
		parsed, err := strconv.ParseUint(value, 10, 64)
		if err != nil || parsed > cap {
			delete(p.Props, metric)
		}
	}
}

func (p *Payload) normalizeRevenue() {
	if len(p.Revenue) == 0 || bytes.Equal(bytes.TrimSpace(p.Revenue), []byte("null")) || p.Name == "pageview" || p.Name == "engagement" {
		p.Revenue = nil
		return
	}
	var revenue struct {
		Amount   json.RawMessage `json:"amount"`
		Currency string          `json:"currency"`
	}
	if err := json.Unmarshal(p.Revenue, &revenue); err != nil {
		p.Revenue = nil
		p.RevenueInvalid = true
		return
	}
	micros, ok := parseRevenueMicros(revenue.Amount)
	if !ok || !validCurrency(revenue.Currency) {
		p.Revenue = nil
		p.RevenueInvalid = true
		return
	}
	p.RevenueMicros = micros
	p.RevenueCurrency = revenue.Currency
}

func parseRevenueMicros(raw json.RawMessage) (int64, bool) {
	raw = bytes.TrimSpace(raw)
	if len(raw) == 0 {
		return 0, false
	}
	text := string(raw)
	if raw[0] == '"' {
		if err := json.Unmarshal(raw, &text); err != nil {
			return 0, false
		}
	}
	mantissa := text
	exponent := 0
	if index := strings.IndexAny(text, "eE"); index >= 0 {
		if strings.IndexAny(text[index+1:], "eE") >= 0 {
			return 0, false
		}
		mantissa = text[:index]
		var err error
		exponent, err = strconv.Atoi(text[index+1:])
		if err != nil || exponent < -100 || exponent > 100 {
			return 0, false
		}
	}
	parts := strings.Split(mantissa, ".")
	if len(parts) > 2 || parts[0] == "" || len(parts) == 2 && (parts[1] == "" || len(parts[1]) > 6) {
		if len(parts) != 2 || exponent == 0 || parts[0] == "" || parts[1] == "" {
			return 0, false
		}
	}
	for _, part := range parts {
		for i := range len(part) {
			if part[i] < '0' || part[i] > '9' {
				return 0, false
			}
		}
	}
	digits := strings.TrimLeft(strings.Join(parts, ""), "0")
	if digits == "" {
		return 0, false
	}
	fractionDigits := 0
	if len(parts) == 2 {
		fractionDigits = len(parts[1])
	}
	scale := 6 + exponent - fractionDigits
	if scale < 0 {
		trim := -scale
		if trim > len(digits) || strings.Trim(digits[len(digits)-trim:], "0") != "" {
			return 0, false
		}
		digits = digits[:len(digits)-trim]
		scale = 0
	}
	if digits == "" || len(digits)+scale > 19 {
		return 0, false
	}
	value, err := strconv.ParseUint(digits, 10, 64)
	if err != nil {
		return 0, false
	}
	for range scale {
		if value > 1_000_000_000_000_000_000/10 {
			return 0, false
		}
		value *= 10
	}
	if value == 0 || value > 1_000_000_000_000_000_000 {
		return 0, false
	}
	micros := value
	return int64(micros), micros > 0
}

func validCurrency(currency string) bool {
	if len(currency) != 3 {
		return false
	}
	for i := range len(currency) {
		if currency[i] < 'A' || currency[i] > 'Z' {
			return false
		}
	}
	return true
}

func validText(value string, maxBytes int) bool {
	return len(value) <= maxBytes && utf8.ValidString(value)
}
