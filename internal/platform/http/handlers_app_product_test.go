package platformhttp_test

import (
	"context"
	"encoding/json"
	"fmt"
	"net/http"
	"reflect"
	"regexp"
	"strings"
	"testing"
	"time"

	"supermetry/internal/appfixture"
	"supermetry/internal/approllup"
	"supermetry/internal/db"
	"supermetry/internal/platform/authz"
)

// newAppProductFixture loads the plan's hand-computed product dataset (UTC,
// 10-06..10-09, installs A-D) into the link fixture's app site and rolls the
// four days up. The fixture clock is 2026-10-12, so every day is closed.
func newAppProductFixture(t *testing.T) linkFixture {
	t.Helper()
	f := newAppKeyFixture(t)
	ctx := context.Background()
	if _, err := f.pool.Exec(ctx, `update sites set timezone='UTC', retention_class=90 where id=$1`, f.siteID); err != nil {
		t.Fatal(err)
	}
	if err := f.registry.Invalidate(); err != nil {
		t.Fatal(err)
	}
	from, to := time.Date(2026, 9, 25, 0, 0, 0, 0, time.UTC), time.Date(2026, 11, 5, 0, 0, 0, 0, time.UTC)
	if err := db.EnsurePartitions(ctx, f.pool, from, to); err != nil {
		t.Fatal(err)
	}
	if err := db.EnsureAppEventPartitions(ctx, f.pool, from, to); err != nil {
		t.Fatal(err)
	}
	p, err := appfixture.AttachProduct(f.pool, f.siteID, "UTC")
	if err != nil {
		t.Fatal(err)
	}
	p.AddDataset(t, true)
	for _, day := range []string{"2026-10-06", "2026-10-07", "2026-10-08", "2026-10-09"} {
		if err := approllup.RecomputeDay(ctx, f.pool, f.siteID, appfixture.Day(day)); err != nil {
			t.Fatal(err)
		}
	}
	org := f.authenticator["owner"].OrgID
	f.authenticator["key-restricted"] = authz.Principal{KeyID: 12, OrgID: org, Role: authz.RoleKey, Scopes: []string{"stats:read"}, SiteIDs: []int32{f.otherSite}}
	f.authenticator["key-noscope"] = authz.Principal{KeyID: 13, OrgID: org, Role: authz.RoleKey, Scopes: []string{"sites:write"}}
	return f
}

const productRange = "from=2026-10-06&to=2026-10-09"

func (f linkFixture) product(t *testing.T, actor, prefix, suffix string) (int, map[string]any, string) {
	t.Helper()
	res := f.request(t, actor, http.MethodGet, fmt.Sprintf("%s/sites/%d/app/product/%s", prefix, f.siteID, suffix), ``)
	var body map[string]any
	_ = json.Unmarshal(res.Body.Bytes(), &body)
	return res.Code, body, res.Body.String()
}

func ints(rows any, key string) []int64 {
	var out []int64
	for _, r := range rows.([]any) {
		out = append(out, int64(r.(map[string]any)[key].(float64)))
	}
	return out
}

