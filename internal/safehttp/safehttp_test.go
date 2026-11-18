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

func TestBlockedAddressCoversMetadataAndMappedForms(t *testing.T) {
	for _, addr := range []string{
		"169.254.169.254", "100.100.100.200", "fd00:ec2::254", "::ffff:127.0.0.1", "::ffff:10.0.0.1",
		"::1", "0.0.0.0", "::", "224.0.0.1", "255.255.255.255", "172.16.0.1", "192.168.1.1", "fe80::1", "fc00::1",
	} {
		if !BlockedAddress(netip.MustParseAddr(addr)) {
			t.Errorf("BlockedAddress(%s) = false, want true", addr)
		}
	}
	for _, addr := range []string{"93.184.216.34", "2606:4700:4700::1111"} {
		if BlockedAddress(netip.MustParseAddr(addr)) {
			t.Errorf("BlockedAddress(%s) = true, want false", addr)
		}
	}
}

// A resolver that answers public first and private later models DNS
// rebinding: the guard must check the answer it dials, on every dial.
func TestGuardedDialChecksEveryDialAgainstFreshAnswers(t *testing.T) {
	calls := 0
	resolver := resolverFunc(func(context.Context, string, string) ([]netip.Addr, error) {
		calls++
		if calls == 1 {
			return []netip.Addr{netip.MustParseAddr("93.184.216.34")}, nil
		}
		return []netip.Addr{netip.MustParseAddr("93.184.216.34"), netip.MustParseAddr("169.254.169.254")}, nil
	})
	var dialed []string
	dial := GuardedDial(resolver, func(_ context.Context, _, address string) (net.Conn, error) {
		dialed = append(dialed, address)
		return nil, errors.New("stop")
	}, false)
	_, _ = dial(context.Background(), "tcp", "rebind.test:80")
	if len(dialed) != 1 || dialed[0] != "93.184.216.34:80" {
		t.Fatalf("first dial = %v, want the public address", dialed)
	}
	_, _ = dial(context.Background(), "tcp", "rebind.test:80")
	if len(dialed) != 1 {
		t.Fatalf("second dial reached the network after the answer turned private: %v", dialed)
	}
}

type resolverFunc func(context.Context, string, string) ([]netip.Addr, error)

func (f resolverFunc) LookupNetIP(ctx context.Context, network, host string) ([]netip.Addr, error) {
	return f(ctx, network, host)
}
