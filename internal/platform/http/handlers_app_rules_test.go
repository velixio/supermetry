package platformhttp_test

import (
	"fmt"
	"net/http"
	"strings"
	"testing"
)

func (f issuesFixture) rulesPath() string {
	return fmt.Sprintf("/api/private/sites/%d/app/rules", f.siteID)
}

func TestAppRulesRoundTripReachesRegistry(t *testing.T) {
	f := newAppIssuesFixture(t)
	body := fmt.Sprintf(`{"ownership":[{"matcher":"path","value":"src/billing/*","owner_user_id":%d},{"matcher":"tag:team","value":"core","owner_user_id":%d}],
"grouping":[{"matcher":"error_type","value":"Timeout*","fingerprint":"timeouts"}]}`, f.otherUser, f.userID)
	f.json(t, "editor", http.MethodPut, f.rulesPath(), body, http.StatusOK)
	got := f.json(t, "viewer", http.MethodGet, f.rulesPath(), ``, http.StatusOK)
	own, _ := got["ownership"].([]any)
	grp, _ := got["grouping"].([]any)
	if len(own) != 2 || len(grp) != 1 || own[0].(map[string]any)["value"] != "src/billing/*" {
		t.Fatalf("rules = %v", got)
	}
	site, _ := f.registry.Site(f.siteID)
	if site.App == nil || site.App.Rules == nil || site.App.Rules.Ownership.Len() != 2 || site.App.Rules.Grouping.Len() != 1 {
		t.Fatalf("registry rules = %+v", site.App)
	}
	if owner, ok := site.App.Rules.Ownership.Owner(nil, map[string]string{"team": "core"}); !ok || owner != f.userID {
		t.Fatalf("owner = %d %v", owner, ok)
	}
	// Replacing with nothing clears them, in the registry too.
	f.json(t, "owner", http.MethodPut, f.rulesPath(), `{"ownership":[],"grouping":[]}`, http.StatusOK)
	site, _ = f.registry.Site(f.siteID)
	if site.App.Rules != nil {
		t.Fatalf("rules not cleared: %+v", site.App.Rules)
	}
}

func TestAppRulesValidation(t *testing.T) {
	f := newAppIssuesFixture(t)
	good := fmt.Sprintf(`{"matcher":"path","value":"a/*","owner_user_id":%d}`, f.userID)
	for name, c := range map[string]struct{ body, want string }{
		"bad glob":        {`{"ownership":[` + good + `,{"matcher":"path","value":"a\\","owner_user_id":1}],"grouping":[]}`, "ownership[1]"},
		"bad matcher":     {`{"ownership":[],"grouping":[{"matcher":"function","value":"a","fingerprint":"f"}]}`, "grouping[0]"},
		"fingerprint":     {`{"ownership":[],"grouping":[{"matcher":"message","value":"a","fingerprint":""}]}`, "grouping[0]"},
		"non member":      {`{"ownership":[{"matcher":"path","value":"a","owner_user_id":999999}],"grouping":[]}`, "ownership[0]"},
		"unknown field":   {`{"ownership":[],"grouping":[],"x":1}`, ""},
		"ownership kinds": {`{"ownership":[{"matcher":"message","value":"a","owner_user_id":1}],"grouping":[]}`, "ownership[0]"},
	} {
		res := f.request(t, "owner", http.MethodPut, f.rulesPath(), c.body)
		if res.Code != http.StatusBadRequest || !strings.Contains(res.Body.String(), c.want) {
			t.Errorf("%s = %d %s", name, res.Code, res.Body)
		}
	}
	var sb strings.Builder
	sb.WriteString(`{"ownership":[`)
	for i := 0; i < 201; i++ {
		if i > 0 {
			sb.WriteString(",")
		}
		sb.WriteString(good)
	}
	sb.WriteString(`],"grouping":[]}`)
	if res := f.request(t, "owner", http.MethodPut, f.rulesPath(), sb.String()); res.Code != http.StatusBadRequest {
		t.Errorf("201 rules = %d", res.Code)
	}
}

func TestAppRulesGuardRoleAndTenant(t *testing.T) {
	f := newAppIssuesFixture(t)
	body := `{"ownership":[],"grouping":[]}`
	if res := f.request(t, "viewer", http.MethodPut, f.rulesPath(), body); res.Code != http.StatusForbidden {
		t.Errorf("viewer PUT = %d, want 403", res.Code)
	}
	if res := f.request(t, "foreign-owner", http.MethodPut, f.rulesPath(), body); res.Code != http.StatusNotFound {
		t.Errorf("foreign PUT = %d, want 404", res.Code)
	}
	if res := f.request(t, "foreign-owner", http.MethodGet, f.rulesPath(), ``); res.Code != http.StatusNotFound {
		t.Errorf("foreign GET = %d, want 404", res.Code)
	}
	if res := f.request(t, "key", http.MethodPut, f.rulesPath(), body); res.Code != http.StatusForbidden {
		t.Errorf("api key PUT = %d, want 403", res.Code)
	}
}
