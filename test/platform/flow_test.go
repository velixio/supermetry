package platform_test

import (
	"bytes"
	"context"
	"encoding/base64"
	"encoding/json"
	"fmt"
	"io"
	"net"
	"net/http"
	"net/url"
	"os"
	"os/exec"
	"path/filepath"
	"strconv"
	"strings"
	"sync"
	"testing"
	"time"

	"github.com/jackc/pgx/v5/pgxpool"

	"panel/internal/testpg"
)

const testPassword = "pw-123456789"

type lockedBuffer struct {
	mu sync.Mutex
	b  bytes.Buffer
}

func (b *lockedBuffer) Write(value []byte) (int, error) {
	b.mu.Lock()
	defer b.mu.Unlock()
	return b.b.Write(value)
}

func (b *lockedBuffer) String() string {
	b.mu.Lock()
	defer b.mu.Unlock()
	return b.b.String()
}

type testEnv struct {
	t          *testing.T
	baseURL    string
	pool       *pgxpool.Pool
	binary     string
	processEnv []string
	command    *exec.Cmd
	done       <-chan error
	logs       *lockedBuffer
	stopped    bool
}

type client struct {
	env     *testEnv
	email   string
	orgID   int64
	session *http.Cookie
	csrf    *http.Cookie
}

type site struct {
	ID       int32  `json:"id"`
	PublicID string `json:"public_id"`
}

type summary struct {
	Pageviews int64
}

func TestPlatformFlow(t *testing.T) {
	env := start(t)
	setup := env.setupURL(t)
	owner := env.completeSetup(t, setup, "o@x.io", "Acme")
	site := owner.createSite(t, "Blog", "blog.example.com", "Europe/Berlin")
	env.track(t, site.PublicID, "https://blog.example.com/", 20)
	key := owner.createKey(t, "stats:read")
	if value := env.publicSummary(t, key, site.ID).Pageviews; value != 20 {
		t.Fatalf("pageviews via public API = %d", value)
	}
	invitation := owner.invite(t, "v@x.io", "viewer", []int32{site.ID})
	viewer := env.acceptInvite(t, invitation, "Viewer", testPassword)
	if value := viewer.privateSummary(t, site.ID).Pageviews; value != 20 {
		t.Fatalf("viewer pageviews = %d", value)
	}
	share := owner.share(t, site.ID, "")
	if value := env.shareSummary(t, share).Pageviews; value != 20 {
		t.Fatalf("share pageviews = %d", value)
	}
	if count := owner.auditCount(t); count < 4 {
		t.Fatalf("audit rows = %d", count)
	}
}

func TestUserCLI(t *testing.T) {
	env := prepare(t)
	output := env.runCLI(t, "user", "create", "--email", "c@x.io", "--org", "CLI Org")
	if !strings.Contains(output, "/password-reset?token=") {
		t.Fatalf("create output does not contain a password reset URL: %q", output)
	}
	var memberships int
	if err := env.pool.QueryRow(context.Background(), `
		select count(*) from memberships m
		join users u on u.id=m.user_id
		join orgs o on o.id=m.org_id
		where u.email='c@x.io' and o.name='CLI Org' and m.role='owner'`).Scan(&memberships); err != nil {
		t.Fatal(err)
	}
	if memberships != 1 {
		t.Fatalf("created owner memberships = %d", memberships)
	}
	output = env.runCLI(t, "user", "reset-password", "--email", "c@x.io")
	if !strings.Contains(output, "/password-reset?token=") {
		t.Fatalf("reset output does not contain a password reset URL: %q", output)
	}
}

func TestSitesFileBenchmarkCompatibility(t *testing.T) {
	env := prepare(t)
	sitesPath := filepath.Join(t.TempDir(), "sites.json")
	if err := os.WriteFile(sitesPath, []byte(`[{"id":1,"public_id":"legacy-site","timezone":"UTC","retention_days":30,"allowed_hosts":["legacy.example"]}]`), 0o600); err != nil {
		t.Fatal(err)
	}
	env.processEnv = append(env.processEnv, "PANEL_SITES_FILE="+sitesPath)
	startPrepared(t, env)
	var response struct {
		Totals map[string]float64 `json:"totals"`
	}
	headers := http.Header{"Authorization": []string{"Bearer legacy-key"}}
	env.request(t, http.MethodGet, "/api/public/v1/stats/summary?site_id=1&period=today", nil, headers, http.StatusOK, &response)
}

