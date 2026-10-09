package auth_test

import (
	"context"
	"errors"
	"net/http"
	"net/http/httptest"
	"net/url"
	"strings"
	"testing"
	"time"
	"unicode/utf8"

	"github.com/pquerna/otp"
	"github.com/pquerna/otp/totp"

	"supermetry/internal/db"
	"supermetry/internal/platform/auth"
	platformcrypto "supermetry/internal/platform/crypto"
	"supermetry/internal/platform/mail"
	"supermetry/internal/platform/store"
	"supermetry/internal/platform/users"
	"supermetry/internal/testpg"
)

var ctx = context.Background()

type fakeClock struct{ now time.Time }

func newFakeClock() *fakeClock {
	return &fakeClock{now: time.Date(2026, 10, 7, 12, 0, 0, 0, time.UTC)}
}

func (c *fakeClock) Now() time.Time          { return c.now }
func (c *fakeClock) Advance(d time.Duration) { c.now = c.now.Add(d) }

type disabledMail struct{}

func (disabledMail) Send(context.Context, mail.Message) error { return nil }
func (disabledMail) Enabled() bool                            { return false }

type authEnv struct {
	a     *auth.Auth
	users *users.Users
	clock *fakeClock
	store *store.Store
}

func newAuth(t *testing.T) authEnv {
	t.Helper()
	return newAuthWithClock(t, newFakeClock())
}

func newAuthWithClock(t *testing.T, clock *fakeClock) authEnv {
	t.Helper()
	pool, _ := testpg.New(t)
	if err := db.Migrate(ctx, pool); err != nil {
		t.Fatal(err)
	}
	s := &store.Store{Pool: pool}
	box, err := platformcrypto.NewSecretBox([]byte("0123456789abcdef0123456789abcdef"), "panel/totp/v1")
	if err != nil {
		t.Fatal(err)
	}
	return authEnv{
		a:     auth.New(s, disabledMail{}, box, "https://supermetry.example", auth.WithClock(clock)),
		users: users.New(s),
		clock: clock,
		store: s,
	}
}

func createUser(t *testing.T, env authEnv, email, password string) int64 {
	t.Helper()
	id, err := env.users.Create(ctx, email, "User", password)
	if err != nil {
		t.Fatal(err)
	}
	return id
}

func TestLoginRateLimitIsPerNormalizedEmail(t *testing.T) {
	env := newAuth(t)
	createUser(t, env, "Ana@Example.com", "pw-123456789")
	for i := 0; i < 5; i++ {
		email := []string{"ana@example.com", "ANA@EXAMPLE.COM", "Ana@Example.com"}[i%3]
		if _, err := env.a.Login(ctx, email, "wrong", "", "x"); !errors.Is(err, auth.ErrInvalid) {
			t.Fatalf("attempt %d: %v", i+1, err)
		}
	}
	if _, err := env.a.Login(ctx, "ana@example.com", "pw-123456789", "", "x"); !errors.Is(err, auth.ErrRateLimited) {
		t.Fatalf("6th attempt must be limited, got %v", err)
	}
}

func TestLoginRateLimitUsesClientKey(t *testing.T) {
	env := newAuth(t)
	createUser(t, env, "u@x.io", "pw-123456789")
	clientCtx := auth.WithClientKey(ctx, "ephemeral-client-hash")
	for i := 0; i < 20; i++ {
		_, _ = env.a.Login(clientCtx, "missing"+string(rune('a'+i))+"@x.io", "wrong", "", "x")
	}
	if _, err := env.a.Login(clientCtx, "u@x.io", "pw-123456789", "", "x"); !errors.Is(err, auth.ErrRateLimited) {
		t.Fatalf("21st client attempt must be limited, got %v", err)
	}
}

func TestSignupRateLimitUsesClientKey(t *testing.T) {
	env := newAuth(t)
	clientCtx := auth.WithClientKey(ctx, "ephemeral-signup-client-hash")
	for i := 0; i < 20; i++ {
		_, _ = env.a.Signup(clientCtx, "", "", "", "", "")
	}
	if _, err := env.a.Signup(clientCtx, "new@example.test", "New", "pw-123456789", "New org", ""); !errors.Is(err, auth.ErrRateLimited) {
		t.Fatalf("21st client attempt must be limited, got %v", err)
	}
}

func TestLoginTruncatesDeviceLabelAtUTF8Boundary(t *testing.T) {
	env := newAuth(t)
	createUser(t, env, "u@x.io", "pw-123456789")
	label := strings.Repeat("a", 199) + "界"
	cookie, err := env.a.Login(ctx, "u@x.io", "pw-123456789", "", label)
	if err != nil {
		t.Fatal(err)
	}
	session, ok := env.a.Session(ctx, cookie)
	if !ok {
		t.Fatal("session missing")
	}
	if !utf8.ValidString(session.DeviceLabel) || len(session.DeviceLabel) > 200 {
		t.Fatalf("invalid device label %q", session.DeviceLabel)
	}
}

