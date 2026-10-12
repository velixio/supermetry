package platformhttp_test

import (
	"context"
	"encoding/json"
	"fmt"
	"net/http"
	"net/http/httptest"
	"strings"
	"testing"

	"supermetry/internal/api"
)

const dashboardLayoutJSON = `{"schema_version":1,"period":"7d","widgets":[{"id":"kpis","kind":"kpi_strip","x":0,"y":0,"w":12,"h":2,"config":{"metrics":["visitors","pageviews"]}},{"id":"top","kind":"breakdown","x":0,"y":2,"w":6,"h":5,"period":"28d","filters":[{"dimension":"country","operator":"is","values":["US"]}],"config":{"dimension":"source","metrics":["visitors"],"limit":5}}]}`

func dashboardBody(name string) string {
	return fmt.Sprintf(`{"name":%q,"layout":%s}`, name, dashboardLayoutJSON)
}

func dashboardsURL(siteID int32) string {
	return fmt.Sprintf("/api/private/sites/%d/dashboards", siteID)
}

func decodeDashboard(t *testing.T, body string) api.Dashboard {
	t.Helper()
	var dashboard api.Dashboard
	if err := json.NewDecoder(strings.NewReader(body)).Decode(&dashboard); err != nil {
		t.Fatal(err)
	}
	return dashboard
}

func TestDashboardsCRUDVersionDefaultAndAudit(t *testing.T) {
	h := newSegmentHarness(t)
	ctx := context.Background()
	if _, err := h.pool.Pool.Exec(ctx, `delete from audit_log`); err != nil {
		t.Fatal(err)
	}
	base := dashboardsURL(h.siteID)

	// No saved dashboards: the built-in layout is listed and is the default.
	var list api.DashboardList
	if err := json.NewDecoder(segmentRawRequest(t, h.handler, "viewer", http.MethodGet, base, "", http.StatusOK).Body).Decode(&list); err != nil {
		t.Fatal(err)
	}
	if len(list.Data) != 1 || !list.Data[0].Builtin || list.Data[0].Id != 0 || !list.Data[0].IsDefault || len(list.Data[0].Layout.Widgets) < 5 {
		t.Fatalf("builtin list = %+v", list)
	}
	if def := decodeDashboard(t, segmentRawRequest(t, h.handler, "viewer", http.MethodGet, base+"/default", "", http.StatusOK).Body.String()); !def.Builtin {
		t.Fatalf("default = %+v", def)
	}

	created := decodeDashboard(t, segmentRawRequest(t, h.handler, "owner", http.MethodPost, base, dashboardBody("  Marketing "), http.StatusCreated).Body.String())
	if created.Name != "Marketing" || created.Version != 1 || created.IsDefault || created.Builtin || created.SiteId != h.siteID || len(created.Layout.Widgets) != 2 || created.CreatedBy == nil {
		t.Fatalf("created = %+v", created)
	}
	path := fmt.Sprintf("%s/%d", base, created.Id)

	got := decodeDashboard(t, segmentRawRequest(t, h.handler, "viewer", http.MethodGet, path, "", http.StatusOK).Body.String())
	if got.Id != created.Id || *got.Layout.Widgets[1].Period != "28d" || *got.Layout.Widgets[1].Config.Limit != 5 {
		t.Fatalf("got = %+v", got)
	}

	// Optimistic concurrency: the second writer holding version 1 loses.
	update := func(version int, name string) string {
		return fmt.Sprintf(`{"name":%q,"version":%d,"layout":{"schema_version":1,"widgets":[]}}`, name, version)
	}
	updated := decodeDashboard(t, segmentRawRequest(t, h.handler, "editor", http.MethodPatch, path, update(1, "Renamed"), http.StatusOK).Body.String())
	if updated.Version != 2 || updated.Name != "Renamed" || len(updated.Layout.Widgets) != 0 {
		t.Fatalf("updated = %+v", updated)
	}
	segmentRawRequest(t, h.handler, "owner", http.MethodPatch, path, update(1, "Stale"), http.StatusConflict)

	// Default handling.
	set := decodeDashboard(t, segmentRawRequest(t, h.handler, "editor", http.MethodPost, path+"/default", "", http.StatusOK).Body.String())
	if !set.IsDefault || set.Version != 2 {
		t.Fatalf("set default = %+v", set)
	}
	copied := decodeDashboard(t, segmentRawRequest(t, h.handler, "owner", http.MethodPost, path+"/duplicate", "", http.StatusCreated).Body.String())
	second := decodeDashboard(t, segmentRawRequest(t, h.handler, "owner", http.MethodPost, path+"/duplicate", "", http.StatusCreated).Body.String())
	if copied.Name != "Renamed (copy)" || second.Name != "Renamed (copy 2)" || copied.IsDefault || copied.Version != 1 {
		t.Fatalf("copies = %+v %+v", copied, second)
	}
	copyPath := fmt.Sprintf("%s/%d", base, copied.Id)
	segmentRawRequest(t, h.handler, "owner", http.MethodPost, copyPath+"/default", "", http.StatusOK)
	if def := decodeDashboard(t, segmentRawRequest(t, h.handler, "viewer", http.MethodGet, base+"/default", "", http.StatusOK).Body.String()); def.Id != copied.Id {
		t.Fatalf("default = %+v, want copy", def)
	}
	if first := decodeDashboard(t, segmentRawRequest(t, h.handler, "viewer", http.MethodGet, path, "", http.StatusOK).Body.String()); first.IsDefault {
		t.Fatal("previous default still flagged")
	}
	segmentRawRequest(t, h.handler, "editor", http.MethodDelete, base+"/default", "", http.StatusNoContent)
	if def := decodeDashboard(t, segmentRawRequest(t, h.handler, "viewer", http.MethodGet, base+"/default", "", http.StatusOK).Body.String()); !def.Builtin {
		t.Fatalf("default after clear = %+v", def)
	}
	for _, id := range []int64{created.Id, copied.Id, second.Id} {
		segmentRawRequest(t, h.handler, "owner", http.MethodDelete, fmt.Sprintf("%s/%d", base, id), "", http.StatusNoContent)
	}
	segmentRawRequest(t, h.handler, "owner", http.MethodDelete, path, "", http.StatusNotFound)

	rows, err := h.pool.Pool.Query(ctx, `select action from audit_log order by id`)
	if err != nil {
		t.Fatal(err)
	}
	defer rows.Close()
	var actions []string
	for rows.Next() {
		var action string
		if err := rows.Scan(&action); err != nil {
			t.Fatal(err)
		}
		actions = append(actions, action)
	}
	want := "[dashboard.created dashboard.updated dashboard.default_set dashboard.duplicated dashboard.duplicated dashboard.default_set dashboard.default_cleared dashboard.deleted dashboard.deleted dashboard.deleted]"
	if fmt.Sprint(actions) != want {
		t.Fatalf("audit actions = %v, want %s", actions, want)
	}
}

