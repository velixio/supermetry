// Package safehttp provides HTTP dialing primitives that reject destinations
// unsuitable for server-side requests, including private and link-local nets.
package safehttp

import (
	"context"
	"errors"
	"net"
	"net/netip"
)

type Resolver interface {
	LookupNetIP(context.Context, string, string) ([]netip.Addr, error)
}

type DialContextFunc func(context.Context, string, string) (net.Conn, error)

// GuardedDial resolves the complete address set before dialing and rejects the
// destination if any answer is blocked. This prevents mixed-answer rebinding.
func GuardedDial(resolver Resolver, dial DialContextFunc, allowPrivate bool) DialContextFunc {
	return func(ctx context.Context, network, address string) (net.Conn, error) {
		host, port, err := net.SplitHostPort(address)
		if err != nil {
			return nil, errors.New("invalid destination address")
		}
		var addresses []netip.Addr
		if literal, parseErr := netip.ParseAddr(host); parseErr == nil {
			addresses = []netip.Addr{literal}
		} else {
			addresses, err = resolver.LookupNetIP(ctx, "ip", host)
			if err != nil || len(addresses) == 0 {
				return nil, errors.New("resolve destination host")
			}
		}
		for _, resolved := range addresses {
			if !allowPrivate && BlockedAddress(resolved) {
				return nil, errors.New("destination resolves to a blocked address")
			}
		}
		for _, resolved := range addresses {
			connection, dialErr := dial(ctx, network, net.JoinHostPort(resolved.String(), port))
			if dialErr == nil {
				return connection, nil
			}
		}
		return nil, errors.New("connect to destination host")
	}
}

var blockedPrefixes = []netip.Prefix{
	netip.MustParsePrefix("0.0.0.0/8"),
	netip.MustParsePrefix("100.64.0.0/10"),
	netip.MustParsePrefix("192.0.0.0/24"),
	netip.MustParsePrefix("198.18.0.0/15"),
	netip.MustParsePrefix("240.0.0.0/4"),
	netip.MustParsePrefix("fec0::/10"),
}

var nat64Prefix = netip.MustParsePrefix("64:ff9b::/96")

// BlockedAddress reports whether an address is private or otherwise unsafe
// for a server-side request target.
func BlockedAddress(address netip.Addr) bool {
	address = address.Unmap()
	if address.Is6() && nat64Prefix.Contains(address) {
		value := address.As16()
		return BlockedAddress(netip.AddrFrom4([4]byte{value[12], value[13], value[14], value[15]}))
	}
	if !address.IsValid() || address.IsPrivate() || address.IsLoopback() || address.IsLinkLocalUnicast() ||
		address.IsLinkLocalMulticast() || address.IsInterfaceLocalMulticast() || address.IsMulticast() || address.IsUnspecified() {
		return true
	}
	for _, prefix := range blockedPrefixes {
		if prefix.Contains(address) {
			return true
		}
	}
	return false
}