func TestSessionIdleAndAbsoluteExpiry(t *testing.T) {
	clock := newFakeClock()
	env := newAuthWithClock(t, clock)
	createUser(t, env, "u@x.io", "pw-123456789")
	cookie, err := env.a.Login(ctx, "u@x.io", "pw-123456789", "", "Chrome on macOS")
	if err != nil {
		t.Fatal(err)
	}
	clock.Advance(13 * 24 * time.Hour)
	if _, ok := env.a.Session(ctx, cookie); !ok {
		t.Fatal("within idle window")
	}
	clock.Advance(15 * 24 * time.Hour)
	if _, ok := env.a.Session(ctx, cookie); ok {
		t.Fatal("idle expiry not applied")
	}

	cookie, err = env.a.Login(ctx, "u@x.io", "pw-123456789", "", "x")
	if err != nil {
		t.Fatal(err)
	}
	for i := 0; i < 10; i++ {
		clock.Advance(10 * 24 * time.Hour)
		env.a.Session(ctx, cookie)
	}
	if _, ok := env.a.Session(ctx, cookie); ok {
		t.Fatal("absolute 90d expiry not applied")
	}
}

func TestSessionListRevokeAndLogout(t *testing.T) {
	env := newAuth(t)
	uid := createUser(t, env, "u@x.io", "pw-123456789")
	first, err := env.a.Login(ctx, "u@x.io", "pw-123456789", "", "Chrome on macOS")
	if err != nil {
		t.Fatal(err)
	}
	second, err := env.a.Login(ctx, "u@x.io", "pw-123456789", "", "Firefox on Linux")
	if err != nil {
		t.Fatal(err)
	}
	sessions, err := env.a.Sessions(ctx, uid)
	if err != nil || len(sessions) != 2 {
		t.Fatalf("Sessions() = %+v, %v", sessions, err)
	}
	var firstID string
	for _, session := range sessions {
		if session.DeviceLabel == "Chrome on macOS" {
			firstID = session.ID
		}
	}
	if firstID == "" {
		t.Fatal("first session missing from list")
	}
	if err := env.a.Revoke(ctx, uid, firstID); err != nil {
		t.Fatal(err)
	}
	if err := env.a.Logout(ctx, second); err != nil {
		t.Fatal(err)
	}
	if _, ok := env.a.Session(ctx, first); ok {
		t.Fatal("revoked session remains active")
	}
	remaining, err := env.a.Sessions(ctx, uid)
	if err != nil || len(remaining) != 0 {
		t.Fatalf("remaining sessions = %+v, %v", remaining, err)
	}
}

func TestSessionsOmitsExpiredSessions(t *testing.T) {
	env := newAuth(t)
	uid := createUser(t, env, "u@x.io", "pw-123456789")
	if _, err := env.a.Login(ctx, "u@x.io", "pw-123456789", "", "x"); err != nil {
		t.Fatal(err)
	}
	env.clock.Advance(91 * 24 * time.Hour)
	sessions, err := env.a.Sessions(ctx, uid)
	if err != nil {
		t.Fatal(err)
	}
	if len(sessions) != 0 {
		t.Fatalf("expired sessions listed: %+v", sessions)
	}
}

func TestResetTokenSingleUse(t *testing.T) {
	env := newAuth(t)
	createUser(t, env, "u@x.io", "old-password-1")
	link, err := env.a.StartReset(ctx, "u@x.io")
	if err != nil {
		t.Fatal(err)
	}
	token := tokenFrom(t, link)
	if err := env.a.FinishReset(ctx, token, "new-password-1"); err != nil {
		t.Fatal(err)
	}
	if err := env.a.FinishReset(ctx, token, "again-password"); !errors.Is(err, auth.ErrInvalid) {
		t.Fatalf("reset token reused: %v", err)
	}
	if _, err := env.a.Login(ctx, "u@x.io", "new-password-1", "", "x"); err != nil {
		t.Fatalf("new password rejected: %v", err)
	}
}

func TestResetTokenExpiresAndRevokesSessions(t *testing.T) {
	env := newAuth(t)
	createUser(t, env, "u@x.io", "old-password-1")
	cookie, err := env.a.Login(ctx, "u@x.io", "old-password-1", "", "x")
	if err != nil {
		t.Fatal(err)
	}
	link, err := env.a.StartReset(ctx, "u@x.io")
	if err != nil {
		t.Fatal(err)
	}
	env.clock.Advance(61 * time.Minute)
	if err := env.a.FinishReset(ctx, tokenFrom(t, link), "new-password-1"); !errors.Is(err, auth.ErrInvalid) {
		t.Fatalf("expired reset accepted: %v", err)
	}
	if _, ok := env.a.Session(ctx, cookie); !ok {
		t.Fatal("failed reset revoked session")
	}

	link, err = env.a.StartReset(ctx, "u@x.io")
	if err != nil {
		t.Fatal(err)
	}
	if err := env.a.FinishReset(ctx, tokenFrom(t, link), "new-password-1"); err != nil {
		t.Fatal(err)
	}
	if _, ok := env.a.Session(ctx, cookie); ok {
		t.Fatal("password reset did not revoke sessions")
	}
}

