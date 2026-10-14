package platformhttp_test

import (
	"context"
	"encoding/json"
	"net/http"
	"testing"
)

func TestMeMembershipSitesExposeProjectKind(t *testing.T) {
	f := newMembershipHTTPFixture(t)
	var pixelSite int32
	if err := f.data.Pool.QueryRow(context.Background(), `insert into sites(org_id,public_id,name,timezone,retention_class,kind) values($1,'pixelkind001','Pixel project','UTC',30,'pixel') returning id`, f.orgID).Scan(&pixelSite); err != nil {
		t.Fatal(err)
	}
	response := f.request(http.MethodGet, "/api/private/me", "owner", "")
	if response.Code != http.StatusOK {
		t.Fatalf("status=%d body=%s", response.Code, response.Body)
	}
	var me struct {
		Memberships []struct {
			Sites []struct {
				ID   int32  `json:"id"`
				Kind string `json:"kind"`
			} `json:"sites"`
		} `json:"memberships"`
	}
	if err := json.Unmarshal(response.Body.Bytes(), &me); err != nil {
		t.Fatal(err)
	}
	kinds := map[int32]string{}
	for _, membership := range me.Memberships {
		for _, site := range membership.Sites {
			kinds[site.ID] = site.Kind
		}
	}
	if kinds[f.siteID] != "web" || kinds[pixelSite] != "pixel" {
		t.Fatalf("kinds = %v, want site %d web and site %d pixel", kinds, f.siteID, pixelSite)
	}
}
