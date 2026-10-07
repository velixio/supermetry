package enrich

import (
	_ "embed"
	"encoding/json"
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
	Name    string   `json:"name"`
	Medium  string   `json:"medium"`
	Domains []string `json:"domains"`
}

//go:embed lists/sources.json
var sourcesJSON []byte

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
	if utm.Source != "" {
		return utm.Source, utm.Medium
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

	for _, rule := range e.sources {
		for _, domain := range rule.Domains {
			if hostHasSuffix(refHost, domain) {
				return rule.Name, rule.Medium
			}
		}
	}
	return refHost, "referral"
}

func loadSources() ([]sourceRule, error) {
	var sources []sourceRule
	if err := json.Unmarshal(sourcesJSON, &sources); err != nil {
		return nil, err
	}
	for i := range sources {
		for j := range sources[i].Domains {
			sources[i].Domains[j] = normalizeHost(sources[i].Domains[j])
		}
	}
	return sources, nil
}

func normalizeHost(host string) string {
	host = strings.TrimSuffix(strings.ToLower(host), ".")
	return strings.TrimPrefix(host, "www.")
}

func hostHasSuffix(host, suffix string) bool {
	return host == suffix || strings.HasSuffix(host, "."+suffix)
}