func TestDashboardValidationRejectsUnsafeLayouts(t *testing.T) {
	h := newSegmentHarness(t)
	widget := func(extra string) string {
		return `{"schema_version":1,"widgets":[{"id":"a","kind":"kpi_strip","x":0,"y":0,"w":12,"h":2,"config":{"metrics":["visitors"]}` + extra + `}]}`
	}
	many := make([]string, 25)
	for i := range many {
		many[i] = fmt.Sprintf(`{"id":"w%d","kind":"retention","x":0,"y":%d,"w":4,"h":2}`, i, i)
	}
	tests := map[string]string{
		"malformed JSON":      `{"name":`,
		"blank name":          `{"name":"  ","layout":` + widget("") + `}`,
		"name too long":       `{"name":"` + strings.Repeat("x", 81) + `","layout":` + widget("") + `}`,
		"missing layout":      `{"name":"A"}`,
		"wrong version":       `{"name":"A","layout":{"schema_version":2,"widgets":[]}}`,
		"unknown layout key":  `{"name":"A","layout":{"schema_version":1,"widgets":[],"sql":"select 1"}}`,
		"unknown widget key":  `{"name":"A","layout":` + widget(`,"sql":"select 1"`) + `}`,
		"unknown kind":        `{"name":"A","layout":{"schema_version":1,"widgets":[{"id":"a","kind":"sql","x":0,"y":0,"w":2,"h":2}]}}`,
		"unknown metric":      `{"name":"A","layout":{"schema_version":1,"widgets":[{"id":"a","kind":"kpi_strip","x":0,"y":0,"w":2,"h":2,"config":{"metrics":["drop_table"]}}]}}`,
		"unknown dimension":   `{"name":"A","layout":{"schema_version":1,"widgets":[{"id":"a","kind":"breakdown","x":0,"y":0,"w":2,"h":2,"config":{"dimension":"x; drop","metrics":["visitors"]}}]}}`,
		"missing dimension":   `{"name":"A","layout":{"schema_version":1,"widgets":[{"id":"a","kind":"breakdown","x":0,"y":0,"w":2,"h":2,"config":{"metrics":["visitors"]}}]}}`,
		"foreign option":      `{"name":"A","layout":{"schema_version":1,"widgets":[{"id":"a","kind":"retention","x":0,"y":0,"w":2,"h":2,"config":{"limit":3}}]}}`,
		"limit too high":      `{"name":"A","layout":{"schema_version":1,"widgets":[{"id":"a","kind":"goals","x":0,"y":0,"w":2,"h":2,"config":{"limit":51}}]}}`,
		"map revenue metric":  `{"name":"A","layout":{"schema_version":1,"widgets":[{"id":"a","kind":"map","x":0,"y":0,"w":2,"h":2,"config":{"metric":"total_revenue"}}]}}`,
		"funnel without id":   `{"name":"A","layout":{"schema_version":1,"widgets":[{"id":"a","kind":"funnel","x":0,"y":0,"w":2,"h":2}]}}`,
		"outside grid":        `{"name":"A","layout":{"schema_version":1,"widgets":[{"id":"a","kind":"retention","x":8,"y":0,"w":6,"h":2}]}}`,
		"zero height":         `{"name":"A","layout":{"schema_version":1,"widgets":[{"id":"a","kind":"retention","x":0,"y":0,"w":6,"h":0}]}}`,
		"bad widget id":       `{"name":"A","layout":{"schema_version":1,"widgets":[{"id":"A b","kind":"retention","x":0,"y":0,"w":6,"h":2}]}}`,
		"duplicate widget id": `{"name":"A","layout":{"schema_version":1,"widgets":[{"id":"a","kind":"retention","x":0,"y":0,"w":6,"h":2},{"id":"a","kind":"retention","x":6,"y":0,"w":6,"h":2}]}}`,
		"too many widgets":    `{"name":"A","layout":{"schema_version":1,"widgets":[` + strings.Join(many, ",") + `]}}`,
		"unknown period":      `{"name":"A","layout":{"schema_version":1,"period":"custom","widgets":[]}}`,
		"unknown compare":     `{"name":"A","layout":{"schema_version":1,"compare":"x","widgets":[]}}`,
		"bad filter":          `{"name":"A","layout":{"schema_version":1,"filters":[{"dimension":"planet","operator":"is","values":["x"]}],"widgets":[]}}`,
		"too many filters":    `{"name":"A","layout":{"schema_version":1,"filters":` + repeatedFilters(11) + `,"widgets":[]}}`,
		"title too long":      `{"name":"A","layout":` + widget(`,"title":"`+strings.Repeat("t", 81)+`"`) + `}`,
	}
	for name, body := range tests {
		t.Run(name, func(t *testing.T) {
			segmentRawRequest(t, h.handler, "owner", http.MethodPost, dashboardsURL(h.siteID), body, http.StatusBadRequest)
		})
	}
	segmentRawRequest(t, h.handler, "owner", http.MethodPost, dashboardsURL(h.siteID), dashboardBody("Same"), http.StatusCreated)
	segmentRawRequest(t, h.handler, "owner", http.MethodPost, dashboardsURL(h.siteID), dashboardBody("same"), http.StatusConflict)
}

