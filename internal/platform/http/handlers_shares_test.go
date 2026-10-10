package platformhttp_test

import (
	"context"
	"encoding/json"
	"net/http"
	"net/http/httptest"
	"strconv"
	"testing"
	"time"

	"supermetry/internal/db"
	"supermetry/internal/platform/authn"
	"supermetry/internal/platform/authz"
	platformhttp "supermetry/internal/platform/http"
	"supermetry/internal/platform/shares"
	"supermetry/internal/platform/store"
	coresites "supermetry/internal/sites"
	"supermetry/internal/testpg"
)

func TestShareExpiryHTTPManagementAndViewer(t *testing.T) {
	pool, _ := testpg.New(t)
	ctx := context.Background()
	if err := db.Migrate(ctx, pool); err != nil {
		t.Fatal(err)
	}
	var userID, orgID int64
	if err := pool.QueryRow(ctx, `insert into users(email,name,password_hash) values ('share-owner@example.test','Owner','hash') returning id`).Scan(&userID); err != nil {
		t.Fatal(err)
	}
	if err := pool.QueryRow(ctx, `insert into orgs(name,slug) values ('Shares','shares') returning id`).Scan(&orgID); err != nil {
		t.Fatal(err)
	}
	if _, err := pool.Exec(ctx, `insert into memberships(org_id,user_id,role) values ($1,$2,'owner')`, orgID, userID); err != nil {
		t.Fatal(err)
	}
	var siteID int32
	if err := pool.QueryRow(ctx, `insert into sites(org_id,public_id,name,timezone,retention_class) values ($1,'sharesite001','Share site','UTC',30) returning id`, orgID).Scan(&siteID); err != nil {
		t.Fatal(err)
	}
	data := &store.Store{Pool: pool}
	service := shares.New(data, []byte("0123456789abcdef0123456789abcdef"))
	owner := authz.Principal{UserID: userID, OrgID: orgID, Role: authz.RoleOwner}
	registry := siteRegistry{siteID: coresites.Site{ID: siteID, OrgID: orgID, PublicID: "sharesite001", TZ: time.UTC}}
	handler := platformhttp.New(platformhttp.Deps{
		Authenticator: actorAuthenticator{"owner": owner}, Sites: registry, Store: data,
		Shares: service, Query: routeQuery{},
	})

	created := requestAs(t, handler, "owner", http.MethodPost, "/api/private/sites/"+stringID(siteID)+"/shares", `{"password":"correct horse battery staple","expires_in_days":7}`)
	if created.Code != http.StatusCreated {
		t.Fatalf("create status=%d body=%s", created.Code, created.Body.String())
	}
	var response struct {
		Slug  string `json:"slug"`
		Share struct {
			ID        int64      `json:"id"`
			ExpiresAt *time.Time `json:"expires_at"`
		} `json:"share"`
	}
	if err := json.NewDecoder(created.Body).Decode(&response); err != nil {
		t.Fatal(err)
	}
	if response.Slug == "" || response.Share.ID == 0 || response.Share.ExpiresAt == nil {
		t.Fatalf("created share = %+v", response)
	}

	changed := requestAs(t, handler, "owner", http.MethodPatch, "/api/private/sites/"+stringID(siteID)+"/shares/"+stringID64(response.Share.ID), `{"expires_in_days":30}`)
	if changed.Code != http.StatusNoContent {
		t.Fatalf("patch status=%d body=%s", changed.Code, changed.Body.String())
	}

	listed := requestAs(t, handler, "owner", http.MethodGet, "/api/private/sites/"+stringID(siteID)+"/shares", "")
	if listed.Code != http.StatusOK {
		t.Fatalf("list status=%d body=%s", listed.Code, listed.Body.String())
	}
	var list struct {
		Data []struct {
			ExpiresAt *time.Time `json:"expires_at"`
		} `json:"data"`
	}
	if err := json.NewDecoder(listed.Body).Decode(&list); err != nil || len(list.Data) != 1 || list.Data[0].ExpiresAt == nil {
		t.Fatalf("listed shares = %+v error=%v", list, err)
	}

	cookie, err := service.Open(ctx, response.Slug, "correct horse battery staple")
	if err != nil {
		t.Fatal(err)
	}
	if _, err := pool.Exec(ctx, `update shared_links set expires_at=now()-interval '1 second' where id=$1`, response.Share.ID); err != nil {
		t.Fatal(err)
	}
	viewer := platformhttp.New(platformhttp.Deps{
		Authenticator: platformhttp.NewAuthenticator(nil, nil, service, nil), Sites: registry,
		Shares: service, Query: routeQuery{},
	})
	expiredRequest := httptest.NewRequest(http.MethodGet, "/api/private/share/"+response.Slug+"/stats/summary?period=7d", nil)
	expiredRequest.AddCookie(&http.Cookie{Name: authn.ShareCookieName, Value: cookie})
	expired := httptest.NewRecorder()
	viewer.ServeHTTP(expired, expiredRequest)
	missing := httptest.NewRecorder()
	viewer.ServeHTTP(missing, httptest.NewRequest(http.MethodGet, "/api/private/share/shr_missing/stats/summary?period=7d", nil))
	if expired.Code != http.StatusNotFound || missing.Code != http.StatusNotFound || expired.Body.String() != missing.Body.String() {
		t.Fatalf("expired=(%d,%q) missing=(%d,%q)", expired.Code, expired.Body.String(), missing.Code, missing.Body.String())
	}
}

func stringID(value int32) string { return stringID64(int64(value)) }

func stringID64(value int64) string {
	return strconv.FormatInt(value, 10)
}
