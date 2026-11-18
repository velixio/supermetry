package platformhttp_test

import (
	"context"
	"fmt"
	"net/http"
	"strings"
	"testing"
	"time"

	"supermetry/internal/db"
)

func TestAppAttachmentDownload(t *testing.T) {
	h := newAppHealthFixture(t)
	ctx := context.Background()
	if err := db.EnsureAppEventPartitions(ctx, h.pool, time.Now().AddDate(0, -1, 0), time.Now().AddDate(0, 1, 0)); err != nil {
		t.Fatal(err)
	}
	png := "\x89PNG\r\n\x1a\n" + strings.Repeat("\x00", 16)
	for _, row := range []struct {
		site        int32
		event       string
		idx         int
		name, ctype string
		data        string
	}{
		{h.siteID, "evt-a", 0, "shot.png", "image/png", png},
		{h.siteID, "evt-a", 1, "log.txt", "text/plain", "hello"},
		{h.siteID, "evt-a", 2, "evil.svg", "image/svg+xml", `<svg onload="alert(1)"/>`},
		{h.siteID, "evt-a", 3, "evil.html", "text/html", `<script>alert(1)</script>`},
		{h.siteID, "evt-a", 4, "a\"b;c.json", "application/json", `{"a":1}`},
		{h.otherSite, "evt-foreign", 0, "x.txt", "text/plain", "foreign"},
	} {
		if _, err := h.pool.Exec(ctx, `insert into app_event_attachments(site_id,ts,event_id,idx,name,content_type,size,data) values($1,now(),$2,$3,$4,$5,$6,$7)`,
			row.site, row.event, row.idx, row.name, row.ctype, len(row.data), []byte(row.data)); err != nil {
			t.Fatal(err)
		}
	}
	get := func(actor, event string, idx any) (int, http.Header, string) {
		res := h.request(t, actor, http.MethodGet, fmt.Sprintf("/api/private/sites/%d/app/events/%s/attachments/%v", h.siteID, event, idx), ``)
		return res.Code, res.Header(), res.Body.String()
	}

	for _, actor := range []string{"owner", "editor", "viewer"} {
		code, hdr, body := get(actor, "evt-a", 1)
		if code != http.StatusOK || body != "hello" {
			t.Fatalf("%s = %d %q", actor, code, body)
		}
		if hdr.Get("X-Content-Type-Options") != "nosniff" || !strings.HasPrefix(hdr.Get("Content-Disposition"), "attachment") || hdr.Get("Content-Type") != "text/plain; charset=utf-8" {
			t.Fatalf("text headers = %v", hdr)
		}
	}

	code, hdr, body := get("owner", "evt-a", 0)
	if code != http.StatusOK || body != png || hdr.Get("Content-Type") != "image/png" || !strings.HasPrefix(hdr.Get("Content-Disposition"), "inline") {
		t.Fatalf("png = %d %v", code, hdr)
	}
	for _, want := range []string{"nosniff"} {
		if hdr.Get("X-Content-Type-Options") != want {
			t.Fatalf("png nosniff header = %v", hdr)
		}
	}
	if csp := hdr.Get("Content-Security-Policy"); !strings.Contains(csp, "sandbox") || !strings.Contains(csp, "default-src 'none'") {
		t.Fatalf("csp = %q", csp)
	}
	if cc := hdr.Get("Cache-Control"); !strings.Contains(cc, "private") {
		t.Fatalf("cache-control = %q", cc)
	}

	// Active content is never served under a renderable type, even if it got into the table.
	for _, idx := range []int{2, 3} {
		code, hdr, _ := get("owner", "evt-a", idx)
		if code != http.StatusOK || hdr.Get("Content-Type") != "application/octet-stream" ||
			!strings.HasPrefix(hdr.Get("Content-Disposition"), "attachment") || hdr.Get("X-Content-Type-Options") != "nosniff" {
			t.Fatalf("idx %d: %d %v", idx, code, hdr)
		}
	}
	// A hostile file name cannot break out of the header.
	_, hdr, _ = get("owner", "evt-a", 4)
	if cd := hdr.Get("Content-Disposition"); strings.Contains(cd, "\r") || strings.Contains(cd, "\n") || !strings.HasPrefix(cd, "attachment") || strings.Contains(cd, `a"b`) {
		t.Fatalf("content-disposition = %q", cd)
	}

	if code, _, _ := get("foreign-owner", "evt-a", 1); code != http.StatusNotFound {
		t.Fatalf("another org = %d, want 404", code)
	}
	if code, _, _ := get("", "evt-a", 1); code != http.StatusUnauthorized {
		t.Fatalf("anonymous = %d", code)
	}
	for _, c := range []struct {
		event string
		idx   any
	}{{"evt-foreign", 0}, {"missing", 0}, {"evt-a", 9}, {"evt-a", "x"}, {"evt-a", -1}, {"evt-a", 99999}} {
		if code, _, _ := get("owner", c.event, c.idx); code != http.StatusNotFound {
			t.Fatalf("%v/%v = %d, want 404", c.event, c.idx, code)
		}
	}
	if _, err := h.pool.Exec(ctx, `update sites set kind='web' where id=$1`, h.siteID); err != nil {
		t.Fatal(err)
	}
	if err := h.registry.Invalidate(); err != nil {
		t.Fatal(err)
	}
	if code, _, _ := get("owner", "evt-a", 1); code != http.StatusNotFound {
		t.Fatalf("web project = %d", code)
	}
}