func TestSitesFileCompatibilityEndsAfterSetup(t *testing.T) {
	env := prepare(t)
	sitesPath := filepath.Join(t.TempDir(), "sites.json")
	if err := os.WriteFile(sitesPath, []byte(`[{"id":1,"public_id":"legacy-site","timezone":"UTC","retention_days":30,"allowed_hosts":["legacy.example"]}]`), 0o600); err != nil {
		t.Fatal(err)
	}
	env.processEnv = append(env.processEnv, "PANEL_SITES_FILE="+sitesPath)
	startPrepared(t, env)
	owner := env.completeSetup(t, env.setupURL(t), "o@x.io", "Acme")
	created := owner.createSite(t, "Blog", "blog.example.com", "UTC")
	env.track(t, created.PublicID, "https://blog.example.com/", 1)
	headers := http.Header{"Authorization": []string{"Bearer legacy-key"}}
	env.request(t, http.MethodGet, "/api/public/v1/stats/summary?site_id=1&period=today", nil, headers, http.StatusUnauthorized, nil)
}

func TestSiteErasureCatchesShutdownFlush(t *testing.T) {
	env := prepare(t)
	env.setProcessEnv("PANEL_FLUSH_EVERY", "1h")
	env.setProcessEnv("PANEL_SESSION_FLUSH", "1h")
	startPrepared(t, env)
	owner := env.completeSetup(t, env.setupURL(t), "o@x.io", "Acme")
	created := owner.createSite(t, "Blog", "blog.example.com", "UTC")
	env.track(t, created.PublicID, "https://blog.example.com/", 1)
	owner.request(t, http.MethodDelete, fmt.Sprintf("/api/private/sites/%d", created.ID), nil, http.StatusNoContent, nil)

	deadline := time.Now().Add(90 * time.Second)
	for {
		var erased bool
		if err := env.pool.QueryRow(context.Background(), `select settings ? 'erased_at' from sites where id=$1`, created.ID).Scan(&erased); err != nil {
			t.Fatal(err)
		}
		if erased {
			break
		}
		if time.Now().After(deadline) {
			t.Fatalf("site erasure job did not run; server logs:\n%s", env.logs.String())
		}
		time.Sleep(100 * time.Millisecond)
	}
	env.stop()
	var count int
	if err := env.pool.QueryRow(context.Background(), `select count(*) from events where site_id=$1`, created.ID).Scan(&count); err != nil {
		t.Fatal(err)
	}
	if count != 0 {
		t.Fatalf("events after shutdown erasure = %d", count)
	}
}

func start(t *testing.T) *testEnv {
	t.Helper()
	env := prepare(t)
	startPrepared(t, env)
	return env
}

func startPrepared(t *testing.T, env *testEnv) {
	t.Helper()
	command := exec.Command(env.binary, "serve")
	command.Env = env.processEnv
	command.Stdout = env.logs
	command.Stderr = env.logs
	if err := command.Start(); err != nil {
		t.Fatal(err)
	}
	env.command = command
	done := make(chan error, 1)
	go func() { done <- command.Wait() }()
	env.done = done
	t.Cleanup(func() { env.stop() })
	env.waitReady(t)
}

