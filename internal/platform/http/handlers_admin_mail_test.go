package platformhttp_test

import (
	"bufio"
	"context"
	"encoding/json"
	"fmt"
	"net"
	"net/http"
	"net/http/httptest"
	"strings"
	"sync"
	"testing"

	"supermetry/internal/config"
	"supermetry/internal/db"
	"supermetry/internal/platform/admin"
	"supermetry/internal/platform/authz"
	platformcrypto "supermetry/internal/platform/crypto"
	platformhttp "supermetry/internal/platform/http"
	"supermetry/internal/platform/mail"
	"supermetry/internal/platform/store"
	"supermetry/internal/testpg"
)

// plainSMTP accepts one message per connection and records the DATA bodies.
func plainSMTP(t *testing.T) (port int, bodies func() []string) {
	t.Helper()
	listener, err := net.Listen("tcp", "127.0.0.1:0")
	if err != nil {
		t.Fatal(err)
	}
	t.Cleanup(func() { _ = listener.Close() })
	var mu sync.Mutex
	var received []string
	go func() {
		for {
			conn, err := listener.Accept()
			if err != nil {
				return
			}
			go func() {
				defer conn.Close()
				reader := bufio.NewReader(conn)
				write := func(s string) { fmt.Fprintf(conn, "%s\r\n", s) }
				write("220 fake")
				for {
					line, err := reader.ReadString('\n')
					if err != nil {
						return
					}
					cmd := strings.ToUpper(strings.TrimSpace(line))
					switch {
					case strings.HasPrefix(cmd, "EHLO"):
						write("250 fake")
					case strings.HasPrefix(cmd, "MAIL"), strings.HasPrefix(cmd, "RCPT"):
						write("250 ok")
					case cmd == "DATA":
						write("354 go")
						var body strings.Builder
						for {
							part, err := reader.ReadString('\n')
							if err != nil {
								return
							}
							if part == ".\r\n" {
								break
							}
							body.WriteString(part)
						}
						mu.Lock()
						received = append(received, body.String())
						mu.Unlock()
						write("250 queued")
					case cmd == "QUIT":
						write("221 bye")
						return
					default:
						write("502 no")
					}
				}
			}()
		}
	}()
	return listener.Addr().(*net.TCPAddr).Port, func() []string {
		mu.Lock()
		defer mu.Unlock()
		return append([]string(nil), received...)
	}
}

func newMailHandler(t *testing.T, env config.SMTP, managed bool) (http.Handler, func() string) {
	t.Helper()
	pool, _ := testpg.New(t)
	ctx := context.Background()
	if err := db.Migrate(ctx, pool); err != nil {
		t.Fatal(err)
	}
	var adminID, memberID int64
	if err := pool.QueryRow(ctx, `insert into users(email,name,password_hash,is_admin) values ('admin@example.test','Admin','hash',true) returning id`).Scan(&adminID); err != nil {
		t.Fatal(err)
	}
	if err := pool.QueryRow(ctx, `insert into users(email,name,password_hash) values ('member@example.test','Member','hash') returning id`).Scan(&memberID); err != nil {
		t.Fatal(err)
	}
	data := &store.Store{Pool: pool}
	box, err := platformcrypto.NewSecretBox([]byte("0123456789abcdef0123456789abcdef"), mail.SMTPSecretInfo)
	if err != nil {
		t.Fatal(err)
	}
	manager := mail.NewManager(env, store.SMTPSettings{Data: data}, box)
	handler := platformhttp.New(platformhttp.Deps{
		Authenticator: actorAuthenticator{
			"admin":  {UserID: adminID, SiteIDs: []int32{}},
			"member": {UserID: memberID, Role: authz.RoleViewer},
		},
		Store: data, Admin: admin.New(data), Signup: "invite", Mail: manager, Managed: managed,
	})
	return handler, func() string {
		var raw string
		if err := pool.QueryRow(ctx, `select coalesce(settings->'smtp','{}')::text from instance_settings where id`).Scan(&raw); err != nil {
			t.Fatal(err)
		}
		return raw
	}
}

func emailRequest(handler http.Handler, actor, method, path, body string) *httptest.ResponseRecorder {
	request := httptest.NewRequest(method, path, strings.NewReader(body))
	request.Header.Set("Content-Type", "application/json")
	request.Header.Set("X-Test-Actor", actor)
	if method != http.MethodGet {
		request.Header.Set("X-CSRF-Token", "token")
		request.AddCookie(&http.Cookie{Name: "supermetry_csrf", Value: "token"})
	}
	response := httptest.NewRecorder()
	handler.ServeHTTP(response, request)
	return response
}

