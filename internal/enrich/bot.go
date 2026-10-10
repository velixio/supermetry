package enrich

import (
	"bufio"
	"bytes"
	_ "embed"
	"encoding/json"
	"fmt"
	"io"
	"net/netip"
	"net/url"
	"os"
	"regexp"
	"strings"

	"supermetry/internal/sites"
)

const MaxReferrerSpamFileBytes = 1 << 20

// BotReason records why traffic was filtered.
type BotReason int8

const (
	BotNone BotReason = iota
	BotUserAgent
	BotHeadless
	BotDatacenter // retired: VPN, proxy and hosting traffic counts as real visitors
	BotSpamReferrer
)

var headlessPattern = regexp.MustCompile(`(?i)(?:HeadlessChrome|PhantomJS|Electron/|puppeteer)`)

//go:embed lists/crawler-user-agents.json
var crawlerUserAgentsJSON []byte

//go:embed lists/referrer-spam.txt
var referrerSpam []byte

type crawlerMatcher struct {
	pattern *regexp.Regexp
	rules   []crawlerRule
}

type crawlerRule struct {
	name    string
	pattern *regexp.Regexp
}

type crawlerEntry struct {
	Pattern string `json:"pattern"`
}

func (m crawlerMatcher) match(ua string) bool {
	return ua == "" || m.pattern.MatchString(ua)
}

// Bot returns the first matching filter reason in precedence order.
func (e *Enricher) Bot(ua string, ip netip.Addr, referrer string) BotReason {
	return e.BotWithBlockedReferrers(ua, ip, referrer, nil)
}

// BotWithBlockedReferrers applies the global and per-site spam-referrer lists.
func (e *Enricher) BotWithBlockedReferrers(ua string, ip netip.Addr, referrer string, blocked []string) BotReason {
	switch {
	case headlessPattern.MatchString(ua):
		return BotHeadless
	case e.crawler.match(ua):
		return BotUserAgent
	case e.isSpamReferrer(referrer, blocked):
		return BotSpamReferrer
	default:
		return BotNone
	}
}

// BotRule returns bounded filter metadata for a matched bot. It never returns
// request-provided user-agent or referrer text.
func (e *Enricher) BotRule(ua, _ string, reason BotReason) string {
	switch reason {
	case BotHeadless:
		return "headless"
	case BotSpamReferrer:
		return "spam_referrer"
	case BotUserAgent:
		for _, rule := range e.crawler.rules {
			if rule.pattern.MatchString(ua) {
				return rule.name
			}
		}
		return "crawler_list"
	default:
		return ""
	}
}

func (e *Enricher) isSpamReferrer(referrer string, blocked []string) bool {
	u, err := url.Parse(referrer)
	if err != nil || u.Hostname() == "" {
		return false
	}
	host := normalizeSpamHost(u.Hostname())
	for {
		if _, ok := e.spam[host]; ok || containsDomain(blocked, host) {
			return true
		}
		dot := strings.IndexByte(host, '.')
		if dot < 0 {
			return false
		}
		host = host[dot+1:]
	}
}

func normalizeSpamHost(host string) string {
	return strings.TrimSuffix(strings.ToLower(host), ".")
}

func containsDomain(domains []string, host string) bool {
	for _, domain := range domains {
		if host == domain {
			return true
		}
	}
	return false
}

// MergeReferrerSpamFile validates and merges an operator-maintained domain list.
// It is intended for startup use before the Enricher is shared by handlers.
func (e *Enricher) MergeReferrerSpamFile(path string) error {
	file, err := os.Open(path)
	if err != nil {
		return fmt.Errorf("open referrer spam file: %w", err)
	}
	defer file.Close()
	info, err := file.Stat()
	if err != nil {
		return fmt.Errorf("stat referrer spam file: %w", err)
	}
	if info.Size() > MaxReferrerSpamFileBytes {
		return fmt.Errorf("referrer spam file exceeds %d bytes", MaxReferrerSpamFileBytes)
	}
	lines := make([]string, 0)
	scanner := bufio.NewScanner(io.LimitReader(file, MaxReferrerSpamFileBytes+1))
	for scanner.Scan() {
		line := strings.TrimSpace(scanner.Text())
		if line == "" || strings.HasPrefix(line, "#") {
			continue
		}
		lines = append(lines, line)
	}
	if err := scanner.Err(); err != nil {
		return fmt.Errorf("read referrer spam file: %w", err)
	}
	normalized, err := sites.NormalizeBlockedReferrers(lines, 0)
	if err != nil {
		return fmt.Errorf("validate referrer spam file: %w", err)
	}
	for _, domain := range normalized {
		e.spam[domain] = struct{}{}
	}
	return nil
}

// crawlerPatternOverrides narrows upstream patterns that also match real
// in-app browsers. WhatsApp's link-preview fetcher sends "WhatsApp/2.x", but
// its in-app browser is a normal Android WebView whose user agent merely ends
// with "WhatsApp/2.x"; an unanchored match dropped those visitors as bots.
var crawlerPatternOverrides = map[string]string{
	"WhatsApp": "^WhatsApp/",
}

func loadBotData() (crawlerMatcher, map[string]struct{}, error) {
	var crawlers []crawlerEntry
	if err := json.Unmarshal(crawlerUserAgentsJSON, &crawlers); err != nil {
		return crawlerMatcher{}, nil, fmt.Errorf("parse crawler list: %w", err)
	}
	patterns := make([]string, 0, len(crawlers))
	rules := make([]crawlerRule, 0, len(crawlers))
	for _, crawler := range crawlers {
		pattern := crawler.Pattern
		if override, ok := crawlerPatternOverrides[pattern]; ok {
			pattern = override
		}
		if pattern != "" {
			patterns = append(patterns, pattern)
			compiled, err := regexp.Compile(`(?i)(?:` + pattern + `)`)
			if err != nil {
				return crawlerMatcher{}, nil, fmt.Errorf("compile crawler rule %q: %w", crawler.Pattern, err)
			}
			rules = append(rules, crawlerRule{name: pattern, pattern: compiled})
		}
	}
	pattern, err := regexp.Compile(`(?i)(?:` + strings.Join(patterns, "|") + `)`)
	if err != nil {
		return crawlerMatcher{}, nil, fmt.Errorf("compile crawler list: %w", err)
	}

	spam := make(map[string]struct{})
	scanner := bufio.NewScanner(bytes.NewReader(referrerSpam))
	for scanner.Scan() {
		domain := normalizeSpamHost(strings.TrimSpace(scanner.Text()))
		if domain != "" && !strings.HasPrefix(domain, "#") {
			spam[domain] = struct{}{}
		}
	}
	if err := scanner.Err(); err != nil {
		return crawlerMatcher{}, nil, fmt.Errorf("parse spam list: %w", err)
	}

	return crawlerMatcher{pattern: pattern, rules: rules}, spam, nil
}