func TestAppProductHandComputedOverHTTP(t *testing.T) {
	f := newAppProductFixture(t)
	code, body, raw := f.product(t, "viewer", "/api/private", "active-users?"+productRange)
	if code != http.StatusOK {
		t.Fatalf("active-users = %d %s", code, raw)
	}
	if got := ints(body["series"], "dau"); !reflect.DeepEqual(got, []int64{3, 2, 2, 1}) {
		t.Fatalf("DAU = %v", got)
	}
	if got := ints(body["series"], "wau"); !reflect.DeepEqual(got, []int64{3, 4, 4, 4}) {
		t.Fatalf("WAU = %v", got)
	}
	if body["distinct"].(float64) != 4 || body["stickiness"].(float64) != 0.5 {
		t.Fatalf("distinct/stickiness = %v %v", body["distinct"], body["stickiness"])
	}
	if got := ints(body["new_installs"], "count"); !reflect.DeepEqual(got, []int64{3, 1, 0, 0}) {
		t.Fatalf("new installs = %v", got)
	}

	_, body, raw = f.product(t, "owner", "/api/private", "adoption?by=platform&"+productRange)
	totals := map[string]float64{}
	for _, r := range body["totals"].([]any) {
		row := r.(map[string]any)
		totals[row["value"].(string)] = row["installs"].(float64)
	}
	if !reflect.DeepEqual(totals, map[string]float64{"ios": 2, "android": 2}) {
		t.Fatalf("platform totals = %v (%s)", totals, raw)
	}
	_, body, raw = f.product(t, "owner", "/api/private", "adoption?by=release&from=2026-10-08&to=2026-10-09")
	share := map[string]float64{}
	for _, r := range body["series"].([]any) {
		row := r.(map[string]any)
		share[row["day"].(string)[:10]+" "+row["value"].(string)] = row["share"].(float64)
	}
	if !reflect.DeepEqual(share, map[string]float64{"2026-10-08 1.0.0": 0.5, "2026-10-08 1.1.0": 0.5, "2026-10-09 1.1.0": 1}) {
		t.Fatalf("release shares = %v (%s)", share, raw)
	}

	_, body, raw = f.product(t, "owner", "/api/private", "engagement?from=2026-10-06&to=2026-10-06")
	if body["sessions"].(float64) != 3 {
		t.Fatalf("engagement = %s", raw)
	}
	if body["mean_session_seconds"].(float64) != 60 || body["sessions_per_user"].(float64) != 1 {
		t.Fatalf("engagement = %s", raw)
	}
}

func TestAppProductFeaturesOverHTTP(t *testing.T) {
	f := newAppProductFixture(t)
	path := fmt.Sprintf("/api/private/sites/%d/app/product/features", f.siteID)
	res := f.request(t, "editor", http.MethodPost, path, `{"name":"Buying","event_names":["purchase"]}`)
	if res.Code != http.StatusCreated {
		t.Fatalf("create = %d %s", res.Code, res.Body)
	}
	var set struct {
		ID         int64    `json:"id"`
		EventNames []string `json:"event_names"`
	}
	if err := json.Unmarshal(res.Body.Bytes(), &set); err != nil || set.ID < 1 || len(set.EventNames) != 1 {
		t.Fatalf("created = %s %v", res.Body, err)
	}
	if res := f.request(t, "editor", http.MethodPost, path, `{"name":"Buying","event_names":["purchase"]}`); res.Code != http.StatusConflict {
		t.Fatalf("duplicate = %d", res.Code)
	}
	if res := f.request(t, "editor", http.MethodPost, path, `{"name":"","event_names":["a"]}`); res.Code != http.StatusBadRequest {
		t.Fatalf("empty name = %d", res.Code)
	}
	code, body, raw := f.product(t, "viewer", "/api/private", fmt.Sprintf("features/%d/adoption?%s", set.ID, productRange))
	if code != http.StatusOK || body["users"].(float64) != 3 || body["active_users"].(float64) != 4 || body["share"].(float64) != 0.75 {
		t.Fatalf("feature adoption = %d %s", code, raw)
	}
	if code, body, _ := f.product(t, "viewer", "/api/private", "features"); code != http.StatusOK || len(body["features"].([]any)) != 1 {
		t.Fatalf("list = %d %v", code, body)
	}
	if code, _, _ := f.product(t, "owner", "/api/private", "features/999999/adoption?"+productRange); code != http.StatusNotFound {
		t.Fatalf("unknown feature = %d", code)
	}
	if res := f.request(t, "editor", http.MethodDelete, fmt.Sprintf("%s/%d", path, set.ID), ``); res.Code != http.StatusNoContent {
		t.Fatalf("delete = %d", res.Code)
	}
	if res := f.request(t, "editor", http.MethodDelete, fmt.Sprintf("%s/%d", path, set.ID), ``); res.Code != http.StatusNotFound {
		t.Fatalf("second delete = %d", res.Code)
	}
}