func TestDashboardAuthorizationMatrix(t *testing.T) {
	h := newSegmentHarness(t)
	base := dashboardsURL(h.siteID)
	created := decodeDashboard(t, segmentRawRequest(t, h.handler, "owner", http.MethodPost, base, dashboardBody("Saved"), http.StatusCreated).Body.String())
	path := fmt.Sprintf("%s/%d", base, created.Id)
	patch := `{"name":"No","version":1,"layout":{"schema_version":1,"widgets":[]}}`

	// Signed out.
	for _, call := range []struct{ method, path, body string }{
		{http.MethodGet, base, ""}, {http.MethodPost, base, dashboardBody("No")}, {http.MethodGet, path, ""},
		{http.MethodPatch, path, patch}, {http.MethodDelete, path, ""}, {http.MethodPost, path + "/duplicate", ""},
		{http.MethodPost, path + "/default", ""}, {http.MethodGet, base + "/default", ""}, {http.MethodDelete, base + "/default", ""},
	} {
		segmentRawRequest(t, h.handler, "nobody", call.method, call.path, call.body, http.StatusUnauthorized)
	}
	// Viewers read but never write.
	segmentRawRequest(t, h.handler, "viewer", http.MethodGet, base, "", http.StatusOK)
	segmentRawRequest(t, h.handler, "viewer", http.MethodGet, path, "", http.StatusOK)
	segmentRawRequest(t, h.handler, "viewer", http.MethodPost, base, dashboardBody("No"), http.StatusForbidden)
	segmentRawRequest(t, h.handler, "viewer", http.MethodPatch, path, patch, http.StatusForbidden)
	segmentRawRequest(t, h.handler, "viewer", http.MethodDelete, path, "", http.StatusForbidden)
	segmentRawRequest(t, h.handler, "viewer", http.MethodPost, path+"/duplicate", "", http.StatusForbidden)
	segmentRawRequest(t, h.handler, "viewer", http.MethodPost, path+"/default", "", http.StatusForbidden)
	segmentRawRequest(t, h.handler, "viewer", http.MethodDelete, base+"/default", "", http.StatusForbidden)
	// A key without the segments scope cannot write; with it, it can.
	segmentRawRequest(t, h.handler, "noscope", http.MethodPost, base, dashboardBody("No"), http.StatusForbidden)
	// Another organization sees nothing.
	segmentRawRequest(t, h.handler, "outside", http.MethodGet, base, "", http.StatusNotFound)
	segmentRawRequest(t, h.handler, "outside", http.MethodGet, path, "", http.StatusNotFound)
	segmentRawRequest(t, h.handler, "outside", http.MethodPatch, path, patch, http.StatusNotFound)
	segmentRawRequest(t, h.handler, "outside", http.MethodDelete, path, "", http.StatusNotFound)
	// Unknown id.
	segmentRawRequest(t, h.handler, "owner", http.MethodGet, base+"/99999", "", http.StatusNotFound)
	segmentRawRequest(t, h.handler, "owner", http.MethodPatch, base+"/99999", patch, http.StatusNotFound)
}