func TestAdminEmailSettingsRoundTripHidesPassword(t *testing.T) {
	handler, stored := newMailHandler(t, config.SMTP{}, false)
	response := emailRequest(handler, "admin", http.MethodGet, "/api/private/admin/email", "")
	if response.Code != 200 || !strings.Contains(response.Body.String(), `"source":"none"`) {
		t.Fatalf("get: %d %s", response.Code, response.Body.String())
	}
	body := `{"host":"smtp.example.test","port":587,"tls":"starttls","user":"mailer","password":"s3cret-value","from":"Supermetry <supermetry@example.com>","helo":""}`
	response = emailRequest(handler, "admin", http.MethodPut, "/api/private/admin/email", body)
	if response.Code != 200 {
		t.Fatalf("put: %d %s", response.Code, response.Body.String())
	}
	if strings.Contains(response.Body.String(), "s3cret-value") || strings.Contains(stored(), "s3cret-value") {
		t.Fatal("password leaked into the response or the database in clear")
	}
	var got map[string]any
	if err := json.Unmarshal(response.Body.Bytes(), &got); err != nil {
		t.Fatal(err)
	}
	if got["source"] != "database" || got["password_set"] != true || got["host"] != "smtp.example.test" || got["user"] != "mailer" {
		t.Fatalf("response = %v", got)
	}
	if _, has := got["password"]; has {
		t.Fatal("password field in response")
	}
	// An empty password keeps the stored one.
	body = `{"host":"smtp2.example.test","port":587,"tls":"starttls","user":"mailer","password":"","from":"supermetry@example.com"}`
	response = emailRequest(handler, "admin", http.MethodPut, "/api/private/admin/email", body)
	if response.Code != 200 || !strings.Contains(response.Body.String(), `"password_set":true`) {
		t.Fatalf("keep: %d %s", response.Code, response.Body.String())
	}
	response = emailRequest(handler, "admin", http.MethodPut, "/api/private/admin/email", `{"host":"smtp2.example.test","port":587,"tls":"starttls","user":"mailer","clear_password":true,"from":"supermetry@example.com"}`)
	if response.Code != 200 || !strings.Contains(response.Body.String(), `"password_set":false`) {
		t.Fatalf("clear: %d %s", response.Code, response.Body.String())
	}
	response = emailRequest(handler, "admin", http.MethodDelete, "/api/private/admin/email", "")
	if response.Code != 200 || !strings.Contains(response.Body.String(), `"source":"none"`) {
		t.Fatalf("delete: %d %s", response.Code, response.Body.String())
	}
}

func TestAdminEmailRejectsInvalidSettings(t *testing.T) {
	handler, _ := newMailHandler(t, config.SMTP{}, false)
	for _, body := range []string{
		`{"host":"","port":587,"tls":"starttls","from":"a@example.com"}`,
		`{"host":"h.example.test","port":0,"tls":"starttls","from":"a@example.com"}`,
		`{"host":"h.example.test","port":587,"tls":"bogus","from":"a@example.com"}`,
		`{"host":"h.example.test","port":587,"tls":"none","user":"u","from":"a@example.com"}`,
		`{"host":"h.example.test","port":587,"tls":"starttls","from":"not an address"}`,
	} {
		if response := emailRequest(handler, "admin", http.MethodPut, "/api/private/admin/email", body); response.Code != http.StatusBadRequest {
			t.Fatalf("%s: status %d %s", body, response.Code, response.Body.String())
		}
	}
}

func TestAdminEmailForbiddenForNonAdminAndRefusedWhenManaged(t *testing.T) {
	handler, _ := newMailHandler(t, config.SMTP{}, false)
	for _, route := range [][2]string{{http.MethodGet, "/api/private/admin/email"}, {http.MethodPut, "/api/private/admin/email"}, {http.MethodDelete, "/api/private/admin/email"}, {http.MethodPost, "/api/private/admin/email/test"}} {
		if response := emailRequest(handler, "member", route[0], route[1], `{}`); response.Code != http.StatusForbidden {
			t.Fatalf("%v member: %d", route, response.Code)
		}
		if response := emailRequest(handler, "", route[0], route[1], `{}`); response.Code != http.StatusUnauthorized {
			t.Fatalf("%v anonymous: %d", route, response.Code)
		}
	}
	managed, _ := newMailHandler(t, config.SMTP{}, true)
	for _, route := range [][2]string{{http.MethodGet, "/api/private/admin/email"}, {http.MethodPut, "/api/private/admin/email"}, {http.MethodPost, "/api/private/admin/email/test"}} {
		if response := emailRequest(managed, "admin", route[0], route[1], `{}`); response.Code != http.StatusConflict {
			t.Fatalf("%v managed: %d %s", route, response.Code, response.Body.String())
		}
	}
}