func prepare(t *testing.T) *testEnv {
	t.Helper()
	pool, schema := testpg.New(t)
	root := filepath.Clean(filepath.Join("..", ".."))
	binary := filepath.Join(t.TempDir(), "panel")
	build := exec.Command("go", "build", "-race", "-o", binary, "./cmd/panel")
	build.Dir = root
	if output, err := build.CombinedOutput(); err != nil {
		t.Fatalf("build panel: %v\n%s", err, output)
	}

	address := freeAddress(t)
	processEnv := append(os.Environ(),
		"PANEL_DATABASE_URL="+schemaDSN(t, schema),
		"PANEL_LISTEN="+address,
		"PANEL_BASE_URL=http://"+address,
		"PANEL_SECRET_KEY="+base64.StdEncoding.EncodeToString(bytes.Repeat([]byte{7}, 32)),
		"PANEL_ADMIN_API_KEY=legacy-key",
		"PANEL_FLUSH_EVERY=10ms",
		"PANEL_SESSION_FLUSH=10ms",
	)
	logs := &lockedBuffer{}
	return &testEnv{t: t, baseURL: "http://" + address, pool: pool, binary: binary, processEnv: processEnv, logs: logs}
}

func (e *testEnv) runCLI(t *testing.T, args ...string) string {
	t.Helper()
	command := exec.Command(e.binary, args...)
	command.Env = e.processEnv
	output, err := command.CombinedOutput()
	if err != nil {
		t.Fatalf("panel %s: %v: %s", strings.Join(args, " "), err, output)
	}
	return string(output)
}

func (e *testEnv) setProcessEnv(name, value string) {
	prefix := name + "="
	for index := range e.processEnv {
		if strings.HasPrefix(e.processEnv[index], prefix) {
			e.processEnv[index] = prefix + value
			return
		}
	}
	e.processEnv = append(e.processEnv, prefix+value)
}

func (e *testEnv) stop() {
	if e.stopped || e.command == nil || e.command.Process == nil {
		return
	}
	e.stopped = true
	_ = e.command.Process.Signal(os.Interrupt)
	select {
	case <-e.done:
	case <-time.After(10 * time.Second):
		_ = e.command.Process.Kill()
		<-e.done
	}
}

func (e *testEnv) waitReady(t *testing.T) {
	t.Helper()
	deadline := time.Now().Add(10 * time.Minute)
	for time.Now().Before(deadline) {
		select {
		case err := <-e.done:
			e.stopped = true
			t.Fatalf("panel exited before ready: %v\n%s", err, e.logs.String())
		default:
		}
		response, err := http.Get(e.baseURL + "/readyz")
		if err == nil {
			_ = response.Body.Close()
			if response.StatusCode == http.StatusOK {
				return
			}
		}
		time.Sleep(25 * time.Millisecond)
	}
	t.Fatalf("panel did not become ready:\n%s", e.logs.String())
}

func (e *testEnv) setupURL(t *testing.T) string {
	t.Helper()
	deadline := time.Now().Add(5 * time.Second)
	for time.Now().Before(deadline) {
		for _, field := range strings.Fields(e.logs.String()) {
			if strings.HasPrefix(field, e.baseURL+"/setup?token=") {
				return field
			}
		}
		time.Sleep(10 * time.Millisecond)
	}
	t.Fatalf("setup URL was not printed:\n%s", e.logs.String())
	return ""
}

func (e *testEnv) completeSetup(t *testing.T, setupURL, email, org string) *client {
	t.Helper()
	parsed, err := url.Parse(setupURL)
	if err != nil {
		t.Fatal(err)
	}
	var created struct {
		OrgID int64 `json:"org_id"`
	}
	e.request(t, http.MethodPost, "/api/private/setup", map[string]any{
		"token": parsed.Query().Get("token"), "email": email, "name": "Owner",
		"password": testPassword, "org_name": org,
	}, nil, http.StatusCreated, &created)
	return e.login(t, email, testPassword, created.OrgID)
}

func (e *testEnv) login(t *testing.T, email, password string, orgID int64) *client {
	t.Helper()
	response := e.request(t, http.MethodPost, "/api/private/auth/login", map[string]string{
		"email": email, "password": password, "device_label": "integration test",
	}, nil, http.StatusOK, nil)
	result := &client{env: e, email: email, orgID: orgID}
	for _, cookie := range response.Cookies() {
		switch cookie.Name {
		case "panel_session":
			result.session = cookie
		case "panel_csrf":
			result.csrf = cookie
		}
	}
	if result.session == nil || result.csrf == nil {
		t.Fatalf("login did not set session and CSRF cookies")
	}
	return result
}

