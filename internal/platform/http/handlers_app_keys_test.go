package platformhttp_test

import (
	"context"
	"encoding/json"
	"fmt"
	"net/http"
	"net/http/httptest"
	"net/netip"
	"strings"
	"testing"
	"time"

	"supermetry/internal/appingest"
	"supermetry/internal/platform/appkeys"
	"supermetry/internal/sentryingest"
)

func newAppKeyFixture(t *testing.T) linkFixture {
	t.Helper()
	f := newLinkFixture(t)
	if _, err := f.pool.Exec(context.Background(), `update sites set kind='app' where id=$1`, f.siteID); err != nil {
		t.Fatal(err)
	}
	if err := f.registry.Invalidate(); err != nil {
		t.Fatal(err)
	}
	return f
}

func TestAppIngestKeyIssueAndList(t *testing.T) {
	f := newAppKeyFixture(t)
	path := fmt.Sprintf("/api/private/sites/%d/ingest-keys", f.siteID)
	res := f.request(t, "owner", http.MethodPost, path, ``)
	if res.Code != http.StatusCreated {
		t.Fatalf("issue = %d %s", res.Code, res.Body)
	}
	var issued struct {
		Key      string  `json:"key"`
		Prefix   string  `json:"prefix"`
		Previous *string `json:"previous_expires_at"`
	}
	if err := json.Unmarshal(res.Body.Bytes(), &issued); err != nil {
		t.Fatal(err)
	}
	if !appkeys.Valid(issued.Key) || issued.Prefix != issued.Key[4:12] || issued.Previous != nil {
		t.Fatalf("issued = %+v", issued)
	}
	site, ok := f.registry.Site(f.siteID)
	if !ok || !site.App.KeyValid(appkeys.Hash(issued.Key), f.now) {
		t.Fatal("registry did not pick up the new key")
	}
	list := f.request(t, "owner", http.MethodGet, path, ``)
	if list.Code != http.StatusOK || strings.Contains(list.Body.String(), issued.Key) || !strings.Contains(list.Body.String(), issued.Prefix) {
		t.Fatalf("list = %d %s", list.Code, list.Body)
	}
	second := f.request(t, "owner", http.MethodPost, path, ``)
	if second.Code != http.StatusCreated || !strings.Contains(second.Body.String(), `"previous_expires_at":"`) {
		t.Fatalf("rotate = %d %s", second.Code, second.Body)
	}
	var audits int
	if err := f.pool.QueryRow(context.Background(), `select count(*) from audit_log where action='site.ingest_key_rotated'`).Scan(&audits); err != nil || audits != 2 {
		t.Fatalf("audit rows = %d err=%v", audits, err)
	}
}

func TestAppIngestKeyRoutesGuardKindAndRole(t *testing.T) {
	f := newAppKeyFixture(t)
	path := fmt.Sprintf("/api/private/sites/%d/ingest-keys", f.siteID)
	for _, actor := range []string{"viewer", "editor"} {
		if res := f.request(t, actor, http.MethodPost, path, ``); res.Code != http.StatusForbidden {
			t.Fatalf("%s issue = %d", actor, res.Code)
		}
		if res := f.request(t, actor, http.MethodGet, path, ``); res.Code != http.StatusForbidden {
			t.Fatalf("%s list = %d", actor, res.Code)
		}
	}
	if _, err := f.pool.Exec(context.Background(), `update sites set kind='web' where id=$1`, f.siteID); err != nil {
		t.Fatal(err)
	}
	if err := f.registry.Invalidate(); err != nil {
		t.Fatal(err)
	}
	if res := f.request(t, "owner", http.MethodPost, path, ``); res.Code != http.StatusNotFound {
		t.Fatalf("web site issue = %d", res.Code)
	}
	if res := f.request(t, "owner", http.MethodGet, path, ``); res.Code != http.StatusNotFound {
		t.Fatalf("web site list = %d", res.Code)
	}
}

