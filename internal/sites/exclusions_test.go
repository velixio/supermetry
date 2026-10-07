package sites

import (
	"net/netip"
	"strings"
	"testing"
)

func TestCompileExclusionsNormalizesAndMatchesIPs(t *testing.T) {
	t.Parallel()

	exclusions, err := CompileExclusions(
		[]string{"203.0.113.7", "2001:db8::/32", "198.51.100.9/24"},
		nil,
	)
	if err != nil {
		t.Fatal(err)
	}
	wantRules := []string{"203.0.113.7/32", "2001:db8::/32", "198.51.100.0/24"}
	if strings.Join(exclusions.IPRules, ",") != strings.Join(wantRules, ",") {
		t.Fatalf("IPRules = %v, want %v", exclusions.IPRules, wantRules)
	}

	tests := []struct {
		name string
		ip   string
		want bool
	}{
		{name: "single IPv4", ip: "203.0.113.7", want: true},
		{name: "outside single IPv4", ip: "203.0.113.8", want: false},
		{name: "IPv4 range", ip: "198.51.100.44", want: true},
		{name: "IPv6 range", ip: "2001:db8:1::1", want: true},
		{name: "outside IPv6 range", ip: "2001:db9::1", want: false},
		{name: "mapped IPv4 client", ip: "::ffff:203.0.113.7", want: true},
	}
	for _, test := range tests {
		t.Run(test.name, func(t *testing.T) {
			t.Parallel()
			if got := exclusions.ExcludesIP(netip.MustParseAddr(test.ip)); got != test.want {
				t.Fatalf("ExcludesIP(%q) = %t, want %t", test.ip, got, test.want)
			}
		})
	}
}

func TestCompileExclusionsMatchesPaths(t *testing.T) {
	t.Parallel()

	exclusions, err := CompileExclusions(nil, []string{"/admin/*", "/preview/**", "/internal"})
	if err != nil {
		t.Fatal(err)
	}
	tests := []struct {
		name string
		path string
		want bool
	}{
		{name: "single segment", path: "/admin/users", want: true},
		{name: "single segment empty", path: "/admin/", want: true},
		{name: "star does not cross segment", path: "/admin/users/7", want: false},
		{name: "double star crosses segments", path: "/preview/releases/7", want: true},
		{name: "double star may be empty", path: "/preview/", want: true},
		{name: "exact", path: "/internal", want: true},
		{name: "exact is not prefix", path: "/internal/tools", want: false},
		{name: "similar path", path: "/administrator", want: false},
	}
	for _, test := range tests {
		t.Run(test.name, func(t *testing.T) {
			t.Parallel()
			if got := exclusions.ExcludesPath(test.path); got != test.want {
				t.Fatalf("ExcludesPath(%q) = %t, want %t", test.path, got, test.want)
			}
		})
	}
}

func TestCompileExclusionsRejectsInvalidRules(t *testing.T) {
	t.Parallel()

	tooMany := make([]string, MaxExclusionRules+1)
	for i := range tooMany {
		tooMany[i] = "/internal"
	}
	tests := []struct {
		name  string
		ips   []string
		paths []string
		entry string
	}{
		{name: "invalid IP", ips: []string{"not-an-ip"}, entry: "not-an-ip"},
		{name: "path without slash", paths: []string{"admin/*"}, entry: "admin/*"},
		{name: "unsupported star run", paths: []string{"/admin/***"}, entry: "/admin/***"},
		{name: "too many IPs", ips: make([]string, MaxExclusionRules+1), entry: "exclude_ips"},
		{name: "too many paths", paths: tooMany, entry: "exclude_paths"},
	}
	for _, test := range tests {
		t.Run(test.name, func(t *testing.T) {
			t.Parallel()
			_, err := CompileExclusions(test.ips, test.paths)
			if err == nil || !strings.Contains(err.Error(), test.entry) {
				t.Fatalf("CompileExclusions() error = %v, want entry %q", err, test.entry)
			}
		})
	}
}
