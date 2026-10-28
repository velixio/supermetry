package sitesvc

import (
	"context"
	"errors"
	"strings"
	"testing"
)

func TestNormalizeHostsRequireDomain(t *testing.T) {
	tests := []struct {
		name     string
		hosts    []string
		managed  bool
		existing []string
		want     []string
		wantErr  string
	}{
		{name: "self-hosted single label", hosts: []string{"wiki"}, want: []string{"wiki"}},
		{name: "managed single label", hosts: []string{"afaridar"}, managed: true, wantErr: `host "afaridar" is not a domain: use something like example.com`},
		{name: "managed domain", hosts: []string{"example.com"}, managed: true, want: []string{"example.com"}},
		{name: "managed subdomain co.uk", hosts: []string{"blog.example.co.uk"}, managed: true, want: []string{"blog.example.co.uk"}},
		{name: "managed wildcard domain", hosts: []string{"*.example.com"}, managed: true, want: []string{"*.example.com"}},
		{name: "managed wildcard single label", hosts: []string{"*.intranet"}, managed: true, wantErr: "is not a domain"},
		{name: "managed localhost", hosts: []string{"localhost"}, managed: true, want: []string{"localhost"}},
		{name: "managed IPv4", hosts: []string{"127.0.0.1"}, managed: true, want: []string{"127.0.0.1"}},
		{name: "managed IPv6 stays rejected", hosts: []string{"::1"}, managed: true, wantErr: "invalid host"},
		{name: "managed uppercase and spaces", hosts: []string{"  Example.COM "}, managed: true, want: []string{"example.com"}},
		{name: "managed uppercase single label", hosts: []string{" AfaRidar "}, managed: true, wantErr: "is not a domain"},
		{name: "one bad host fails the list", hosts: []string{"example.com", "wiki"}, managed: true, wantErr: "is not a domain"},
		{name: "existing single label kept", hosts: []string{"wiki", "example.com"}, managed: true, existing: []string{"Wiki"}, want: []string{"wiki", "example.com"}},
	}
	for _, test := range tests {
		t.Run(test.name, func(t *testing.T) {
			got, err := normalizeHosts(test.hosts, test.managed, test.existing)
			if test.wantErr != "" {
				if !errors.Is(err, ErrInvalid) || !strings.Contains(err.Error(), test.wantErr) {
					t.Fatalf("err = %v, want ErrInvalid containing %q", err, test.wantErr)
				}
				return
			}
			if err != nil || strings.Join(got, ",") != strings.Join(test.want, ",") {
				t.Fatalf("got %v, %v; want %v", got, err, test.want)
			}
		})
	}
}

func TestManagedUpdateKeepsStoredSingleLabelHost(t *testing.T) {
	service, _, owner, _ := newSites(t)
	ctx := context.Background()
	site, err := service.Create(ctx, owner, CreateInput{Name: "Old", Hosts: []string{"wiki"}, Timezone: "UTC", RetentionDays: 30, RevenueCurrency: "USD"})
	if err != nil {
		t.Fatal(err)
	}
	service.SetRequireDomain(true)
	if _, err := service.Create(ctx, owner, CreateInput{Name: "New", Hosts: []string{"afaridar"}, Timezone: "UTC", RetentionDays: 30, RevenueCurrency: "USD"}); !errors.Is(err, ErrInvalid) {
		t.Fatalf("managed create err = %v, want ErrInvalid", err)
	}
	name := "Renamed"
	if _, err := service.Update(ctx, owner, site.ID, SitePatch{Name: &name}); err != nil {
		t.Fatalf("rename with stored single-label host: %v", err)
	}
	same := []string{"wiki"}
	if _, err := service.Update(ctx, owner, site.ID, SitePatch{Hosts: &same}); err != nil {
		t.Fatalf("resubmitting unchanged host: %v", err)
	}
	added := []string{"wiki", "other"}
	if _, err := service.Update(ctx, owner, site.ID, SitePatch{Hosts: &added}); !errors.Is(err, ErrInvalid) {
		t.Fatalf("adding new single-label host err = %v, want ErrInvalid", err)
	}
	added = []string{"wiki", "other.example.com"}
	if _, err := service.Update(ctx, owner, site.ID, SitePatch{Hosts: &added}); err != nil {
		t.Fatalf("adding domain: %v", err)
	}
}
