package platformhttp_test

import (
	"context"
	"encoding/json"
	"net/http"
	"net/http/httptest"
	"strings"
	"testing"
	"time"

	"supermetry/internal/db"
	"supermetry/internal/platform/admin"
	platformauth "supermetry/internal/platform/auth"
	platformcrypto "supermetry/internal/platform/crypto"
	platformhttp "supermetry/internal/platform/http"
	"supermetry/internal/platform/mail"
	"supermetry/internal/platform/store"
	"supermetry/internal/platform/users"
	"supermetry/internal/testpg"
	"supermetry/internal/updatecheck"
	"supermetry/internal/updatehelper"
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

func TestAdminApplyUpdateRequiresReauthenticationAndDispatches(t *testing.T) {
	pool, _ := testpg.New(t)
	ctx := context.Background()
	if err := db.Migrate(ctx, pool); err != nil {
		t.Fatal(err)
	}
	data := &store.Store{Pool: pool}
	box, err := platformcrypto.NewSecretBox([]byte("0123456789abcdef0123456789abcdef"), "supermetry/totp/v1")
	if err != nil {
		t.Fatal(err)
	}
	authService := platformauth.New(data, &mail.Fake{}, box, "https://supermetry.example")
	userID, err := users.New(data).Create(ctx, "update-apply@example.test", "Admin", "pw-123456789")
	if err != nil {
		t.Fatal(err)
	}
	if _, err := pool.Exec(ctx, `update users set is_admin=true where id=$1`, userID); err != nil {
		t.Fatal(err)
	}
	starter := &fakeUpdateStarter{}
	handler := platformhttp.New(platformhttp.Deps{
		Version: "v1.2.0", Auth: authService, Store: data, Admin: admin.New(data),
		Authenticator: actorAuthenticator{"admin": {UserID: userID}},
		UpdateMode:    updatehelper.DeploymentBinary, UpdateStarter: starter,
		UpdateNonce: func() (string, error) { return "MDEyMzQ1Njc4OWFiY2RlZjAxMjM0NTY3ODlhYmNkZWY", nil },
	})

	request := func(body string) *httptest.ResponseRecorder {
		t.Helper()
		r := httptest.NewRequest(http.MethodPost, "/api/private/admin/update/apply", strings.NewReader(body))
		r.Header.Set("Content-Type", "application/json")
		r.Header.Set("X-Test-Actor", "admin")
		r.Header.Set("X-CSRF-Token", "token")
		r.AddCookie(&http.Cookie{Name: "supermetry_csrf", Value: "token"})
		w := httptest.NewRecorder()
		handler.ServeHTTP(w, r)
		return w
	}
	wrong := request(`{"target_version":"v1.3.0","idempotency_key":"update-2026-10-14","password":"wrong"}`)
	if wrong.Code != http.StatusUnauthorized || len(starter.requests) != 0 {
		t.Fatalf("wrong password status=%d requests=%d body=%s", wrong.Code, len(starter.requests), wrong.Body.String())
	}
	got := request(`{"target_version":"v1.3.0","idempotency_key":"update-2026-10-14","password":"pw-123456789"}`)
	if got.Code != http.StatusAccepted || len(starter.requests) != 1 {
		t.Fatalf("status=%d requests=%d body=%s", got.Code, len(starter.requests), got.Body.String())
	}
	if starter.requests[0].TargetVersion != "v1.3.0" || starter.requests[0].JobID == "" {
		t.Fatalf("helper request=%+v", starter.requests[0])
	}
	var job admin.UpdateJob
	if err := json.Unmarshal(got.Body.Bytes(), &job); err != nil || job.Status != "requested" {
		t.Fatalf("job=%+v err=%v", job, err)
	}
	retry := request(`{"target_version":"v1.3.0","idempotency_key":"update-2026-10-14","password":"pw-123456789"}`)
	if retry.Code != http.StatusAccepted || len(starter.requests) != 1 {
		t.Fatalf("retry status=%d requests=%d body=%s", retry.Code, len(starter.requests), retry.Body.String())
	}
}

type fakeUpdateStarter struct{ requests []updatehelper.Request }

func (f *fakeUpdateStarter) Start(_ context.Context, request updatehelper.Request) (updatehelper.Response, error) {
	f.requests = append(f.requests, request)
	return updatehelper.Response{Version: updatehelper.ProtocolVersion, JobID: request.JobID, State: updatehelper.StateRequested}, nil
}

func (f *fakeUpdateStarter) Status(_ context.Context, jobID string) (updatehelper.Response, error) {
	return updatehelper.Response{Version: updatehelper.ProtocolVersion, JobID: jobID, State: updatehelper.StateRequested}, nil
}

type fakeUpdateChecker struct {
	status updatecheck.Status
	calls  int
}

func (f *fakeUpdateChecker) Check(context.Context) updatecheck.Status {
	f.calls++
	return f.status
}