func TestAppDSNKeyLifecycle(t *testing.T) {
	f := newAppKeyFixture(t)
	path := fmt.Sprintf("/api/private/sites/%d/app/dsn-keys", f.siteID)
	for _, actor := range []string{"viewer", "editor"} {
		if res := f.request(t, actor, http.MethodPost, path, ``); res.Code != http.StatusForbidden {
			t.Fatalf("%s create = %d", actor, res.Code)
		}
	}
	if res := f.request(t, "owner", http.MethodPost, path, `{"allowed_origins":["https://x.example.test/path"]}`); res.Code != http.StatusBadRequest {
		t.Fatalf("bad origin = %d", res.Code)
	}
	res := f.request(t, "owner", http.MethodPost, path, `{"label":"web","allowed_origins":["HTTPS://App.Example.test"]}`)
	if res.Code != http.StatusCreated {
		t.Fatalf("create = %d %s", res.Code, res.Body)
	}
	var created struct {
		PublicKey      string   `json:"public_key"`
		Prefix         string   `json:"prefix"`
		DSN            string   `json:"dsn"`
		AllowedOrigins []string `json:"allowed_origins"`
	}
	if err := json.Unmarshal(res.Body.Bytes(), &created); err != nil {
		t.Fatal(err)
	}
	if !appkeys.ValidDSNKey(created.PublicKey) || created.Prefix != created.PublicKey[:8] ||
		len(created.AllowedOrigins) != 1 || created.AllowedOrigins[0] != "https://app.example.test" {
		t.Fatalf("created = %+v", created)
	}
	if want := fmt.Sprintf("https://%s@sm.example.test/%d", created.PublicKey, f.siteID); created.DSN != want {
		t.Fatalf("dsn = %q, want %q", created.DSN, want)
	}
	site, key, ok := f.registry.ByDSNKey(created.PublicKey)
	if !ok || site.ID != f.siteID || len(key.AllowedOrigins) != 1 {
		t.Fatal("registry did not pick up the DSN key")
	}
	// A native key rotation must leave the DSN key alone.
	if res := f.request(t, "owner", http.MethodPost, fmt.Sprintf("/api/private/sites/%d/ingest-keys", f.siteID), ``); res.Code != http.StatusCreated {
		t.Fatalf("rotate = %d", res.Code)
	}
	if res := f.request(t, "owner", http.MethodPost, fmt.Sprintf("/api/private/sites/%d/ingest-keys", f.siteID), ``); res.Code != http.StatusCreated {
		t.Fatalf("rotate = %d", res.Code)
	}
	if _, _, ok := f.registry.ByDSNKey(created.PublicKey); !ok {
		t.Fatal("native key rotation affected the DSN key")
	}
	if list := f.request(t, "owner", http.MethodGet, fmt.Sprintf("/api/private/sites/%d/ingest-keys", f.siteID), ``); strings.Contains(list.Body.String(), created.Prefix) {
		t.Fatalf("native list shows a DSN key: %s", list.Body)
	}
	list := f.request(t, "owner", http.MethodGet, path, ``)
	if list.Code != http.StatusOK || !strings.Contains(list.Body.String(), created.PublicKey) {
		t.Fatalf("list = %d %s", list.Code, list.Body)
	}
	if res := f.request(t, "owner", http.MethodDelete, path+"/"+created.Prefix, ``); res.Code != http.StatusNoContent {
		t.Fatalf("revoke = %d %s", res.Code, res.Body)
	}
	if res := f.request(t, "owner", http.MethodDelete, path+"/"+created.Prefix, ``); res.Code != http.StatusNotFound {
		t.Fatalf("second revoke = %d", res.Code)
	}
	if _, _, ok := f.registry.ByDSNKey(created.PublicKey); ok {
		t.Fatal("revoked key still resolves")
	}
	var audits int
	if err := f.pool.QueryRow(context.Background(), `select count(*) from audit_log where action in ('site.dsn_key_created','site.dsn_key_revoked')`).Scan(&audits); err != nil || audits != 2 {
		t.Fatalf("audit rows = %d err=%v", audits, err)
	}
}

type dsnTestBodies struct{}

func (dsnTestBodies) Enqueue(appingest.Body) bool { return true }

type dsnTestGeo struct{}

func (dsnTestGeo) Country(netip.Addr) string { return "DE" }