func (c *client) createSite(t *testing.T, name, host, timezone string) site {
	t.Helper()
	var created site
	c.request(t, http.MethodPost, "/api/private/sites", map[string]any{
		"name": name, "hosts": []string{host}, "timezone": timezone, "retention_days": 30, "revenue_currency": "USD",
	}, http.StatusCreated, &created)
	return created
}

func (e *testEnv) track(t *testing.T, publicID, pageURL string, count int) {
	t.Helper()
	for index := 0; index < count; index++ {
		request, err := http.NewRequest(http.MethodPost, e.baseURL+"/api/public/v1/events", strings.NewReader(fmt.Sprintf(
			`{"site":%q,"name":"pageview","url":%q}`, publicID, pageURL)))
		if err != nil {
			t.Fatal(err)
		}
		request.Header.Set("Content-Type", "text/plain")
		request.Header.Set("User-Agent", "Mozilla/5.0 Chrome/131 Safari/537")
		response, err := http.DefaultClient.Do(request)
		if err != nil {
			t.Fatalf("track event %d: %v", index, err)
		}
		_ = response.Body.Close()
		if response.StatusCode != http.StatusAccepted {
			t.Fatalf("track event %d: status %d", index, response.StatusCode)
		}
	}
}

func (c *client) createKey(t *testing.T, scope string) string {
	t.Helper()
	var created struct {
		Key string `json:"key"`
	}
	c.request(t, http.MethodPost, "/api/private/orgs/"+strconv.FormatInt(c.orgID, 10)+"/api-keys", map[string]any{
		"name": "flow", "scopes": []string{scope},
	}, http.StatusCreated, &created)
	return created.Key
}

func (e *testEnv) publicSummary(t *testing.T, key string, siteID int32) summary {
	t.Helper()
	deadline := time.Now().Add(5 * time.Second)
	for {
		var response struct {
			Totals map[string]float64 `json:"totals"`
		}
		headers := http.Header{"Authorization": []string{"Bearer " + key}}
		e.request(t, http.MethodGet, fmt.Sprintf("/api/public/v1/stats/summary?site_id=%d&period=today", siteID), nil, headers, http.StatusOK, &response)
		result := summary{Pageviews: int64(response.Totals["pageviews"])}
		if result.Pageviews == 20 || time.Now().After(deadline) {
			return result
		}
		time.Sleep(20 * time.Millisecond)
	}
}

func (c *client) invite(t *testing.T, email, role string, siteIDs []int32) string {
	t.Helper()
	var created struct {
		Link string `json:"link"`
	}
	c.request(t, http.MethodPost, "/api/private/orgs/"+strconv.FormatInt(c.orgID, 10)+"/invitations", map[string]any{
		"email": email, "role": role, "site_ids": siteIDs,
	}, http.StatusCreated, &created)
	parsed, err := url.Parse(created.Link)
	if err != nil {
		t.Fatal(err)
	}
	return parsed.Query().Get("token")
}

func (e *testEnv) acceptInvite(t *testing.T, token, name, password string) *client {
	t.Helper()
	var accepted struct {
		UserID int64 `json:"user_id"`
	}
	e.request(t, http.MethodPost, "/api/private/invitations/"+url.PathEscape(token)+"/accept", map[string]string{
		"name": name, "password": password,
	}, nil, http.StatusOK, &accepted)
	var orgID int64
	if err := e.pool.QueryRow(context.Background(), `select org_id from memberships where user_id=$1`, accepted.UserID).Scan(&orgID); err != nil {
		t.Fatal(err)
	}
	return e.login(t, "v@x.io", password, orgID)
}

