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
	"sort"
	"strings"
)

// BotReason records why traffic was filtered.
type BotReason int8

const (
	BotNone BotReason = iota
	BotUserAgent
	BotHeadless
	BotDatacenter
	BotSpamReferrer
)

var headlessPattern = regexp.MustCompile(`(?i)(?:HeadlessChrome|PhantomJS|Electron/|puppeteer)`)

//go:embed lists/crawler-user-agents.json
var crawlerUserAgentsJSON []byte

//go:embed lists/referrer-spam.txt
var referrerSpam []byte

//go:embed lists/datacenter-cidrs.txt
var datacenterCIDRs []byte

type crawlerMatcher struct {
	pattern *regexp.Regexp
}

type crawlerEntry struct {
	Pattern string `json:"pattern"`
}

func (m crawlerMatcher) match(ua string) bool {
	return ua == "" || m.pattern.MatchString(ua)
}

type prefixSet []netip.Prefix

func (s prefixSet) contains(addr netip.Addr) bool {
	if !addr.IsValid() {
		return false
	}
	addr = addr.Unmap()
	i := sort.Search(len(s), func(i int) bool {
		return s[i].Addr().Compare(addr) > 0
	})
	return i > 0 && s[i-1].Contains(addr)
}

// Bot returns the first matching filter reason in precedence order.
func (e *Enricher) Bot(ua string, ip netip.Addr, referrer string) BotReason {
	switch {
	case headlessPattern.MatchString(ua):
		return BotHeadless
	case e.crawler.match(ua):
		return BotUserAgent
	case e.datacenter.contains(ip):
		return BotDatacenter
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

func loadBotData() (crawlerMatcher, prefixSet, map[string]struct{}, error) {
	var crawlers []crawlerEntry
	if err := json.Unmarshal(crawlerUserAgentsJSON, &crawlers); err != nil {
		return crawlerMatcher{}, nil, nil, fmt.Errorf("parse crawler list: %w", err)
	}
	patterns := make([]string, 0, len(crawlers))
	for _, crawler := range crawlers {
		if crawler.Pattern != "" {
			patterns = append(patterns, crawler.Pattern)
		}
	}
	pattern, err := regexp.Compile(`(?i)(?:` + strings.Join(patterns, "|") + `)`)
	if err != nil {
		return crawlerMatcher{}, nil, nil, fmt.Errorf("compile crawler list: %w", err)
	}

	prefixes, err := parsePrefixes(datacenterCIDRs)
	if err != nil {
		return crawlerMatcher{}, nil, nil, err
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
		return crawlerMatcher{}, nil, nil, fmt.Errorf("parse spam list: %w", err)
	}

	return crawlerMatcher{pattern: pattern}, prefixes, spam, nil
}

func parsePrefixes(data []byte) (prefixSet, error) {
	var prefixes prefixSet
	scanner := bufio.NewScanner(bytes.NewReader(data))
	for scanner.Scan() {
		line := strings.TrimSpace(scanner.Text())
		if line == "" || strings.HasPrefix(line, "#") {
			continue
		}
		prefix, err := netip.ParsePrefix(line)
		if err != nil {
			return nil, fmt.Errorf("parse datacenter prefix: %w", err)
		}
		prefixes = append(prefixes, prefix.Masked())
	}
	if err := scanner.Err(); err != nil {
		return nil, fmt.Errorf("parse datacenter prefixes: %w", err)
	}
	sort.Slice(prefixes, func(i, j int) bool {
		if cmp := prefixes[i].Addr().Compare(prefixes[j].Addr()); cmp != 0 {
			return cmp < 0
		}
		return prefixes[i].Bits() < prefixes[j].Bits()
	})

	// CIDR overlap implies containment. Removing contained ranges makes the
	// greatest start address at or below an IP the only candidate to inspect.
	normalized := prefixes[:0]
	for _, prefix := range prefixes {
		if len(normalized) > 0 && normalized[len(normalized)-1].Contains(prefix.Addr()) {
			continue
		}
		normalized = append(normalized, prefix)
	}
	return normalized, nil
}