// Review Focus 4: another org, a restricted key and a web project all get 404
// on every product route; a viewer reads but cannot write.
func TestAppProductTenantMatrix(t *testing.T) {
	f := newAppProductFixture(t)
	var setID int64
	if err := f.pool.QueryRow(context.Background(), `insert into app_feature_sets(site_id,name,event_names) values($1,'x','{purchase}') returning id`, f.siteID).Scan(&setID); err != nil {
		t.Fatal(err)
	}
	privateReads := []string{
		"active-users?" + productRange, "adoption?by=release&" + productRange, "engagement?" + productRange, "features",
		fmt.Sprintf("features/%d/adoption?%s", setID, productRange),
	}
	sessionActors := map[string]int{"viewer": http.StatusOK, "owner": http.StatusOK, "foreign-owner": http.StatusNotFound, "other-editor": http.StatusNotFound, "": http.StatusUnauthorized}
	for _, read := range privateReads {
		for actor, want := range sessionActors {
			if code, _, raw := f.product(t, actor, "/api/private", read); code != want {
				t.Errorf("private %s as %q = %d, want %d (%s)", read, actor, code, want, raw)
			}
		}
	}
	keyActors := map[string]int{"key": http.StatusOK, "key-restricted": http.StatusNotFound, "key-noscope": http.StatusForbidden, "foreign-owner": http.StatusNotFound}
	for _, read := range privateReads[:3] {
		for actor, want := range keyActors {
			if code, _, raw := f.product(t, actor, "/api/public/v1", read); code != want {
				t.Errorf("public %s as %q = %d, want %d (%s)", read, actor, code, want, raw)
			}
		}
	}
	if code, _, _ := f.product(t, "key", "/api/public/v1", privateReads[4]); code != http.StatusOK {
		t.Errorf("public feature adoption as key = %d", code)
	}
	write := fmt.Sprintf("/api/private/sites/%d/app/product/features", f.siteID)
	if res := f.request(t, "viewer", http.MethodPost, write, `{"name":"v","event_names":["a"]}`); res.Code != http.StatusForbidden {
		t.Errorf("viewer POST = %d, want 403", res.Code)
	}
	if res := f.request(t, "viewer", http.MethodDelete, fmt.Sprintf("%s/%d", write, setID), ``); res.Code != http.StatusForbidden {
		t.Errorf("viewer DELETE = %d, want 403", res.Code)
	}
	for _, actor := range []string{"foreign-owner", "other-editor"} {
		if res := f.request(t, actor, http.MethodPost, write, `{"name":"v","event_names":["a"]}`); res.Code != http.StatusNotFound {
			t.Errorf("%s POST = %d, want 404", actor, res.Code)
		}
		if res := f.request(t, actor, http.MethodDelete, fmt.Sprintf("%s/%d", write, setID), ``); res.Code != http.StatusNotFound {
			t.Errorf("%s DELETE = %d, want 404", actor, res.Code)
		}
	}
	var left int
	if err := f.pool.QueryRow(context.Background(), `select count(*) from app_feature_sets where site_id=$1`, f.siteID).Scan(&left); err != nil || left != 1 {
		t.Fatalf("refused writes changed the sets: %d %v", left, err)
	}
	if _, err := f.pool.Exec(context.Background(), `update sites set kind='web' where id=$1`, f.siteID); err != nil {
		t.Fatal(err)
	}
	if err := f.registry.Invalidate(); err != nil {
		t.Fatal(err)
	}
	for _, read := range privateReads {
		if code, _, _ := f.product(t, "owner", "/api/private", read); code != http.StatusNotFound {
			t.Errorf("web project %s = %d, want 404", read, code)
		}
	}
}

var longHex = regexp.MustCompile(`[0-9a-fA-F]{32,}`)

func TestAppProductOutputHasNoIdentifiers(t *testing.T) {
	f := newAppProductFixture(t)
	for _, read := range []string{"active-users?" + productRange, "adoption?by=country&" + productRange, "engagement?" + productRange} {
		_, _, raw := f.product(t, "owner", "/api/private", read)
		lower := strings.ToLower(raw)
		if strings.Contains(raw, `"i:`) || longHex.MatchString(raw) || strings.Contains(lower, "visitor") || strings.Contains(lower, "install_") {
			t.Errorf("%s leaks an identifier: %s", read, raw)
		}
	}
	if code, _, _ := f.product(t, "owner", "/api/private", "adoption?by=nope&"+productRange); code != http.StatusBadRequest {
		t.Errorf("unknown dimension = %d", code)
	}
}