func (c *client) privateSummary(t *testing.T, siteID int32) summary {
	t.Helper()
	var response struct {
		Totals map[string]float64 `json:"totals"`
	}
	c.request(t, http.MethodGet, fmt.Sprintf("/api/private/stats/summary?site_id=%d&period=today", siteID), nil, http.StatusOK, &response)
	return summary{Pageviews: int64(response.Totals["pageviews"])}
}

func (c *client) share(t *testing.T, siteID int32, password string) string {
	t.Helper()
	var created struct {
		Slug string `json:"slug"`
	}
	c.request(t, http.MethodPost, fmt.Sprintf("/api/private/sites/%d/shares", siteID), map[string]string{"password": password}, http.StatusCreated, &created)
	return created.Slug
}

func (e *testEnv) shareSummary(t *testing.T, slug string) summary {
	t.Helper()
	var response struct {
		Totals map[string]float64 `json:"totals"`
	}
	e.request(t, http.MethodGet, "/api/private/share/"+url.PathEscape(slug)+"/stats/summary?period=today", nil, nil, http.StatusOK, &response)
	return summary{Pageviews: int64(response.Totals["pageviews"])}
}

func (c *client) auditCount(t *testing.T) int {
	t.Helper()
	var response struct {
		Data []json.RawMessage `json:"data"`
	}
	c.request(t, http.MethodGet, "/api/private/orgs/"+strconv.FormatInt(c.orgID, 10)+"/audit?limit=100", nil, http.StatusOK, &response)
	return len(response.Data)
}

func (c *client) request(t *testing.T, method, path string, body any, want int, target any) *http.Response {
	t.Helper()
	headers := make(http.Header)
	headers.Set("X-Panel-Org", strconv.FormatInt(c.orgID, 10))
	headers.Set("X-CSRF-Token", c.csrf.Value)
	return c.env.requestWithCookies(t, method, path, body, headers, want, target, c.session, c.csrf)
}

func (e *testEnv) request(t *testing.T, method, path string, body any, headers http.Header, want int, target any) *http.Response {
	t.Helper()
	return e.requestWithCookies(t, method, path, body, headers, want, target)
}

func (e *testEnv) requestWithCookies(t *testing.T, method, path string, body any, headers http.Header, want int, target any, cookies ...*http.Cookie) *http.Response {
	t.Helper()
	var reader io.Reader
	if body != nil {
		encoded, err := json.Marshal(body)
		if err != nil {
			t.Fatal(err)
		}
		reader = bytes.NewReader(encoded)
	}
	request, err := http.NewRequest(method, e.baseURL+path, reader)
	if err != nil {
		t.Fatal(err)
	}
	request.Header.Set("Content-Type", "application/json")
	for key, values := range headers {
		request.Header[key] = append([]string(nil), values...)
	}
	for _, cookie := range cookies {
		request.AddCookie(cookie)
	}
	response, err := http.DefaultClient.Do(request)
	if err != nil {
		t.Fatal(err)
	}
	payload, err := io.ReadAll(response.Body)
	_ = response.Body.Close()
	if err != nil {
		t.Fatal(err)
	}
	if response.StatusCode != want {
		t.Fatalf("%s %s: status %d, want %d: %s\nlogs:\n%s", method, path, response.StatusCode, want, payload, e.logs.String())
	}
	if target != nil && len(payload) > 0 {
		if err := json.Unmarshal(payload, target); err != nil {
			t.Fatalf("decode %s %s: %v: %s", method, path, err, payload)
		}
	}
	return response
}

func freeAddress(t *testing.T) string {
	t.Helper()
	listener, err := net.Listen("tcp", "127.0.0.1:0")
	if err != nil {
		t.Fatal(err)
	}
	address := listener.Addr().String()
	if err := listener.Close(); err != nil {
		t.Fatal(err)
	}
	return address
}

func schemaDSN(t *testing.T, schema string) string {
	t.Helper()
	parsed, err := url.Parse(os.Getenv("PANEL_TEST_DSN"))
	if err != nil {
		t.Fatal(err)
	}
	values := parsed.Query()
	values.Set("options", "-csearch_path="+schema)
	parsed.RawQuery = values.Encode()
	return parsed.String()
}
