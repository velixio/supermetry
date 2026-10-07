package ingest

import (
	"log/slog"
	"net"
	"net/http"
	"net/netip"
	"strings"
)

// logDiag records how a client address was resolved. It never writes a public
// address: private addresses print as-is, public ones as <public>, and
// anything unparseable as <unparsed>. Enabled with PANEL_INGEST_DIAG=1.
func (h *Handler) logDiag(r *http.Request, ip netip.Addr) {
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
		"country", h.Enricher.Country(ip) != "",
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
	return a.String()
}

// maskHeader keeps private addresses readable and hides everything else.
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