func TestAppDSNKeyUpdate(t *testing.T) {
	f := newAppKeyFixture(t)
	path := fmt.Sprintf("/api/private/sites/%d/app/dsn-keys", f.siteID)
	create := f.request(t, "owner", http.MethodPost, path, `{"label":"web","allowed_origins":["https://old.example.test"]}`)
	if create.Code != http.StatusCreated {
		t.Fatalf("create = %d %s", create.Code, create.Body)
	}
	var created struct {
		PublicKey string `json:"public_key"`
		Prefix    string `json:"prefix"`
	}
	if err := json.Unmarshal(create.Body.Bytes(), &created); err != nil {
		t.Fatal(err)
	}
	patch := path + "/" + created.Prefix
	for _, actor := range []string{"viewer", "editor"} {
		if res := f.request(t, actor, http.MethodPatch, patch, `{"label":"x"}`); res.Code != http.StatusForbidden {
			t.Fatalf("%s update = %d", actor, res.Code)
		}
	}
	for name, body := range map[string]string{
		"bad origin": `{"allowed_origins":["https://x.example.test/path"]}`,
		"bad json":   `{`,
		"empty body": `{}`,
		"long label": `{"label":"` + strings.Repeat("a", 81) + `"}`,
	} {
		if res := f.request(t, "owner", http.MethodPatch, patch, body); res.Code != http.StatusBadRequest {
			t.Fatalf("%s = %d %s", name, res.Code, res.Body)
		}
	}
	if res := f.request(t, "owner", http.MethodPatch, path+"/short", `{"label":"x"}`); res.Code != http.StatusNotFound {
		t.Fatalf("short prefix = %d", res.Code)
	}

	// Ingest enforces the origins held by the registry, so check it end to end.
	ingest := sentryingest.NewHandler(f.registry, appingest.NewIntake(appingest.Config{}), dsnTestBodies{}, dsnTestGeo{}, nil, func() time.Time { return f.now }).WithIPLimit(-1)
	send := func(origin string) int {
		body := `{"event_id":"11111111111111111111111111111111"}` + "\n" + `{"type":"event"}` + "\n" + `{"message":"hi"}` + "\n"
		r := httptest.NewRequest(http.MethodPost, fmt.Sprintf("/api/%d/envelope/?sentry_version=7&sentry_key=%s", f.siteID, created.PublicKey), strings.NewReader(body))
		r.Header.Set("Origin", origin)
		w := httptest.NewRecorder()
		ingest.ServeHTTP(w, r)
		return w.Code
	}
	if send("https://old.example.test") != http.StatusOK || send("https://new.example.test") != http.StatusForbidden {
		t.Fatal("before update: old origin must pass, new origin must be refused")
	}

	res := f.request(t, "owner", http.MethodPatch, patch, `{"allowed_origins":["HTTPS://New.Example.test"]}`)
	if res.Code != http.StatusOK {
		t.Fatalf("update = %d %s", res.Code, res.Body)
	}
	var updated struct {
		Label          string   `json:"label"`
		AllowedOrigins []string `json:"allowed_origins"`
	}
	if err := json.Unmarshal(res.Body.Bytes(), &updated); err != nil || updated.Label != "web" || len(updated.AllowedOrigins) != 1 || updated.AllowedOrigins[0] != "https://new.example.test" {
		t.Fatalf("updated = %+v err=%v (label must be unchanged)", updated, err)
	}
	if list := f.request(t, "owner", http.MethodGet, path, ``); !strings.Contains(list.Body.String(), "https://new.example.test") || strings.Contains(list.Body.String(), "old.example.test") {
		t.Fatalf("list = %s", list.Body)
	}
	if send("https://new.example.test") != http.StatusOK || send("https://old.example.test") != http.StatusForbidden {
		t.Fatal("after update: new origin must pass and old origin be refused")
	}

	// Clearing the list allows any origin; the label can change on its own.
	res = f.request(t, "owner", http.MethodPatch, patch, `{"allowed_origins":[],"label":" renamed "}`)
	if res.Code != http.StatusOK || !strings.Contains(res.Body.String(), `"allowed_origins":[]`) || !strings.Contains(res.Body.String(), `"label":"renamed"`) {
		t.Fatalf("clear = %d %s", res.Code, res.Body)
	}
	if send("https://anything.example.test") != http.StatusOK {
		t.Fatal("cleared key must accept any origin")
	}
	if res = f.request(t, "owner", http.MethodPatch, patch, `{"label":"only label"}`); res.Code != http.StatusOK {
		t.Fatalf("label only = %d", res.Code)
	}

	// Another site's prefix is not found.
	if _, err := f.pool.Exec(context.Background(), `update sites set kind='app' where id=$1`, f.otherSite); err != nil {
		t.Fatal(err)
	}
	if err := f.registry.Invalidate(); err != nil {
		t.Fatal(err)
	}
	if res := f.request(t, "owner", http.MethodPatch, fmt.Sprintf("/api/private/sites/%d/app/dsn-keys/%s", f.otherSite, created.Prefix), `{"label":"x"}`); res.Code != http.StatusNotFound {
		t.Fatalf("other site's key = %d %s", res.Code, res.Body)
	}

	var audits int
	if err := f.pool.QueryRow(context.Background(), `select count(*) from audit_log where action='site.dsn_key_updated'`).Scan(&audits); err != nil || audits != 3 {
		t.Fatalf("audit rows = %d err=%v", audits, err)
	}

	// Revoked keys cannot be edited.
	if res := f.request(t, "owner", http.MethodDelete, patch, ``); res.Code != http.StatusNoContent {
		t.Fatalf("revoke = %d", res.Code)
	}
	if res := f.request(t, "owner", http.MethodPatch, patch, `{"label":"x"}`); res.Code != http.StatusNotFound {
		t.Fatalf("revoked update = %d", res.Code)
	}
}
