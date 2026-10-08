// Package clientip resolves the effective client address behind configured
// trusted proxies without retaining or logging it.
package clientip

import (
	"net"
	"net/http"
	"net/netip"
	"strings"
)

// Resolve returns the direct peer unless it belongs to a trusted proxy range.
// For trusted peers it walks X-Forwarded-For from the nearest hop outward and
// returns the first public, non-proxy address.
func Resolve(r *http.Request, trusted []netip.Prefix) (netip.Addr, bool) {
	peer, ok := parseRemoteAddr(r.RemoteAddr)
	if !ok {
		return netip.Addr{}, false
	}
	peer = peer.Unmap()
	if contains(trusted, peer) {
		hops := strings.Split(r.Header.Get("X-Forwarded-For"), ",")
		for index := len(hops) - 1; index >= 0; index-- {
			hop, err := netip.ParseAddr(strings.TrimSpace(hops[index]))
			if err != nil {
				continue
			}
			hop = hop.Unmap()
			if isPublic(hop) && !contains(trusted, hop) {
				return hop, true
			}
		}
	}
	return peer, true
}

// ResolveAccessClient resolves the client address for access-control
// decisions. Unlike Resolve, which skips non-public hops to find a useful
// geolocation address, it treats the nearest untrusted hop of any kind as the
// client, so a private client cannot hide behind a spoofed loopback hop. A
// malformed hop in a trusted chain is rejected rather than skipped.
func ResolveAccessClient(r *http.Request, trusted []netip.Prefix) (netip.Addr, bool) {
	peer, ok := parseRemoteAddr(r.RemoteAddr)
	if !ok {
		return netip.Addr{}, false
	}
	peer = peer.Unmap()
	if !contains(trusted, peer) {
		return peer, true
	}
	client := peer
	values := r.Header.Values("X-Forwarded-For")
	for valueIndex := len(values) - 1; valueIndex >= 0; valueIndex-- {
		hops := strings.Split(values[valueIndex], ",")
		for index := len(hops) - 1; index >= 0; index-- {
			text := strings.TrimSpace(hops[index])
			if text == "" {
				continue
			}
			hop, err := netip.ParseAddr(text)
			if err != nil {
				return netip.Addr{}, false
			}
			hop = hop.Unmap()
			if !contains(trusted, hop) {
				return hop, true
			}
			client = hop
		}
	}
	return client, true
}

func parseRemoteAddr(remote string) (netip.Addr, bool) {
	host, _, err := net.SplitHostPort(remote)
	if err != nil {
		host = remote
	}
	addr, err := netip.ParseAddr(host)
	return addr, err == nil
}

func contains(prefixes []netip.Prefix, addr netip.Addr) bool {
	for _, prefix := range prefixes {
		if prefix.Contains(addr) {
			return true
		}
	}
	return false
}

func isPublic(addr netip.Addr) bool {
	return addr.IsGlobalUnicast() && !addr.IsPrivate() && !addr.IsLoopback() && !addr.IsLinkLocalUnicast()
}
