package platformhttp_test

import (
	"context"
	"net/http"
	"testing"
)

func TestAppReleaseDetailEndpoint(t *testing.T) {
	h := newAppHealthFixture(t)
	for _, actor := range []string{"owner", "editor", "viewer"} {
		code, body, raw := h.get(t, actor, "releases/1.0.0?from=2026-10-01&to=2026-10-13")
		if code != http.StatusOK {
			t.Fatalf("%s = %d %s", actor, code, raw)
		}
		for _, key := range []string{"release", "status", "health", "rollout", "deploys", "new_issues", "regressed_issues", "commit_count", "health_source"} {
			if body[key] == nil {
				t.Fatalf("%s: missing %s in %s", actor, key, raw)
			}
		}
		if body["release"] != "1.0.0" || len(body["new_issues"].([]any)) != 2 {
			t.Fatalf("detail = %s", raw)
		}
	}
	if code, _, _ := h.get(t, "owner", "releases/9.9.9?from=2026-10-01&to=2026-10-13"); code != http.StatusNotFound {
		t.Fatalf("unknown release = %d, want 404", code)
	}
	if code, _, _ := h.get(t, "foreign-owner", "releases/1.0.0?from=2026-10-01&to=2026-10-13"); code != http.StatusNotFound {
		t.Fatalf("foreign org = %d, want 404", code)
	}
	if code, _, _ := h.get(t, "owner", "releases/1.0.0?from=2026-10-13&to=2026-10-01"); code != http.StatusBadRequest {
		t.Fatalf("bad range = %d, want 400", code)
	}
}

func TestAppReleaseCompareEndpoint(t *testing.T) {
	h := newAppHealthFixture(t)
	if _, err := h.pool.Exec(context.Background(), `insert into app_releases(org_id,version) select org_id,'2.0.0' from sites where id=$1`, h.siteID); err != nil {
		t.Fatal(err)
	}
	code, body, raw := h.get(t, "viewer", "releases/compare?a=1.0.0&b=2.0.0&from=2026-10-01&to=2026-10-13")
	if code != http.StatusOK || body["a"] == nil || body["b"] == nil || body["delta"] == nil || body["only_in_b"] == nil {
		t.Fatalf("compare = %d %s", code, raw)
	}
	if code, _, _ := h.get(t, "viewer", "releases/compare?a=1.0.0&from=2026-10-01&to=2026-10-13"); code != http.StatusBadRequest {
		t.Fatalf("missing b = %d, want 400", code)
	}
	if code, _, _ := h.get(t, "viewer", "releases/compare?a=1.0.0&b=9.9.9&from=2026-10-01&to=2026-10-13"); code != http.StatusNotFound {
		t.Fatalf("unknown b = %d, want 404", code)
	}
	if code, _, _ := h.get(t, "foreign-owner", "releases/compare?a=1.0.0&b=2.0.0&from=2026-10-01&to=2026-10-13"); code != http.StatusNotFound {
		t.Fatalf("foreign org = %d, want 404", code)
	}
}
