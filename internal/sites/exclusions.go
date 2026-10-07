package sites

import (
	"fmt"
	"net/netip"
	"regexp"
	"strings"
)

// MaxExclusionRules bounds each per-site exclusion list.
const MaxExclusionRules = 50

// Exclusions contains both the normalized rules returned by the API and the
// precompiled values used by ingestion. It is immutable after construction.
type Exclusions struct {
	IPRules      []string
	PathRules    []string
	IPPrefixes   []netip.Prefix
	pathMatchers []*regexp.Regexp
}

// CompileExclusions validates, normalizes, and compiles a site's exclusion
// rules. A bare address is represented as a host prefix.
func CompileExclusions(ipRules, pathRules []string) (Exclusions, error) {
	if len(ipRules) > MaxExclusionRules {
		return Exclusions{}, fmt.Errorf("exclude_ips must contain at most %d entries", MaxExclusionRules)
	}
	if len(pathRules) > MaxExclusionRules {
		return Exclusions{}, fmt.Errorf("exclude_paths must contain at most %d entries", MaxExclusionRules)
	}

	result := Exclusions{
		IPRules:      make([]string, 0, len(ipRules)),
		PathRules:    make([]string, 0, len(pathRules)),
		IPPrefixes:   make([]netip.Prefix, 0, len(ipRules)),
		pathMatchers: make([]*regexp.Regexp, 0, len(pathRules)),
	}
	seenIPs := make(map[string]struct{}, len(ipRules))
	for _, raw := range ipRules {
		rule := strings.TrimSpace(raw)
		prefix, err := parseIPRule(rule)
		if err != nil {
			return Exclusions{}, fmt.Errorf("invalid exclude_ips entry %q", raw)
		}
		normalized := prefix.String()
		if _, exists := seenIPs[normalized]; exists {
			continue
		}
		seenIPs[normalized] = struct{}{}
		result.IPRules = append(result.IPRules, normalized)
		result.IPPrefixes = append(result.IPPrefixes, prefix)
	}

	seenPaths := make(map[string]struct{}, len(pathRules))
	for _, raw := range pathRules {
		rule := strings.TrimSpace(raw)
		matcher, err := compilePathRule(rule)
		if err != nil {
			return Exclusions{}, fmt.Errorf("invalid exclude_paths entry %q", raw)
		}
		if _, exists := seenPaths[rule]; exists {
			continue
		}
		seenPaths[rule] = struct{}{}
		result.PathRules = append(result.PathRules, rule)
		result.pathMatchers = append(result.pathMatchers, matcher)
	}
	return result, nil
}

func parseIPRule(rule string) (netip.Prefix, error) {
	if !strings.Contains(rule, "/") {
		addr, err := netip.ParseAddr(rule)
		if err != nil {
			return netip.Prefix{}, err
		}
		addr = addr.Unmap()
		return netip.PrefixFrom(addr, addr.BitLen()), nil
	}
	prefix, err := netip.ParsePrefix(rule)
	if err != nil {
		return netip.Prefix{}, err
	}
	if prefix.Addr().Is4In6() {
		if prefix.Bits() < 96 {
			return netip.Prefix{}, fmt.Errorf("mapped IPv4 prefix includes non-IPv4 addresses")
		}
		prefix = netip.PrefixFrom(prefix.Addr().Unmap(), prefix.Bits()-96)
	}
	return prefix.Masked(), nil
}

func compilePathRule(rule string) (*regexp.Regexp, error) {
	if !strings.HasPrefix(rule, "/") {
		return nil, fmt.Errorf("path must start with slash")
	}
	var expression strings.Builder
	expression.WriteByte('^')
	for index := 0; index < len(rule); {
		if rule[index] != '*' {
			expression.WriteString(regexp.QuoteMeta(rule[index : index+1]))
			index++
			continue
		}
		run := 1
		for index+run < len(rule) && rule[index+run] == '*' {
			run++
		}
		switch run {
		case 1:
			expression.WriteString(`[^/]*`)
		case 2:
			expression.WriteString(`.*`)
		default:
			return nil, fmt.Errorf("star runs must be * or **")
		}
		index += run
	}
	expression.WriteByte('$')
	return regexp.Compile(expression.String())
}

// ExcludesIP reports whether addr belongs to any configured prefix.
func (e Exclusions) ExcludesIP(addr netip.Addr) bool {
	addr = addr.Unmap()
	for _, prefix := range e.IPPrefixes {
		if prefix.Contains(addr) {
			return true
		}
	}
	return false
}

// ExcludesPath reports whether path matches any configured exact or glob rule.
func (e Exclusions) ExcludesPath(path string) bool {
	if path == "" {
		path = "/"
	}
	for _, matcher := range e.pathMatchers {
		if matcher.MatchString(path) {
			return true
		}
	}
	return false
}

// Clone returns an independent slice view of immutable compiled exclusions.
func (e Exclusions) Clone() Exclusions {
	e.IPRules = append([]string(nil), e.IPRules...)
	e.PathRules = append([]string(nil), e.PathRules...)
	e.IPPrefixes = append([]netip.Prefix(nil), e.IPPrefixes...)
	e.pathMatchers = append([]*regexp.Regexp(nil), e.pathMatchers...)
	return e
}
