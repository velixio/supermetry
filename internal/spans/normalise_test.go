package spans

import (
	"strings"
	"testing"
	"time"
)

func TestParameteriseSQL(t *testing.T) {
	for name, c := range map[string]struct{ in, want string }{
		"literals":      {"select * from users where email = 'a@b.c' and id = 42", "select * from users where email = ? and id = ?"},
		"escaped quote": {"select 1 from t where n = 'it''s' and x=3.5e2", "select ? from t where n = ? and x=?"},
		"in list":       {"SELECT a FROM t WHERE id IN (1, 2, 3) AND b = 'x'", "SELECT a FROM t WHERE id IN (?) AND b = ?"},
		"identifiers":   {`select t1.col2, "Col 3" from t1 where a = $1`, `select t1.col2, "Col 3" from t1 where a = $1`},
		"whitespace":    {"select  *\n from\tt  where a = 1", "select * from t where a = ?"},
		"comments":      {"select 1 -- secret 'x@y.z'\n from t /* 42 */ where a=7", "select ? from t where a=?"},
		"unterminated":  {"select * from t where a = 'abc", "select * from t where a = ?"},
		"dollar":        {"select * from t where a = $$x@y.z$$ and b = 1", "select * from t where a = ? and b = ?"},
		"empty":         {"", ""},
	} {
		if got := ParameteriseSQL(c.in); got != c.want {
			t.Errorf("%s: got %q, want %q", name, got, c.want)
		}
	}
	long := ParameteriseSQL("select " + strings.Repeat("é", 2000))
	if len(long) > 1000 || strings.ContainsRune(long, '�') {
		t.Errorf("long query not clipped on a rune boundary: %d bytes", len(long))
	}
}

func TestParameteriseURL(t *testing.T) {
	for name, c := range map[string]struct{ method, in, want string }{
		"token query":   {"", "GET https://api.example.com/v1/items/12345?token=sk_live_abc#frag", "GET https://api.example.com/v1/items/*"},
		"method arg":    {"POST", "https://user:pw@api.example.com:8443/v1/a/9f8e7d6c5b4a3210", "POST https://api.example.com:8443/v1/a/*"},
		"uuid":          {"", "https://x.io/o/123e4567-e89b-12d3-a456-426614174000/r", "https://x.io/o/*/r"},
		"path only":     {"", "/users/123/orders/9f8e7d6c5b4a3210?x=1", "/users/*/orders/*"},
		"short hex":     {"", "/v1/abc/ff", "/v1/abc/ff"},
		"email segment": {"", "/u/a@b.c/x", "/u/*/x"},
		"no path":       {"GET", "https://example.com", "GET https://example.com"},
	} {
		if got := ParameteriseURL(c.method, c.in); got != c.want {
			t.Errorf("%s: got %q, want %q", name, got, c.want)
		}
	}
}

func TestTransactionName(t *testing.T) {
	if got := TransactionName("/users/123/orders/9f8e7d6c5b4a3210?a=1", "url"); got != "/users/*/orders/*" {
		t.Errorf("url source: %q", got)
	}
	if got := TransactionName("GET /users/{id}", "route"); got != "GET /users/{id}" {
		t.Errorf("route source: %q", got)
	}
	if got := TransactionName(strings.Repeat("a", 500), ""); len(got) != 200 {
		t.Errorf("not clipped: %d", len(got))
	}
}

func TestCleanAttrs(t *testing.T) {
	got := CleanAttrs(map[string]any{
		"http.request.method": "GET", "http.response.status_code": float64(200), "db.system": "postgresql",
		"db.operation.name": "SELECT", "server.address": "db.internal",
		"http.request.header.cookie": "x", "user.email": "a@b.c", "rpc.method": strings.Repeat("m", 500),
	})
	want := map[string]string{"http.method": "GET", "http.status_code": "200", "db.system": "postgresql", "db.operation": "SELECT", "server.address": "db.internal", "rpc.method": strings.Repeat("m", 200)}
	if len(got) != len(want) {
		t.Fatalf("got %v", got)
	}
	for k, v := range want {
		if got[k] != v {
			t.Errorf("%s = %q, want %q", k, got[k], v)
		}
	}
	if got := CleanAttrs(map[string]any{"url.full": "https://api.example.com:8080/a?token=1"}); got["server.address"] != "api.example.com" {
		t.Errorf("url.full host: %v", got)
	}
}

func TestNameCapsOverflow(t *testing.T) {
	c := NewNameCaps(3)
	day := time.Date(2026, 11, 18, 0, 0, 0, 0, time.UTC)
	for _, n := range []string{"a", "b", "c"} {
		if got, over := c.Admit(1, day, n); got != n || over {
			t.Fatalf("%s: %q %v", n, got, over)
		}
	}
	if got, over := c.Admit(1, day, "d"); got != OtherName || !over {
		t.Errorf("overflow: %q %v", got, over)
	}
	if got, _ := c.Admit(1, day, "a"); got != "a" {
		t.Errorf("known name must pass: %q", got)
	}
	if got, _ := c.Admit(2, day, "d"); got != "d" {
		t.Errorf("other site: %q", got)
	}
	if got, _ := c.Admit(1, day.Add(24*time.Hour), "d"); got != "d" {
		t.Errorf("next day resets: %q", got)
	}
}

func FuzzParameteriseSQL(f *testing.F) {
	f.Add("select * from t where a = 'x' and b in (1,2)")
	f.Add("'\"$$/*--")
	f.Fuzz(func(t *testing.T, s string) {
		if got := ParameteriseSQL(s); len(got) > 1000 {
			t.Fatalf("len %d", len(got))
		}
	})
}

func FuzzParameteriseURL(f *testing.F) {
	f.Add("GET", "https://u:p@h/a/123?x=1#f")
	f.Fuzz(func(t *testing.T, m, s string) {
		if got := ParameteriseURL(m, s); len(got) > 1000+16 {
			t.Fatalf("len %d", len(got))
		}
	})
}
