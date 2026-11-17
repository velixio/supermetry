package sentryingest

import (
	"net"
	"net/url"
	"strings"

	"supermetry/internal/enrich"
)

// Inbound filter reasons; "" means keep the event.
const (
	filterLocalhost = "localhost"
	filterExtension = "browser_extension"
	filterCrawler   = "crawler"
)

var extensionSchemes = []string{"chrome-extension://", "moz-extension://", "safari-extension://", "safari-web-extension://"}

// filterEvent decides from the raw event JSON (before scrubbing drops the
// request host) whether an event is noise: raised on a developer machine,
// raised inside a browser extension, or sent by a crawler. Anything it cannot
// parse is kept; the normaliser rejects bad payloads. The localhost filter
// applies only when localhost is true. httpUA is the user agent of the HTTP
// request; an empty one never counts as a crawler.
func filterEvent(raw []byte, httpUA string, localhost bool) string {
	if enrich.IsCrawlerUA(httpUA) {
		return filterCrawler
	}
	m, err := decodeObject(raw)
	if err != nil {
		return ""
	}
	req := asMap(m["request"])
	if localhost && isLocalURL(str(req["url"])) {
		return filterLocalhost
	}
	if enrich.IsCrawlerUA(requestUserAgent(req["headers"])) {
		return filterCrawler
	}
	ex := m["exception"]
	values := asList(ex)
	if values == nil {
		values = asList(asMap(ex)["values"])
	}
	for _, v := range values {
		for _, f := range asList(asMap(asMap(v)["stacktrace"])["frames"]) {
			fm := asMap(f)
			if isExtensionURL(str(fm["filename"])) || isExtensionURL(str(fm["abs_path"])) {
				return filterExtension
			}
		}
	}
	return ""
}

func isExtensionURL(s string) bool {
	for _, p := range extensionSchemes {
		if strings.HasPrefix(s, p) {
			return true
		}
	}
	return false
}

func isLocalURL(raw string) bool {
	if raw == "" {
		return false
	}
	u, err := url.Parse(raw)
	if err != nil {
		return false
	}
	host := strings.ToLower(u.Hostname())
	if host == "localhost" || strings.HasSuffix(host, ".localhost") {
		return true
	}
	ip := net.ParseIP(host)
	return ip != nil && ip.IsLoopback()
}

// requestUserAgent reads the User-Agent header of an event's request, which
// SDKs send either as an object or as a list of name/value pairs.
func requestUserAgent(headers any) string {
	if m := asMap(headers); m != nil {
		for k, v := range m {
			if strings.EqualFold(k, "user-agent") {
				return str(v)
			}
		}
		return ""
	}
	for _, p := range asList(headers) {
		if pair := asList(p); len(pair) == 2 && strings.EqualFold(str(pair[0]), "user-agent") {
			return str(pair[1])
		}
	}
	return ""
}