func TestTOTPRequiredAfterEnroll(t *testing.T) {
	env := newAuth(t)
	uid := createUser(t, env, "u@x.io", "pw-123456789")
	otpauthURL, recovery, err := env.a.EnrollTOTP(ctx, uid, "pw-123456789", "")
	if err != nil {
		t.Fatal(err)
	}
	if len(recovery) != 10 {
		t.Fatalf("recovery codes = %d, want 10", len(recovery))
	}
	if err := env.a.ConfirmTOTP(ctx, uid, codeFor(t, otpauthURL, env.clock.Now())); err != nil {
		t.Fatal(err)
	}
	if _, err := env.a.Login(ctx, "u@x.io", "pw-123456789", "", "x"); !errors.Is(err, auth.ErrTOTPRequired) {
		t.Fatalf("missing TOTP = %v", err)
	}
	if _, err := env.a.Login(ctx, "u@x.io", "pw-123456789", recovery[0], "x"); err != nil {
		t.Fatalf("recovery code rejected: %v", err)
	}
	if _, err := env.a.Login(ctx, "u@x.io", "pw-123456789", recovery[0], "x"); !errors.Is(err, auth.ErrInvalid) {
		t.Fatalf("recovery code reused: %v", err)
	}
}

func TestSetupOnlyOnce(t *testing.T) {
	env := newAuth(t)
	setupURL, ok := env.a.SetupURL(ctx)
	if !ok {
		t.Fatal("setup URL missing on empty install")
	}
	uid, oid, err := env.a.CompleteSetup(ctx, tokenFrom(t, setupURL), "o@x.io", "Owner", "pw-123456789", "Acme")
	if err != nil || uid == 0 || oid == 0 {
		t.Fatalf("CompleteSetup() = %d, %d, %v", uid, oid, err)
	}
	if _, ok := env.a.SetupURL(ctx); ok {
		t.Fatal("setup still open")
	}
	if _, _, err := env.a.CompleteSetup(ctx, tokenFrom(t, setupURL), "e@x.io", "Evil", "pw-123456789", "Evil"); !errors.Is(err, auth.ErrInvalid) {
		t.Fatalf("setup token reusable: %v", err)
	}
	var role string
	if err := env.store.Pool.QueryRow(ctx, `select role from memberships where org_id=$1 and user_id=$2`, oid, uid).Scan(&role); err != nil || role != "owner" {
		t.Fatalf("owner membership = %q, %v", role, err)
	}
}

func TestCSRFRejectsMissingToken(t *testing.T) {
	called := 0
	handler := auth.CSRF(http.HandlerFunc(func(w http.ResponseWriter, _ *http.Request) {
		called++
		w.WriteHeader(http.StatusNoContent)
	}))

	t.Run("GET passes", func(t *testing.T) {
		req := httptest.NewRequest(http.MethodGet, "/api/private/me", nil)
		res := httptest.NewRecorder()
		handler.ServeHTTP(res, req)
		if res.Code != http.StatusNoContent {
			t.Fatalf("status = %d", res.Code)
		}
	})

	t.Run("POST without header is a problem", func(t *testing.T) {
		req := httptest.NewRequest(http.MethodPost, "/api/private/me", nil)
		req.AddCookie(&http.Cookie{Name: auth.CSRFCookieName, Value: "token"})
		res := httptest.NewRecorder()
		handler.ServeHTTP(res, req)
		if res.Code != http.StatusForbidden || res.Header().Get("Content-Type") != "application/problem+json" {
			t.Fatalf("status/content-type = %d/%q", res.Code, res.Header().Get("Content-Type"))
		}
		if !strings.Contains(res.Body.String(), `"status":403`) {
			t.Fatalf("body = %s", res.Body.String())
		}
	})

	t.Run("matching cookie and header passes", func(t *testing.T) {
		req := httptest.NewRequest(http.MethodPost, "/api/private/me", nil)
		req.AddCookie(&http.Cookie{Name: auth.CSRFCookieName, Value: "token"})
		req.Header.Set(auth.CSRFHeaderName, "token")
		res := httptest.NewRecorder()
		handler.ServeHTTP(res, req)
		if res.Code != http.StatusNoContent {
			t.Fatalf("status = %d", res.Code)
		}
	})

	if called != 2 {
		t.Fatalf("next called %d times, want 2", called)
	}
}

func tokenFrom(t *testing.T, rawURL string) string {
	t.Helper()
	u, err := url.Parse(rawURL)
	if err != nil {
		t.Fatal(err)
	}
	token := u.Query().Get("token")
	if token == "" {
		t.Fatalf("URL has no token: %q", rawURL)
	}
	return token
}

func codeFor(t *testing.T, rawURL string, now time.Time) string {
	t.Helper()
	key, err := otp.NewKeyFromURL(rawURL)
	if err != nil {
		t.Fatal(err)
	}
	code, err := totp.GenerateCodeCustom(key.Secret(), now, totp.ValidateOpts{
		Period:    30,
		Skew:      1,
		Digits:    otp.DigitsSix,
		Algorithm: otp.AlgorithmSHA1,
	})
	if err != nil {
		t.Fatal(err)
	}
	return code
}
