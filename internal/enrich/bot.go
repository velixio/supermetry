package enrich

import (
	"bufio"
	"bytes"
	_ "embed"
	"encoding/json"
	"fmt"
	"net/netip"
	"net/url"
	"regexp"
	"strings"
)

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
}

type crawlerEntry struct {
	Pattern string `json:"pattern"`
}

func (m crawlerMatcher) match(ua string) bool {
	return ua == "" || m.pattern.MatchString(ua)
}

// Bot returns the first matching filter reason in precedence order.
func (e *Enricher) Bot(ua string, ip netip.Addr, referrer string) BotReason {
	switch {
	case headlessPattern.MatchString(ua):
		return BotHeadless
	case e.crawler.match(ua):
		return BotUserAgent
	case e.isSpamReferrer(referrer):
		return BotSpamReferrer
	default:
		return BotNone
	}
}

func (e *Enricher) isSpamReferrer(referrer string) bool {
	u, err := url.Parse(referrer)
	if err != nil || u.Hostname() == "" {
		return false
	}
	host := normalizeHost(u.Hostname())
	for {
		if _, ok := e.spam[host]; ok {
			return true
		}
		dot := strings.IndexByte(host, '.')
		if dot < 0 {
			return false
		}
		host = host[dot+1:]
	}
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
	for _, crawler := range crawlers {
		pattern := crawler.Pattern
		if override, ok := crawlerPatternOverrides[pattern]; ok {
			pattern = override
		}
		if pattern != "" {
			patterns = append(patterns, pattern)
		}
	}
	pattern, err := regexp.Compile(`(?i)(?:` + strings.Join(patterns, "|") + `)`)
	if err != nil {
		return crawlerMatcher{}, nil, fmt.Errorf("compile crawler list: %w", err)
	}

	spam := make(map[string]struct{})
	scanner := bufio.NewScanner(bytes.NewReader(referrerSpam))
	for scanner.Scan() {
		domain := normalizeHost(strings.TrimSpace(scanner.Text()))
		if domain != "" && !strings.HasPrefix(domain, "#") {
			spam[domain] = struct{}{}
		}
	}
	if err := scanner.Err(); err != nil {
		return crawlerMatcher{}, nil, fmt.Errorf("parse spam list: %w", err)
	}

	return crawlerMatcher{pattern: pattern}, spam, nil
}
