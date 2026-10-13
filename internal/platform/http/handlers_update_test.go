package platformhttp_test

import (
	"context"
	"encoding/json"
	"net/http"
	"net/http/httptest"
	"testing"
	"time"

	"supermetry/internal/db"
	"supermetry/internal/platform/admin"
	platformhttp "supermetry/internal/platform/http"
	"supermetry/internal/platform/store"
	"supermetry/internal/testpg"
	"supermetry/internal/updatecheck"
)

func TestAdminUpdateStatus(t *testing.T) {
	pool, _ := testpg.New(t)
	ctx := context.Background()
	if err := db.Migrate(ctx, pool); err != nil {
		t.Fatal(err)
	}
	var userID int64
	if err := pool.QueryRow(ctx, `insert into users(email,name,password_hash,is_admin) values ('update-admin@example.test','Admin','hash',true) returning id`).Scan(&userID); err != nil {
		t.Fatal(err)
	}
	checkedAt := time.Date(2026, 10, 14, 4, 5, 6, 0, time.UTC)
	latest := "v1.3.0"
	releaseURL := "https://github.com/velixio/supermetry/releases/tag/v1.3.0"
	checker := &fakeUpdateChecker{status: updatecheck.Status{
		CurrentVersion: "1.2.0", LatestVersion: &latest, UpdateAvailable: true,
		ReleaseURL: &releaseURL, CheckedAt: &checkedAt,
	}}
	data := &store.Store{Pool: pool}
	handler := platformhttp.New(platformhttp.Deps{
		Version: "1.2.0", Authenticator: actorAuthenticator{"admin": {UserID: userID, SiteIDs: []int32{}}},
		Store: data, Admin: admin.New(data), UpdateChecker: checker,
	})
	request := httptest.NewRequest(http.MethodGet, "/api/private/admin/update", nil)
	request.Header.Set("X-Test-Actor", "admin")
	response := httptest.NewRecorder()
	handler.ServeHTTP(response, request)
	if response.Code != http.StatusOK {
		t.Fatalf("status=%d body=%s", response.Code, response.Body.String())
	}
	var got updatecheck.Status
	if err := json.Unmarshal(response.Body.Bytes(), &got); err != nil {
		t.Fatal(err)
	}
	if got.CurrentVersion != "1.2.0" || got.LatestVersion == nil || *got.LatestVersion != latest || !got.UpdateAvailable || got.PublishedAt != nil || got.CheckError != nil {
		t.Fatalf("response=%+v", got)
	}
	if checker.calls != 1 {
		t.Fatalf("checker calls=%d, want 1", checker.calls)
	}
}

type fakeUpdateChecker struct {
	status updatecheck.Status
	calls  int
}

func (f *fakeUpdateChecker) Check(context.Context) updatecheck.Status {
	f.calls++
	return f.status
}