func TestAdminEmailEnvironmentWinsAndIsReadOnly(t *testing.T) {
	env := config.SMTP{Host: "env.example.test", Port: 465, ImplicitTLS: true, User: "envuser", Pass: "envpass", From: "env@example.com"}
	handler, _ := newMailHandler(t, env, false)
	response := emailRequest(handler, "admin", http.MethodGet, "/api/private/admin/email", "")
	body := response.Body.String()
	if response.Code != 200 || !strings.Contains(body, `"source":"environment"`) || !strings.Contains(body, `"tls":"implicit"`) || !strings.Contains(body, `"password_set":true`) || strings.Contains(body, "envpass") {
		t.Fatalf("get: %d %s", response.Code, body)
	}
	response = emailRequest(handler, "admin", http.MethodPut, "/api/private/admin/email", `{"host":"x.example.test","port":587,"tls":"starttls","from":"a@example.com"}`)
	if response.Code != http.StatusConflict {
		t.Fatalf("put: %d %s", response.Code, response.Body.String())
	}
}

func TestAdminEmailTestSendsAndReportsFailures(t *testing.T) {
	port, bodies := plainSMTP(t)
	handler, _ := newMailHandler(t, config.SMTP{}, false)
	save := fmt.Sprintf(`{"host":"127.0.0.1","port":%d,"tls":"none","from":"Supermetry <supermetry@example.com>"}`, port)
	if response := emailRequest(handler, "admin", http.MethodPut, "/api/private/admin/email", save); response.Code != 200 {
		t.Fatalf("save: %d %s", response.Code, response.Body.String())
	}
	// Default recipient is the calling admin.
	response := emailRequest(handler, "admin", http.MethodPost, "/api/private/admin/email/test", "")
	if response.Code != 200 || !strings.Contains(response.Body.String(), `"ok":true`) || !strings.Contains(response.Body.String(), "admin@example.test") || !strings.Contains(response.Body.String(), "inbox") {
		t.Fatalf("test: %d %s", response.Code, response.Body.String())
	}
	if got := bodies(); len(got) != 1 || !strings.Contains(got[0], "Subject: Supermetry test email") {
		t.Fatalf("bodies = %q", got)
	}
	response = emailRequest(handler, "admin", http.MethodPost, "/api/private/admin/email/test", `{"to":"someone@example.test"}`)
	if !strings.Contains(response.Body.String(), "someone@example.test") || response.Code != 200 {
		t.Fatalf("custom: %d %s", response.Code, response.Body.String())
	}
	if response := emailRequest(handler, "admin", http.MethodPost, "/api/private/admin/email/test", `{"to":"not-an-address"}`); response.Code != http.StatusBadRequest {
		t.Fatalf("bad recipient: %d", response.Code)
	}
	// A dead port reports the failing step in the body, not as an HTTP error.
	dead := `{"host":"127.0.0.1","port":1,"tls":"none","from":"supermetry@example.com"}`
	if response := emailRequest(handler, "admin", http.MethodPut, "/api/private/admin/email", dead); response.Code != 200 {
		t.Fatalf("save dead: %d", response.Code)
	}
	response = emailRequest(handler, "admin", http.MethodPost, "/api/private/admin/email/test", "")
	if response.Code != 200 || !strings.Contains(response.Body.String(), `"ok":false`) || !strings.Contains(response.Body.String(), "connect SMTP") {
		t.Fatalf("failure: %d %s", response.Code, response.Body.String())
	}
}

func TestAdminEmailTestIsRateLimited(t *testing.T) {
	handler, _ := newMailHandler(t, config.SMTP{}, false)
	var last int
	for i := 0; i < 6; i++ {
		last = emailRequest(handler, "admin", http.MethodPost, "/api/private/admin/email/test", "").Code
	}
	if last != http.StatusTooManyRequests {
		t.Fatalf("sixth test status = %d, want 429", last)
	}
}
