package platformhttp_test

import (
	"context"
	"encoding/json"
	"fmt"
	"net/http"
	"net/http/httptest"
	"strings"
	"testing"

	"errors"

	platformhttp "supermetry/internal/platform/http"
	"supermetry/internal/platform/sitesvc"
	"supermetry/internal/platform/store"
)

func (e adminOrgDeleteEnv) addRetentionSite(t *testing.T) int32 {
	t.Helper()
	site, err := e.sites.Create(context.Background(), e.owner, sitesvc.CreateInput{Name: "real", Hosts: []string{"real.example.test"}, Timezone: "UTC", RetentionDays: 30, RevenueCurrency: "USD"})
	if err != nil {
		t.Fatal(err)
	}
	return site.ID
}

func patchRetention(handler http.Handler, actor string, siteID int32, body string) *httptest.ResponseRecorder {
	request := httptest.NewRequest(http.MethodPatch, fmt.Sprintf("/api/private/admin/sites/%d", siteID), strings.NewReader(body))
	request.Header.Set("X-Test-Actor", actor)
	request.Header.Set("Content-Type", "application/json")
	request.Header.Set("X-CSRF-Token", "t")
	request.AddCookie(&http.Cookie{Name: "supermetry_csrf", Value: "t"})
	response := httptest.NewRecorder()
	handler.ServeHTTP(response, request)
	return response
}

func siteRetention(t *testing.T, e adminOrgDeleteEnv, siteID int32) int {
	t.Helper()
	var class int
	if err := e.pool.QueryRow(context.Background(), `select retention_class from sites where id=$1`, siteID).Scan(&class); err != nil {
		t.Fatal(err)
	}
	return class
}

func TestAdminSetRetention(t *testing.T) {
	e := newAdminOrgDeleteEnv(t)
	id := e.addRetentionSite(t)
	if response := patchRetention(e.handler, "member", id, `{"retention_days":365}`); response.Code != http.StatusForbidden {
		t.Fatalf("non-admin status = %d", response.Code)
	}
	response := patchRetention(e.handler, "admin", id, `{"retention_days":365}`)
	if response.Code != http.StatusOK {
		t.Fatalf("status = %d body=%s", response.Code, response.Body)
	}
	list := serveCSRF(e.handler, http.MethodGet, "/api/private/admin/sites", "admin")
	var body struct {
		Data []struct {
			ID            int32 `json:"id"`
			RetentionDays int   `json:"retention_days"`
		} `json:"data"`
	}
	if err := json.Unmarshal(list.Body.Bytes(), &body); err != nil {
		t.Fatal(err)
	}
	found := false
	for _, item := range body.Data {
		if item.ID == id {
			found = true
			if item.RetentionDays != 365 {
				t.Fatalf("listed retention_days = %d", item.RetentionDays)
			}
		}
	}
	if !found {
		t.Fatal("site not listed")
	}
	var count int
	var from, to float64
	var actor int64
	rows, err := e.pool.Query(context.Background(), `select actor_user_id, (metadata->>'from')::float, (metadata->>'to')::float from audit_log where action='instance.site.retention_changed' and target_type='site' and target_id=$1`, id)
	if err != nil {
		t.Fatal(err)
	}
	defer rows.Close()
	for rows.Next() {
		count++
		if err := rows.Scan(&actor, &from, &to); err != nil {
			t.Fatal(err)
		}
	}
	if count != 1 || from != 30 || to != 365 || actor == 0 {
		t.Fatalf("audit rows=%d from=%v to=%v actor=%d", count, from, to, actor)
	}
}

func TestAdminSetRetentionInvalid(t *testing.T) {
	e := newAdminOrgDeleteEnv(t)
	id := e.addRetentionSite(t)
	for _, body := range []string{`{"retention_days":60}`, `{}`, `{"retention_days":"x"}`} {
		if response := patchRetention(e.handler, "admin", id, body); response.Code != http.StatusBadRequest {
			t.Fatalf("%s status = %d body=%s", body, response.Code, response.Body)
		}
	}
	if got := siteRetention(t, e, id); got != 30 {
		t.Fatalf("retention changed to %d", got)
	}
}

func TestAdminSetRetentionDeletedSite404(t *testing.T) {
	e := newAdminOrgDeleteEnv(t)
	id := e.addRetentionSite(t)
	if err := e.sites.Delete(context.Background(), e.owner, id); err != nil {
		t.Fatal(err)
	}
	if response := patchRetention(e.handler, "admin", id, `{"retention_days":90}`); response.Code != http.StatusNotFound {
		t.Fatalf("deleted status = %d", response.Code)
	}
	if response := patchRetention(e.handler, "admin", 999999, `{"retention_days":90}`); response.Code != http.StatusNotFound {
		t.Fatalf("missing status = %d", response.Code)
	}
}

// An admin demoted after the principal was resolved must not mutate anything:
// both admin mutations re-check instance admin inside their transaction.
func TestAdminMutationsRecheckAdminInTx(t *testing.T) {
	e := newAdminOrgDeleteEnv(t)
	ctx := context.Background()
	id := e.addRetentionSite(t)
	var rootID int64
	if err := e.pool.QueryRow(ctx, `update users set is_admin=false where email='root@example.test' returning id`).Scan(&rootID); err != nil {
		t.Fatal(err)
	}
	if _, err := e.sites.AdminSetRetention(ctx, rootID, id, 365); !errors.Is(err, sitesvc.ErrForbidden) {
		t.Fatalf("retention err=%v, want ErrForbidden", err)
	}
	if got := siteRetention(t, e, id); got != 30 {
		t.Fatalf("retention=%d after forbidden change", got)
	}
	services := platformhttp.Services{Store: &store.Store{Pool: e.pool}, Sites: e.sites}
	if err := services.AdminDeleteOrg(ctx, rootID, e.orgID); !errors.Is(err, sitesvc.ErrForbidden) {
		t.Fatalf("delete org err=%v, want ErrForbidden", err)
	}
	var deleted bool
	if err := e.pool.QueryRow(ctx, `select deleted_at is not null from orgs where id=$1`, e.orgID).Scan(&deleted); err != nil || deleted {
		t.Fatalf("org deleted=%v err=%v", deleted, err)
	}
	var audits int
	if err := e.pool.QueryRow(ctx, `select count(*) from audit_log where action like 'instance.%'`).Scan(&audits); err != nil || audits != 0 {
		t.Fatalf("instance audit rows=%d err=%v", audits, err)
	}
}
