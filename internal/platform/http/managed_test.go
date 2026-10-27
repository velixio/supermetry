package platformhttp_test

import (
	"context"
	"encoding/json"
	"net/http"
	"net/http/httptest"
	"strings"
	"testing"

	"supermetry/internal/db"
	"supermetry/internal/platform/admin"
	platformauth "supermetry/internal/platform/auth"
	platformcrypto "supermetry/internal/platform/crypto"
	platformhttp "supermetry/internal/platform/http"
	"supermetry/internal/platform/mail"
	"supermetry/internal/platform/store"
	"supermetry/internal/testpg"
	"supermetry/internal/updatecheck"
	"supermetry/internal/updatehelper"
)

func TestAuthConfigReportsManaged(t *testing.T) {
	pool, _ := testpg.New(t)
	if err := db.Migrate(context.Background(), pool); err != nil {
		t.Fatal(err)
	}
	box, err := platformcrypto.NewSecretBox([]byte("0123456789abcdef0123456789abcdef"), "supermetry/totp/v1")
	if err != nil {
		t.Fatal(err)
	}
	authService := platformauth.New(&store.Store{Pool: pool}, &mail.Fake{}, box, "https://supermetry.example")
	for _, managed := range []bool{false, true} {
		handler := platformhttp.New(platformhttp.Deps{Auth: authService, Signup: "open", Managed: managed})
		response := httptest.NewRecorder()
		handler.ServeHTTP(response, httptest.NewRequest(http.MethodGet, "/api/private/auth/config", nil))
		var body map[string]any
		if err := json.Unmarshal(response.Body.Bytes(), &body); err != nil {
			t.Fatalf("status=%d body=%s err=%v", response.Code, response.Body.String(), err)
		}
		if got, ok := body["managed"].(bool); !ok || got != managed {
			t.Fatalf("managed = %v (%T), want %v; body=%s", body["managed"], body["managed"], managed, response.Body.String())
		}
	}
}

// A managed instance never replaces its binary or forces a release check, whether
// the caller is a session admin or holds the admin API key. The status stays readable
// but reports the update as unsupported.
func TestManagedInstanceRefusesUpdates(t *testing.T) {
	pool, _ := testpg.New(t)
	ctx := context.Background()
	if err := db.Migrate(ctx, pool); err != nil {
		t.Fatal(err)
	}
	var userID int64
	if err := pool.QueryRow(ctx, `insert into users(email,name,password_hash,is_admin) values ('managed-update@example.test','Admin','hash',true) returning id`).Scan(&userID); err != nil {
		t.Fatal(err)
	}
	const key = "managed-admin-key-0123456789abcdef0123456789"
	latest := "v1.3.0"
	checker := &fakeUpdateChecker{status: updatecheck.Status{CurrentVersion: "1.2.0", LatestVersion: &latest, UpdateAvailable: true}}
	starter := &fakeUpdateStarter{}
	data := &store.Store{Pool: pool}
	for _, managed := range []bool{true, false} {
		handler := platformhttp.New(platformhttp.Deps{
			Version: "v1.2.0", Authenticator: actorAuthenticator{"admin": {UserID: userID}},
			Store: data, Admin: admin.New(data), UpdateChecker: checker,
			UpdateMode: updatehelper.DeploymentBinary, UpdateStarter: starter,
			AdminAPIKey: key, AdminServiceUserID: userID, Managed: managed,
		})
		call := func(method, path, body string, viaKey bool) *httptest.ResponseRecorder {
			r := httptest.NewRequest(method, path, strings.NewReader(body))
			r.Header.Set("Content-Type", "application/json")
			if viaKey {
				r.Header.Set("Authorization", "Bearer "+key)
			} else {
				r.Header.Set("X-Test-Actor", "admin")
				r.Header.Set("X-CSRF-Token", "token")
				r.AddCookie(&http.Cookie{Name: "supermetry_csrf", Value: "token"})
			}
			w := httptest.NewRecorder()
			handler.ServeHTTP(w, r)
			return w
		}
		for _, viaKey := range []bool{false, true} {
			for _, path := range []string{"/api/private/admin/update/check", "/api/private/admin/update/apply"} {
				w := call(http.MethodPost, path, `{"target_version":"v1.3.0","idempotency_key":"k1","password":"x"}`, viaKey)
				if managed {
					if w.Code != http.StatusConflict || !strings.Contains(w.Body.String(), "managed by the operator") {
						t.Fatalf("managed %s key=%v: status=%d body=%s", path, viaKey, w.Code, w.Body.String())
					}
				} else if path == "/api/private/admin/update/check" && w.Code != http.StatusOK {
					t.Fatalf("unmanaged check key=%v: status=%d body=%s", viaKey, w.Code, w.Body.String())
				}
			}
			status := call(http.MethodGet, "/api/private/admin/update", "", viaKey)
			var got struct {
				UpdateSupported bool `json:"update_supported"`
			}
			if status.Code != http.StatusOK || json.Unmarshal(status.Body.Bytes(), &got) != nil || got.UpdateSupported == managed {
				t.Fatalf("managed=%v key=%v status: %d update_supported=%v body=%s", managed, viaKey, status.Code, got.UpdateSupported, status.Body.String())
			}
		}
	}
	if len(starter.requests) != 0 {
		t.Fatalf("the updater was started %d times", len(starter.requests))
	}
}
