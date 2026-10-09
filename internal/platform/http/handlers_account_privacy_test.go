package platformhttp_test

import (
	"context"
	"encoding/json"
	"net/http"
	"reflect"
	"testing"

	platformhttp "supermetry/internal/platform/http"
	"supermetry/internal/platform/orgs"
	"supermetry/internal/privacy"
)

func TestMeMembershipSitesExposeConfiguredAndEffectiveTrackingPolicy(t *testing.T) {
	for _, mode := range []privacy.Mode{privacy.Anonymous, privacy.Identified} {
		t.Run(mode.String(), func(t *testing.T) {
			f := newMembershipHTTPFixture(t)
			ctx := context.Background()
			var enabledSite int32
			if err := f.data.Pool.QueryRow(ctx, `insert into sites(org_id,public_id,name,timezone,retention_class,settings) values($1,'profilemode1','Profiles enabled','UTC',30,'{"profiles_enabled":true}') returning id`, f.orgID).Scan(&enabledSite); err != nil {
				t.Fatal(err)
			}
			// The original fixture site has no profiles_enabled key: absent means false.
			if _, err := f.data.Pool.Exec(ctx, `insert into sites(org_id,public_id,name,timezone,retention_class,settings,deleted_at) values
   ($1,'hiddenmode01','Out of scope','UTC',30,'{"profiles_enabled":true}',null),
   ($1,'deletedmode1','Deleted site','UTC',30,'{"profiles_enabled":true}',now())`, f.orgID); err != nil {
				t.Fatal(err)
			}
			if _, err := f.data.Pool.Exec(ctx, `insert into orgs(name,slug) values('Foreign organization','foreign-privacy');
   insert into sites(org_id,public_id,name,timezone,retention_class,settings)
   select id,'foreignmode1','Foreign site','UTC',30,'{"profiles_enabled":true}'::jsonb from orgs where slug='foreign-privacy'`); err != nil {
				t.Fatal(err)
			}
			if _, err := f.data.Pool.Exec(ctx, `update memberships set site_ids=$1 where org_id=$2 and user_id=$3`, []int32{f.siteID, enabledSite}, f.orgID, f.users["viewer"]); err != nil {
				t.Fatal(err)
			}
			f.handler = platformhttp.New(platformhttp.Deps{Store: f.data, Orgs: orgs.New(f.data), Authenticator: f.authenticator, TrackingMode: mode})
			response := f.request(http.MethodGet, "/api/private/me", "viewer", "")
			if response.Code != http.StatusOK {
				t.Fatalf("status=%d body=%s", response.Code, response.Body)
			}
			type policyResponse struct {
				InstanceMode              string   `json:"instance_mode"`
				ConfiguredProfilesEnabled bool     `json:"configured_profiles_enabled"`
				EffectiveMode             string   `json:"effective_mode"`
				Restrictions              []string `json:"restrictions"`
			}
			var me struct {
				Memberships []struct {
					Sites []struct {
						ID              int32          `json:"id"`
						OrgID           int64          `json:"org_id"`
						ProfilesEnabled bool           `json:"profiles_enabled"`
						TrackingPolicy  policyResponse `json:"tracking_policy"`
					} `json:"sites"`
				} `json:"memberships"`
			}
			if err := json.Unmarshal(response.Body.Bytes(), &me); err != nil {
				t.Fatal(err)
			}
			if len(me.Memberships) != 1 || len(me.Memberships[0].Sites) != 2 {
				t.Fatalf("unauthorized membership/sites exposed: %s", response.Body)
			}
			seen := map[int32]bool{}
			for _, site := range me.Memberships[0].Sites {
				configured := site.ID == enabledSite
				if (site.ID != f.siteID && !configured) || site.OrgID != f.orgID || seen[site.ID] {
					t.Fatalf("unauthorized/duplicate site=%+v", site)
				}
				seen[site.ID] = true
				effective := "anonymous"
				restrictions := []string{}
				if mode == privacy.Anonymous {
					restrictions = append(restrictions, "instance_anonymous")
				}
				if !configured {
					restrictions = append(restrictions, "project_profiles_disabled")
				}
				if mode == privacy.Identified && configured {
					effective = "identified"
				}
				want := policyResponse{InstanceMode: mode.String(), ConfiguredProfilesEnabled: configured, EffectiveMode: effective, Restrictions: restrictions}
				if site.ProfilesEnabled != configured || !reflect.DeepEqual(site.TrackingPolicy, want) {
					t.Errorf("site=%d profiles=%v policy=%+v; want profiles=%v policy=%+v", site.ID, site.ProfilesEnabled, site.TrackingPolicy, configured, want)
				}
			}
			if _, err := f.data.Pool.Exec(ctx, `update memberships set site_ids='{}' where org_id=$1 and user_id=$2`, f.orgID, f.users["viewer"]); err != nil {
				t.Fatal(err)
			}
			response = f.request(http.MethodGet, "/api/private/me", "viewer", "")
			if response.Code != http.StatusOK {
				t.Fatalf("empty-scope status=%d body=%s", response.Code, response.Body)
			}
			if err := json.Unmarshal(response.Body.Bytes(), &me); err != nil {
				t.Fatal(err)
			}
			if len(me.Memberships) != 1 || len(me.Memberships[0].Sites) != 0 {
				t.Fatalf("empty scope leaked sites: %s", response.Body)
			}
		})
	}
}
