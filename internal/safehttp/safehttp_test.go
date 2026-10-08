package safehttp

import (
	"context"
	"errors"
	"net"
	"net/netip"
	"testing"
)

func TestGuardedDialRejectsBlockedAddressesBeforeDial(t *testing.T) {
	tests := []struct {
		name string
		addr string
	}{
		{name: "private", addr: "10.0.0.1"},
		{name: "loopback", addr: "127.0.0.1"},
		{name: "link local", addr: "169.254.1.1"},
	}
	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			dialed := false
			dial := GuardedDial(fakeResolver{addresses: []netip.Addr{netip.MustParseAddr(tt.addr)}}, func(context.Context, string, string) (net.Conn, error) {
				dialed = true
				return nil, errors.New("unexpected dial")
			}, false)

			if _, err := dial(context.Background(), "tcp", "example.test:443"); err == nil || dialed {
				t.Fatalf("GuardedDial() error=%v dialed=%t, want rejection before dial", err, dialed)
			}
		})
	}
}

func TestBlockedAddressRejectsIPv6TransitionRanges(t *testing.T) {
	tests := []struct {
		name string
		addr string
	}{
		{name: "IPv4 compatible", addr: "::7f00:1"},
		{name: "6to4", addr: "2002:7f00:1::1"},
		{name: "Teredo", addr: "2001:0:4136:e378:8000:63bf:3fff:fdd2"},
		{name: "local use NAT64", addr: "64:ff9b:1::1"},
		{name: "well known NAT64", addr: "64:ff9b::808:808"},
	}
	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			if address := netip.MustParseAddr(tt.addr); !BlockedAddress(address) {
				t.Fatalf("BlockedAddress(%s) = false, want true", address)
			}
		})
	}
}

type fakeResolver struct {
	addresses []netip.Addr
}

func (r fakeResolver) LookupNetIP(context.Context, string, string) ([]netip.Addr, error) {
	return append([]netip.Addr(nil), r.addresses...), nil
}
