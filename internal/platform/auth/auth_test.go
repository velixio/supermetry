package auth_test

import (
	"context"
	"crypto/hmac"
	"crypto/sha256"
	"encoding/hex"
	"encoding/json"
	"errors"
	"net/http"
	"net/http/httptest"
	"net/url"
	"strings"
	"testing"
	"time"
	"unicode/utf8"

	"github.com/jackc/pgx/v5"
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

var testSecretKey = []byte("0123456789abcdef0123456789abcdef")

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
	box, err := platformcrypto.NewSecretBox(testSecretKey, "supermetry/totp/v1")
	if err != nil {
		t.Fatal(err)
	}
	return authEnv{
		a:     auth.New(s, disabledMail{}, box, "https://supermetry.example", auth.WithClock(clock), auth.WithAuditSecret(testSecretKey)),
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
	createUser(t, env, "Ana@example.com", "pw-123456789")
	for i := 0; i < 5; i++ {
		email := []string{"ana@example.com", "ANA@example.com", "Ana@example.com"}[i%3]
		if _, err := env.a.Login(ctx, email, "wrong", "", "x"); !errors.Is(err, auth.ErrInvalid) {
			t.Fatalf("attempt %d: %v", i+1, err)
		}
	}
	if _, err := env.a.Login(ctx, "ana@example.com", "pw-123456789", "", "x"); !errors.Is(err, auth.ErrRateLimited) {
		t.Fatalf("6th attempt must be limited, got %v", err)
	}
}

func TestLoginSuccessAndFailureAreAuditedWithoutRequestSecrets(t *testing.T) {
	env := newAuth(t)
	userID := createUser(t, env, "u@example.test", "pw-123456789")
	orgID := addOwnerMembership(t, env, userID)
	const client = "plaintext-client-identifier"
	loginCtx := auth.WithClientKey(ctx, client)
	if _, err := env.a.Login(loginCtx, "u@example.test", "wrong-password", "", "Browser"); !errors.Is(err, auth.ErrInvalid) {
		t.Fatalf("failed Login() error = %v", err)
	}
	var actorUserID *int64
	var metadataJSON []byte
	waitFor(t, func() (bool, error) {
		err := env.store.Pool.QueryRow(ctx, `select actor_user_id,metadata from audit_log where org_id=$1 and action='auth.login_failed'`, orgID).Scan(&actorUserID, &metadataJSON)
		if errors.Is(err, pgx.ErrNoRows) {
			return false, nil
		}
		return err == nil, err
	})
	if actorUserID != nil {
		t.Fatalf("failed-login actor_user_id = %d, want NULL", *actorUserID)
	}
	var metadata map[string]any
	if err := json.Unmarshal(metadataJSON, &metadata); err != nil {
		t.Fatal(err)
	}
	mac := hmac.New(sha256.New, testSecretKey)
	_, _ = mac.Write([]byte("supermetry/auth-login-client/v1\x00" + client))
	if got := metadata["client_hash"]; got != hex.EncodeToString(mac.Sum(nil)) {
		t.Fatalf("client_hash = %v, want keyed hash", got)
	}
	if strings.Contains(string(metadataJSON), client) {
		t.Fatalf("audit metadata retained plaintext client: %s", metadataJSON)
	}
	if _, err := env.a.Login(loginCtx, "u@example.test", "pw-123456789", "", "Browser"); err != nil {
		t.Fatal(err)
	}
	rows, err := env.store.Pool.Query(ctx, `select action from audit_log where org_id=$1 and action like 'auth.login%' order by id`, orgID)
	if err != nil {
		t.Fatal(err)
	}
	defer rows.Close()
	var got []string
	for rows.Next() {
		var action string
		if err := rows.Scan(&action); err != nil {
			t.Fatal(err)
		}
		got = append(got, action)
	}
	if strings.Join(got, ",") != "auth.login_failed,auth.login" {
		t.Fatalf("audit actions = %v", got)
	}
}

func TestLoginAuditFailureStillReturnsInvalidCredentials(t *testing.T) {
	env := newAuth(t)
	userID := createUser(t, env, "u@example.test", "pw-123456789")
	addOwnerMembership(t, env, userID)
	if _, err := env.store.Pool.Exec(ctx, `
		create sequence login_audit_attempts;
		create function reject_login_audit() returns trigger language plpgsql as $$
		begin
			if new.action = 'auth.login_failed' then
				perform nextval('login_audit_attempts');
				raise exception 'injected audit failure';
			end if;
			return new;
		end $$;
		create trigger reject_login_audit before insert on audit_log
		for each row execute function reject_login_audit()`); err != nil {
		t.Fatal(err)
	}

	if _, err := env.a.Login(auth.WithClientKey(ctx, "client"), "u@example.test", "wrong", "", "Browser"); !errors.Is(err, auth.ErrInvalid) {
		t.Fatalf("Login() error = %v, want ErrInvalid", err)
	}
	waitFor(t, func() (bool, error) {
		var called bool
		err := env.store.Pool.QueryRow(ctx, `select is_called from login_audit_attempts`).Scan(&called)
		return called, err
	})
}

func TestLoginRateLimitUsesClientKey(t *testing.T) {
	env := newAuth(t)
	createUser(t, env, "u@example.test", "pw-123456789")
	clientCtx := auth.WithClientKey(ctx, "ephemeral-client-hash")
	for i := 0; i < 20; i++ {
		_, _ = env.a.Login(clientCtx, "missing"+string(rune('a'+i))+"@example.test", "wrong", "", "x")
	}
	if _, err := env.a.Login(clientCtx, "u@example.test", "pw-123456789", "", "x"); !errors.Is(err, auth.ErrRateLimited) {
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

func TestSignupRejectsWeakPassword(t *testing.T) {
	env := newAuth(t)
	if _, err := env.a.Signup(auth.WithClientKey(ctx, "signup-client"), "new@example.test", "New", "password123", "New org", ""); !errors.Is(err, platformcrypto.ErrPasswordCommon) {
		t.Fatalf("Signup() error = %v, want ErrPasswordCommon", err)
	}
}

func TestResetRequestsAreLimitedPerTargetAndClient(t *testing.T) {
	env := newAuth(t)
	createUser(t, env, "u@example.test", "old-password-1")
	clientCtx := auth.WithClientKey(ctx, "reset-client")
	for i := 0; i < 3; i++ {
		if _, err := env.a.StartReset(clientCtx, "U@EXAMPLE.TEST"); err != nil {
			t.Fatalf("attempt %d: %v", i+1, err)
		}
	}
	if _, err := env.a.StartReset(clientCtx, "u@example.test"); !errors.Is(err, auth.ErrRateLimited) {
		t.Fatalf("paired client limit error = %v, want ErrRateLimited", err)
	}
	for _, client := range []string{"other-client", "third-client"} {
		if _, err := env.a.StartReset(auth.WithClientKey(ctx, client), "u@example.test"); err != nil {
			t.Fatalf("distinct client %q unexpectedly limited: %v", client, err)
		}
	}
	if _, err := env.a.StartReset(auth.WithClientKey(ctx, "fourth-client"), "u@example.test"); !errors.Is(err, auth.ErrRateLimited) {
		t.Fatalf("target limit error = %v, want ErrRateLimited", err)
	}

	env = newAuth(t)
	clientCtx = auth.WithClientKey(ctx, "shared-client")
	for i := 0; i < 20; i++ {
		_, _ = env.a.StartReset(clientCtx, "missing"+string(rune('a'+i))+"@example.test")
	}
	if _, err := env.a.StartReset(clientCtx, "another@example.test"); !errors.Is(err, auth.ErrRateLimited) {
		t.Fatalf("client limit error = %v, want ErrRateLimited", err)
	}
}

func TestLoginTruncatesDeviceLabelAtUTF8Boundary(t *testing.T) {
	env := newAuth(t)
	createUser(t, env, "u@example.test", "pw-123456789")
	label := strings.Repeat("a", 199) + "界"
	cookie, err := env.a.Login(ctx, "u@example.test", "pw-123456789", "", label)
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
	createUser(t, env, "u@example.test", "pw-123456789")
	cookie, err := env.a.Login(ctx, "u@example.test", "pw-123456789", "", "Chrome on macOS")
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

	cookie, err = env.a.Login(ctx, "u@example.test", "pw-123456789", "", "x")
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
	uid := createUser(t, env, "u@example.test", "pw-123456789")
	first, err := env.a.Login(ctx, "u@example.test", "pw-123456789", "", "Chrome on macOS")
	if err != nil {
		t.Fatal(err)
	}
	second, err := env.a.Login(ctx, "u@example.test", "pw-123456789", "", "Firefox on Linux")
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

func TestSessionRevocationsAreAudited(t *testing.T) {
	env := newAuth(t)
	userID := createUser(t, env, "u@example.test", "pw-123456789")
	orgID := addOwnerMembership(t, env, userID)
	first, err := env.a.Login(ctx, "u@example.test", "pw-123456789", "", "first")
	if err != nil {
		t.Fatal(err)
	}
	second, err := env.a.Login(ctx, "u@example.test", "pw-123456789", "", "second")
	if err != nil {
		t.Fatal(err)
	}
	sessions, err := env.a.Sessions(ctx, userID)
	if err != nil {
		t.Fatal(err)
	}
	var secondID string
	for _, session := range sessions {
		if session.DeviceLabel == "second" {
			secondID = session.ID
		}
	}
	if err := env.a.Revoke(ctx, userID, secondID); err != nil {
		t.Fatal(err)
	}
	if err := env.a.RevokeOthers(ctx, userID, first); err != nil {
		t.Fatal(err)
	}
	var actions []string
	rows, err := env.store.Pool.Query(ctx, `select action from audit_log where org_id=$1 and action like 'session%' order by id`, orgID)
	if err != nil {
		t.Fatal(err)
	}
	defer rows.Close()
	for rows.Next() {
		var action string
		if err := rows.Scan(&action); err != nil {
			t.Fatal(err)
		}
		actions = append(actions, action)
	}
	if strings.Join(actions, ",") != "session.revoked,sessions.revoked_all" {
		t.Fatalf("audit actions = %v", actions)
	}
	_ = second
}

func TestSessionsOmitsExpiredSessions(t *testing.T) {
	env := newAuth(t)
	uid := createUser(t, env, "u@example.test", "pw-123456789")
	if _, err := env.a.Login(ctx, "u@example.test", "pw-123456789", "", "x"); err != nil {
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
	createUser(t, env, "u@example.test", "old-password-1")
	link, err := env.a.StartReset(ctx, "u@example.test")
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
	if _, err := env.a.Login(ctx, "u@example.test", "new-password-1", "", "x"); err != nil {
		t.Fatalf("new password rejected: %v", err)
	}
}

func TestFinishResetRejectsWeakPassword(t *testing.T) {
	env := newAuth(t)
	createUser(t, env, "u@example.test", "old-password-1")
	link, err := env.a.StartReset(auth.WithClientKey(ctx, "reset-client"), "u@example.test")
	if err != nil {
		t.Fatal(err)
	}
	if err := env.a.FinishReset(ctx, tokenFrom(t, link), "short"); !errors.Is(err, platformcrypto.ErrPasswordTooShort) {
		t.Fatalf("FinishReset() error = %v, want ErrPasswordTooShort", err)
	}
	if err := env.a.FinishReset(ctx, tokenFrom(t, link), "sixsix"); err != nil {
		t.Fatalf("FinishReset() six-character password error = %v", err)
	}
}

func TestResetTokenExpiresAndRevokesSessions(t *testing.T) {
	env := newAuth(t)
	createUser(t, env, "u@example.test", "old-password-1")
	cookie, err := env.a.Login(ctx, "u@example.test", "old-password-1", "", "x")
	if err != nil {
		t.Fatal(err)
	}
	link, err := env.a.StartReset(ctx, "u@example.test")
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

	link, err = env.a.StartReset(ctx, "u@example.test")
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
	uid := createUser(t, env, "u@example.test", "pw-123456789")
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
	if _, err := env.a.Login(ctx, "u@example.test", "pw-123456789", "", "x"); !errors.Is(err, auth.ErrTOTPRequired) {
		t.Fatalf("missing TOTP = %v", err)
	}
	if _, err := env.a.Login(ctx, "u@example.test", "pw-123456789", recovery[0], "x"); err != nil {
		t.Fatalf("recovery code rejected: %v", err)
	}
	if _, err := env.a.Login(ctx, "u@example.test", "pw-123456789", recovery[0], "x"); !errors.Is(err, auth.ErrInvalid) {
		t.Fatalf("recovery code reused: %v", err)
	}
}

func TestSetupOnlyOnce(t *testing.T) {
	env := newAuth(t)
	setupURL, ok := env.a.SetupURL(ctx)
	if !ok {
		t.Fatal("setup URL missing on empty install")
	}
	uid, oid, err := env.a.CompleteSetup(ctx, tokenFrom(t, setupURL), "o@example.test", "Owner", "pw-123456789", "Acme")
	if err != nil || uid == 0 || oid == 0 {
		t.Fatalf("CompleteSetup() = %d, %d, %v", uid, oid, err)
	}
	var admin bool
	if err := env.store.Pool.QueryRow(ctx, `select is_admin from users where id=$1`, uid).Scan(&admin); err != nil || !admin {
		t.Fatalf("setup user is_admin=%t err=%v, want an instance admin", admin, err)
	}
	if _, ok := env.a.SetupURL(ctx); ok {
		t.Fatal("setup still open")
	}
	if _, _, err := env.a.CompleteSetup(ctx, tokenFrom(t, setupURL), "e@example.test", "Evil", "pw-123456789", "Evil"); !errors.Is(err, auth.ErrInvalid) {
		t.Fatalf("setup token reusable: %v", err)
	}
	var role string
	if err := env.store.Pool.QueryRow(ctx, `select role from memberships where org_id=$1 and user_id=$2`, oid, uid).Scan(&role); err != nil || role != "owner" {
		t.Fatalf("owner membership = %q, %v", role, err)
	}
}

func TestSetupRejectsWeakPassword(t *testing.T) {
	env := newAuth(t)
	setupURL, ok := env.a.SetupURL(ctx)
	if !ok {
		t.Fatal("setup URL missing")
	}
	token := tokenFrom(t, setupURL)
	if _, _, err := env.a.CompleteSetup(ctx, token, "o@example.test", "Owner", "short", "Acme"); !errors.Is(err, platformcrypto.ErrPasswordTooShort) {
		t.Fatalf("CompleteSetup() five-character password error = %v, want ErrPasswordTooShort", err)
	}
	if _, _, err := env.a.CompleteSetup(ctx, token, "o@example.test", "Owner", "sixsix", "Acme"); err != nil {
		t.Fatalf("CompleteSetup() six-character password error = %v", err)
	}
}

func TestSignupPasswordLengthBoundary(t *testing.T) {
	env := newAuth(t)
	if _, err := env.a.Signup(auth.WithClientKey(ctx, "short-signup"), "short@example.test", "Short", "short", "Short org", ""); !errors.Is(err, platformcrypto.ErrPasswordTooShort) {
		t.Fatalf("Signup() five-character password error = %v, want ErrPasswordTooShort", err)
	}
	if _, err := env.a.Signup(auth.WithClientKey(ctx, "six-signup"), "six@example.test", "Six", "sixsix", "Six org", ""); err != nil {
		t.Fatalf("Signup() six-character password error = %v", err)
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
		token, _ = strings.CutPrefix(u.Path, "/reset/")
	}
	if token == "" {
		t.Fatalf("URL has no token: %q", rawURL)
	}
	return token
}

func addOwnerMembership(t *testing.T, env authEnv, userID int64) int64 {
	t.Helper()
	var orgID int64
	if err := env.store.Pool.QueryRow(ctx, `insert into orgs(name,slug) values ('Test','test') returning id`).Scan(&orgID); err != nil {
		t.Fatal(err)
	}
	if _, err := env.store.Pool.Exec(ctx, `insert into memberships(org_id,user_id,role) values ($1,$2,'owner')`, orgID, userID); err != nil {
		t.Fatal(err)
	}
	return orgID
}

func waitFor(t *testing.T, condition func() (bool, error)) {
	t.Helper()
	deadline := time.Now().Add(2 * time.Second)
	for {
		ok, err := condition()
		if err != nil {
			t.Fatal(err)
		}
		if ok {
			return
		}
		if time.Now().After(deadline) {
			t.Fatal("timed out waiting for asynchronous audit")
		}
		time.Sleep(time.Millisecond)
	}
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

func TestFinishResetSignInIssuesSessionAndEndsOldOnes(t *testing.T) {
	env := newAuth(t)
	uid := createUser(t, env, "u@example.test", "old-password-1")
	oldSession, err := env.a.Login(ctx, "u@example.test", "old-password-1", "", "old")
	if err != nil {
		t.Fatal(err)
	}
	link, err := env.a.StartReset(ctx, "u@example.test")
	if err != nil {
		t.Fatal(err)
	}
	token := tokenFrom(t, link)
	session, err := env.a.FinishResetSignIn(ctx, token, "new-password-1", "browser")
	if err != nil {
		t.Fatal(err)
	}
	if session == "" {
		t.Fatal("no session issued for a user without a second factor")
	}
	if s, ok := env.a.Session(ctx, session); !ok || s.UserID != uid {
		t.Fatalf("new session = %+v, ok %v", s, ok)
	}
	if _, ok := env.a.Session(ctx, oldSession); ok {
		t.Fatal("a session from before the reset survived")
	}
	if _, err := env.a.Login(ctx, "u@example.test", "new-password-1", "", "x"); err != nil {
		t.Fatalf("new password rejected: %v", err)
	}
	if _, err := env.a.FinishResetSignIn(ctx, token, "again-password", "browser"); !errors.Is(err, auth.ErrInvalid) {
		t.Fatalf("reset token reused: %v", err)
	}
}

func TestFinishResetSignInSkipsUsersWithASecondFactor(t *testing.T) {
	env := newAccountEnv(t)
	uid, _, _ := enrollTestTOTP(t, env, "totp@example.test")
	if _, err := env.a.StartReset(ctx, "totp@example.test"); err != nil {
		t.Fatal(err)
	}
	text := env.mailer.Sent[len(env.mailer.Sent)-1].Text
	var resetLink string
	for _, field := range strings.Fields(text) {
		if strings.Contains(field, "/reset/") {
			resetLink = field
		}
	}
	token := tokenFrom(t, resetLink)
	session, err := env.a.FinishResetSignIn(ctx, token, "new-password-1", "browser")
	if err != nil {
		t.Fatal(err)
	}
	if session != "" {
		t.Fatal("a session was issued without the second factor")
	}
	sessions, err := env.a.Sessions(ctx, uid)
	if err != nil || len(sessions) != 0 {
		t.Fatalf("sessions after reset = %v, %v", sessions, err)
	}
	if _, err := env.a.Login(ctx, "totp@example.test", "new-password-1", "", "x"); !errors.Is(err, auth.ErrTOTPRequired) {
		t.Fatalf("password changed but login did not ask for the code: %v", err)
	}
}

func TestFinishResetSignInRejectsWeakPassword(t *testing.T) {
	env := newAuth(t)
	createUser(t, env, "u@example.test", "old-password-1")
	link, err := env.a.StartReset(ctx, "u@example.test")
	if err != nil {
		t.Fatal(err)
	}
	if _, err := env.a.FinishResetSignIn(ctx, tokenFrom(t, link), "short", "browser"); !errors.Is(err, platformcrypto.ErrPasswordTooShort) {
		t.Fatalf("error = %v, want ErrPasswordTooShort", err)
	}
}