// A dashboard id that belongs to another site must not be reachable through
// this site's path, even by a caller who is authorized here.
func TestDashboardIDFromAnotherSiteIsNotFound(t *testing.T) {
	h := newSegmentHarness(t)
	ctx := context.Background()
	var siteB int32
	if err := h.pool.Pool.QueryRow(ctx, `insert into sites(org_id,public_id,name,timezone,retention_class) values ($1,'dashsiteb001','Site B','UTC',30) returning id`, h.owner.OrgID).Scan(&siteB); err != nil {
		t.Fatal(err)
	}
	var otherID int64
	if err := h.pool.Pool.QueryRow(ctx, `insert into dashboards(site_id,name,layout) values ($1,'Secret','{"schema_version":1,"widgets":[]}') returning id`, siteB).Scan(&otherID); err != nil {
		t.Fatal(err)
	}
	path := fmt.Sprintf("%s/%d", dashboardsURL(h.siteID), otherID)
	patch := `{"name":"Hijack","version":1,"layout":{"schema_version":1,"widgets":[]}}`
	for _, actor := range []string{"editor", "owner"} {
		segmentRawRequest(t, h.handler, actor, http.MethodGet, path, "", http.StatusNotFound)
		segmentRawRequest(t, h.handler, actor, http.MethodPatch, path, patch, http.StatusNotFound)
		segmentRawRequest(t, h.handler, actor, http.MethodPost, path+"/default", "", http.StatusNotFound)
		segmentRawRequest(t, h.handler, actor, http.MethodPost, path+"/duplicate", "", http.StatusNotFound)
		segmentRawRequest(t, h.handler, actor, http.MethodDelete, path, "", http.StatusNotFound)
	}
	// An editor limited to site A cannot reach site B's own path either.
	other := fmt.Sprintf("%s/%d", dashboardsURL(siteB), otherID)
	if code := rawStatus(t, h, "editor", http.MethodGet, other); code != http.StatusNotFound && code != http.StatusForbidden {
		t.Fatalf("editor on site B path: status %d", code)
	}
	var name string
	var version int
	var count int
	if err := h.pool.Pool.QueryRow(ctx, `select name,version,(select count(*) from dashboards) from dashboards where id=$1`, otherID).Scan(&name, &version, &count); err != nil {
		t.Fatal(err)
	}
	if name != "Secret" || version != 1 || count != 1 {
		t.Fatalf("site B dashboard changed: %q v%d, %d rows", name, version, count)
	}
}

func rawStatus(t *testing.T, h segmentHarness, actor, method, path string) int {
	t.Helper()
	request, _ := http.NewRequest(method, path, nil)
	request.Header.Set("X-Test-Actor", actor)
	recorder := httptest.NewRecorder()
	h.handler.ServeHTTP(recorder, request)
	return recorder.Code
}

func TestDashboardsAreCappedPerSite(t *testing.T) {
	h := newSegmentHarness(t)
	if _, err := h.pool.Pool.Exec(context.Background(), `insert into dashboards(site_id,name,layout)
		select $1,'bulk '||g,'{"schema_version":1,"widgets":[]}' from generate_series(1,50) g`, h.siteID); err != nil {
		t.Fatal(err)
	}
	segmentRawRequest(t, h.handler, "owner", http.MethodPost, dashboardsURL(h.siteID), dashboardBody("One too many"), http.StatusConflict)
}
