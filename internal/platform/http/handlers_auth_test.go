package platformhttp_test

import (
	"context"
	"encoding/json"
	"fmt"
	"net/http"
	"net/http/httptest"
	"net/url"
	"strings"
	"testing"

	"supermetry/internal/db"
	platformauth "supermetry/internal/platform/auth"
	platformcrypto "supermetry/internal/platform/crypto"
	platformhttp "supermetry/internal/platform/http"
	"supermetry/internal/platform/mail"
	"supermetry/internal/platform/store"
	"supermetry/internal/platform/users"
	"supermetry/internal/testpg"
)

func TestSetupStatusReportsOnlyAvailability(t *testing.T) {
	tests := []struct {
		name      string
		addUser   bool
		available bool
	}{
		{name: "empty installation", available: true},
		{name: "initialized installation", addUser: true, available: false},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			handler, data, ctx := newSetupTestHandler(t)
			if tt.addUser {
				if _, err := users.New(data).Create(ctx, "owner@example.test", "Owner", "pw-123456789"); err != nil {
					t.Fatal(err)
				}
			}

			response := httptest.NewRecorder()
			handler.ServeHTTP(response, httptest.NewRequest(http.MethodGet, "/api/private/setup/status", nil))
			if response.Code != http.StatusOK {
				t.Fatalf("status=%d body=%s, want 200", response.Code, response.Body.String())
			}
			var body map[string]any
			if err := json.Unmarshal(response.Body.Bytes(), &body); err != nil {
				t.Fatal(err)
			}
			if len(body) != 1 || body["available"] != tt.available {
				t.Fatalf("body=%v, want only available=%t", body, tt.available)
			}
		})
	}
}

func TestSetupPostRejectedAfterInitialization(t *testing.T) {
	handler, _, ctx := newSetupTestHandler(t)
	authService := setupAuthFromHandler(t, handler)
	setupURL, ok := authService.SetupURL(ctx)
	if !ok {
		t.Fatal("setup URL missing")
	}
	parsed, err := url.Parse(setupURL)
	if err != nil {
		t.Fatal(err)
	}
	token := parsed.Query().Get("token")
	body := fmt.Sprintf(`{"token":%q,"email":"owner@example.test","name":"Owner","password":"pw-123456789","org_name":"Acme"}`, token)

	first := httptest.NewRecorder()
	handler.ServeHTTP(first, httptest.NewRequest(http.MethodPost, "/api/private/setup", strings.NewReader(body)))
	if first.Code != http.StatusCreated {
		t.Fatalf("first setup status=%d body=%s, want 201", first.Code, first.Body.String())
	}

	second := httptest.NewRecorder()
	handler.ServeHTTP(second, httptest.NewRequest(http.MethodPost, "/api/private/setup", strings.NewReader(body)))
	if second.Code != http.StatusBadRequest {
		t.Fatalf("repeated setup status=%d body=%s, want 400", second.Code, second.Body.String())
	}
}

type setupTestHandler struct {
	http.Handler
	auth *platformauth.Auth
}

func newSetupTestHandler(t *testing.T) (*setupTestHandler, *store.Store, context.Context) {
	t.Helper()
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
	return &setupTestHandler{
		Handler: platformhttp.New(platformhttp.Deps{Auth: authService}),
		auth:    authService,
	}, data, ctx
}

func setupAuthFromHandler(t *testing.T, handler *setupTestHandler) *platformauth.Auth {
	t.Helper()
	return handler.auth
}
