package enrich

import (
	_ "embed"
	"encoding/json"
	"fmt"
	"net/url"
	"strings"
)

// UTM contains campaign attribution parsed from a page URL.
type UTM struct {
	Source   string
	Medium   string
	Campaign string
	Content  string
	Term     string
}

type sourceRule struct {
	Name     string   `json:"name"`
	Medium   string   `json:"medium"`
	Category string   `json:"category"`
	Domains  []string `json:"domains"`
	Aliases  []string `json:"aliases"`
}

type mediumRule struct {
	Name    string   `json:"name"`
	Aliases []string `json:"aliases"`
}

type sourceData struct {
	Sources []sourceRule `json:"sources"`
	Mediums []mediumRule `json:"mediums"`
}

// SourceClassifier derives canonical traffic attribution from UTM values and
// referrer hosts.
type SourceClassifier struct {
	sources []sourceRule
	aliases map[string]sourceRule
	mediums map[string]string
}

//go:embed lists/sources.json
var sourcesJSON []byte

// NewSourceClassifier loads the embedded source and medium mappings.
func NewSourceClassifier() (*SourceClassifier, error) {
	data, err := loadSourceData()
	if err != nil {
		return nil, err
	}
	classifier := &SourceClassifier{
		sources: data.Sources,
		aliases: make(map[string]sourceRule),
		mediums: make(map[string]string),
	}
	for _, rule := range data.Sources {
		for _, alias := range append([]string{rule.Name}, rule.Aliases...) {
			key := normalizeToken(alias)
			if existing, ok := classifier.aliases[key]; ok && existing.Name != rule.Name {
				return nil, fmt.Errorf("duplicate source alias %q", alias)
			}
			classifier.aliases[key] = rule
		}
	}
	for _, rule := range data.Mediums {
		for _, alias := range append([]string{rule.Name}, rule.Aliases...) {
			key := normalizeToken(alias)
			if existing, ok := classifier.mediums[key]; ok && existing != rule.Name {
				return nil, fmt.Errorf("duplicate medium alias %q", alias)
			}
			classifier.mediums[key] = rule.Name
		}
	}
	return classifier, nil
}

// ParseUTM extracts UTM parameters from a page URL. Invalid URLs produce an
// empty value.
func ParseUTM(pageURL string) UTM {
	u, err := url.Parse(pageURL)
	if err != nil {
		return UTM{}
	}
	query := u.Query()
	return UTM{
		Source:   query.Get("utm_source"),
		Medium:   query.Get("utm_medium"),
		Campaign: query.Get("utm_campaign"),
		Content:  query.Get("utm_content"),
		Term:     query.Get("utm_term"),
	}
}

// Source derives source and medium from campaign parameters or the referrer.
// UTM attribution wins; internal and malformed referrers are direct traffic.
func (e *Enricher) Source(referrer, pageURL string, utm UTM) (source, medium string) {
	return e.source.Source(referrer, pageURL, utm)
}

// Source derives source and medium from campaign parameters or the referrer.
// UTM attribution wins; internal and malformed referrers are direct traffic.
func (c *SourceClassifier) Source(referrer, pageURL string, utm UTM) (source, medium string) {
	if utm.Source != "" {
		canonical, defaultMedium, known := c.CanonicalSource(utm.Source)
		source = utm.Source
		if known {
			source = canonical
		}
		if utm.Medium != "" {
			if canonical, ok := c.mediums[normalizeToken(utm.Medium)]; ok {
				return source, canonical
			}
			return source, utm.Medium
		}
		if known {
			return source, defaultMedium
		}
		return source, ""
	}

	ref, err := url.Parse(referrer)
	if err != nil || ref.Hostname() == "" {
		return "", ""
	}
	page, err := url.Parse(pageURL)
	if err != nil {
		return "", ""
	}
	refHost := normalizeHost(ref.Hostname())
	if refHost == normalizeHost(page.Hostname()) {
		return "", ""
	}

	if source, medium, known := c.CanonicalSource(refHost); known {
		return source, medium
	}
	return refHost, "referral"
}

// CanonicalSource maps a configured UTM alias or referrer host to its display
// name and default medium.
func (c *SourceClassifier) CanonicalSource(value string) (source, medium string, known bool) {
	if rule, ok := c.aliases[normalizeToken(value)]; ok {
		return rule.Name, rule.Medium, true
	}
	host := normalizeHost(value)
	for _, rule := range c.sources {
		for _, domain := range rule.Domains {
			if hostMatches(host, domain) {
				return rule.Name, rule.Medium, true
			}
		}
	}
	return value, "", false
}

func loadSourceData() (sourceData, error) {
	var data sourceData
	if err := json.Unmarshal(sourcesJSON, &data); err != nil {
		return sourceData{}, err
	}
	for i := range data.Sources {
		if data.Sources[i].Name == "" || data.Sources[i].Medium == "" {
			return sourceData{}, fmt.Errorf("source rule %d is incomplete", i)
		}
		switch data.Sources[i].Category {
		case "", "search", "social", "ai":
		default:
			return sourceData{}, fmt.Errorf("source %q has unknown category %q", data.Sources[i].Name, data.Sources[i].Category)
		}
		for j := range data.Sources[i].Domains {
			data.Sources[i].Domains[j] = normalizeHost(data.Sources[i].Domains[j])
		}
	}
	return data, nil
}

func normalizeHost(host string) string {
	host = strings.TrimSuffix(strings.ToLower(host), ".")
	return strings.TrimPrefix(host, "www.")
}

func normalizeToken(value string) string {
	return strings.ToLower(strings.TrimSpace(value))
}

func hostMatches(host, pattern string) bool {
	if strings.HasSuffix(pattern, ".*") {
		name := strings.TrimSuffix(pattern, ".*")
		return strings.HasPrefix(host, name+".") || strings.Contains(host, "."+name+".")
	}
	return hostHasSuffix(host, pattern)
}

func hostHasSuffix(host, suffix string) bool {
	return host == suffix || strings.HasSuffix(host, "."+suffix)
}
