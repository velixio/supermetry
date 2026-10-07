package ingest

import (
	"log/slog"
	"net"
	"net/http"
	"net/netip"
	"strings"

	"panel/internal/enrich"
)

// logDiag records how a client address was resolved without writing any
// address value. Enabled with PANEL_INGEST_DIAG=1.
func (h *Handler) logDiag(r *http.Request, ip netip.Addr, location enrich.CityInfo) {
	peer, _ := parseRemoteAddr(r.RemoteAddr)
	peer = peer.Unmap()
	slog.Info("ingest ip diag",
		"class", ipClass(ip),
		"peer", maskAddr(peer),
		"peer_trusted", h.proxyTrusted(peer),
		"xff", maskHeader(r.Header.Get("X-Forwarded-For")),
		"x_real_ip", maskHeader(r.Header.Get("X-Real-IP")),
		"cf", maskHeader(r.Header.Get("CF-Connecting-IP")),
		"forwarded", maskHeader(r.Header.Get("Forwarded")),
		"country", countryOrDash(location.Country),
	)
}

func ipClass(a netip.Addr) string {
	switch {
	case a.IsLoopback():
		return "loopback"
	case a.IsPrivate():
		return "private"
	case a.IsLinkLocalUnicast():
		return "linklocal"
	case isPublicIP(a):
		return "public"
	}
	return "other"
}

func maskAddr(a netip.Addr) string {
	if !a.IsValid() {
		return "<unparsed>"
	}
	if isPublicIP(a) {
		return "<public>"
	}
	return "<private>"
}

// maskHeader retains only address classes from forwarding headers.
func maskHeader(v string) string {
	if v == "" {
		return "-"
	}
	var out []string
	for _, el := range strings.FieldsFunc(v, func(r rune) bool { return r == ',' || r == ';' }) {
		el = strings.TrimSpace(el)
		if k, val, ok := strings.Cut(el, "="); ok {
			if !strings.EqualFold(k, "for") {
				continue // proto=, by=, host= carry no client address
			}
			el = val
		}
		a, ok := parseHop(el)
		if !ok {
			out = append(out, "<unparsed>")
			continue
		}
		out = append(out, maskAddr(a))
	}
	if len(out) == 0 {
		return "-"
	}
	return strings.Join(out, ",")
}

// parseHop parses an address token that may be quoted, bracketed or carry a port.
func parseHop(tok string) (netip.Addr, bool) {
	tok = strings.Trim(strings.TrimSpace(tok), `"`)
	if ap, err := netip.ParseAddrPort(tok); err == nil {
		return ap.Addr().Unmap(), true
	}
	tok = strings.Trim(tok, "[]")
	if a, err := netip.ParseAddr(tok); err == nil {
		return a.Unmap(), true
	}
	if host, _, err := net.SplitHostPort(tok); err == nil {
		if a, err := netip.ParseAddr(strings.Trim(host, "[]")); err == nil {
			return a.Unmap(), true
		}
	}
	return netip.Addr{}, false
}

func countryOrDash(c string) string {
	if c == "" {
		return "-"
	}
	return c
}

// logBotDrop explains why an event was accepted but discarded as a bot. The
// network organisation (never the address) shows which list matched.
func (h *Handler) logBotDrop(reason enrich.BotReason, ip netip.Addr, location enrich.CityInfo) {
	names := map[enrich.BotReason]string{
		enrich.BotUserAgent:    "crawler_user_agent",
		enrich.BotHeadless:     "headless_browser",
		enrich.BotSpamReferrer: "spam_referrer",
	}
	slog.Info("ingest dropped as bot",
		"reason", names[reason],
		"country", countryOrDash(location.Country),
		"asn_org", h.Enricher.ASN(ip).Organization,
	)
}
