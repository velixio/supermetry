package platformhttp_test

import (
	"context"
	"encoding/json"
	"fmt"
	"net/http"
	"strings"
	"testing"
)

func newPixelFixture(t *testing.T) linkFixture {
	t.Helper()
	f := newLinkFixture(t)
	if _, err := f.pool.Exec(context.Background(), `update sites set kind='pixel' where id=$1`, f.siteID); err != nil {
		t.Fatal(err)
	}
	if err := f.registry.Invalidate(); err != nil {
		t.Fatal(err)
	}
	return f
}

func TestPixelsCRUDLimitAndStats(t *testing.T) {
	f := newPixelFixture(t)
	base := fmt.Sprintf("/api/private/sites/%d/pixels", f.siteID)
	res := f.request(t, "editor", http.MethodPost, base, `{"name":"GitHub profile"}`)
	if res.Code != http.StatusCreated {
		t.Fatalf("create %d %s", res.Code, res.Body)
	}
	var created map[string]any
	_ = json.NewDecoder(res.Body).Decode(&created)
	publicID := created["public_id"].(string)
	if created["badge_label"] != "views" || created["svg_url"] != "https://sm.example.test/px/"+publicID+".svg" {
		t.Fatalf("created = %v", created)
	}
	if _, ok := f.pixels.ByPublicID(publicID); !ok {
		t.Fatal("registry not reloaded after create")
	}
	id := int64(created["id"].(float64))
	if res := f.request(t, "viewer", http.MethodPost, base, `{"name":"x"}`); res.Code != http.StatusForbidden {
		t.Fatalf("viewer create = %d", res.Code)
	}
	if res := f.request(t, "editor", http.MethodPost, base, `{"name":""}`); res.Code != http.StatusBadRequest {
		t.Fatalf("empty name = %d", res.Code)
	}
	if res := f.request(t, "editor", http.MethodPatch, fmt.Sprintf("%s/%d", base, id), `{"badge_label":"hits","enabled":false}`); res.Code != http.StatusOK || !strings.Contains(res.Body.String(), `"enabled":false`) {
		t.Fatalf("patch %d %s", res.Code, res.Body)
	}
	for i := 1; i < 100; i++ {
		f.request(t, "editor", http.MethodPost, base, fmt.Sprintf(`{"name":"p%d"}`, i))
	}
	res = f.request(t, "editor", http.MethodPost, base, `{"name":"too many"}`)
	if res.Code != http.StatusConflict || res.Header().Get("Content-Type") != "application/problem+json" {
		t.Fatalf("101st = %d %s", res.Code, res.Header())
	}
	if _, err := f.pool.Exec(context.Background(), `insert into pixel_views_daily values ($1,'2026-10-12','','proxy',4)`, id); err != nil {
		t.Fatal(err)
	}
	res = f.request(t, "viewer", http.MethodGet, base+"/stats?from=2026-10-01&to=2026-10-13", "")
	if res.Code != 200 || !strings.Contains(res.Body.String(), `{"day":"2026-10-12","proxy":4,"direct":0}`) {
		t.Fatalf("stats %d %s", res.Code, res.Body)
	}
	if res := f.request(t, "editor", http.MethodDelete, fmt.Sprintf("%s/%d", base, id), ""); res.Code != http.StatusNoContent {
		t.Fatalf("delete = %d", res.Code)
	}
	web := fmt.Sprintf("/api/private/sites/%d/pixels", f.otherSite)
	if res := f.request(t, "owner", http.MethodGet, web, ""); res.Code != http.StatusNotFound {
		t.Fatalf("web project pixels = %d", res.Code)
	}
}
