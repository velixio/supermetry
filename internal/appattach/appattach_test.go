package appattach

import (
	"bytes"
	"strings"
	"testing"
)

var png = append([]byte("\x89PNG\r\n\x1a\n"), make([]byte, 32)...)

func redact(s string) string { return strings.ReplaceAll(s, "secret", "[x]") }

func TestPrepareStoresImagesAndText(t *testing.T) {
	r := Prepare([]Input{
		{Name: "shot.png", ContentType: "image/png", AttachmentType: "event.attachment", Payload: png},
		{Name: "log.txt", ContentType: "text/plain; charset=utf-8", Payload: []byte("a secret b")},
		{Name: "vh.json", AttachmentType: "event.view_hierarchy", Payload: []byte(`{"k":"secret"}`)},
	}, redact)
	if len(r.Metas) != 3 || len(r.Files) != 3 {
		t.Fatalf("metas=%d files=%d", len(r.Metas), len(r.Files))
	}
	if r.Files[1].ContentType != "text/plain" || string(r.Files[1].Data) != "a [x] b" {
		t.Fatalf("text = %q %q", r.Files[1].ContentType, r.Files[1].Data)
	}
	if r.Files[2].ContentType != "application/json" || string(r.Files[2].Data) != `{"k":"[x]"}` {
		t.Fatalf("json = %q %q", r.Files[2].ContentType, r.Files[2].Data)
	}
	for i, f := range r.Files {
		if int(f.Idx) != i || !r.Metas[i].Stored {
			t.Fatalf("idx/stored mismatch at %d", i)
		}
	}
}

func TestPrepareCapsKeepMetadata(t *testing.T) {
	big := bytes.Repeat([]byte("a"), MaxFile+1)
	ok := bytes.Repeat([]byte("b"), MaxFile)
	in := []Input{{Name: "big.txt", ContentType: "text/plain", Payload: big}}
	for range 6 {
		in = append(in, Input{Name: "f.txt", ContentType: "text/plain", Payload: ok})
	}
	r := Prepare(in, func(s string) string { return s })
	if r.TooLarge != 2 { // the oversize file and the sixth 1 MiB file (event cap 5 MiB)
		t.Fatalf("too large = %d, want 2", r.TooLarge)
	}
	if len(r.Metas) != 7 || r.Metas[0].Stored || r.Metas[0].Size != MaxFile+1 || r.Metas[6].Stored || !r.Metas[5].Stored {
		t.Fatalf("metas = %+v", r.Metas)
	}
	total := 0
	for _, f := range r.Files {
		total += len(f.Data)
	}
	if total > MaxEvent || len(r.Files) != 5 {
		t.Fatalf("stored %d bytes in %d files", total, len(r.Files))
	}
}

func TestPrepareNeverStoresActiveContent(t *testing.T) {
	r := Prepare([]Input{
		{Name: "x.svg", ContentType: "image/svg+xml", Payload: []byte(`<svg onload="alert(1)"/>`)},
		{Name: "x.html", ContentType: "text/html", Payload: []byte(`<script>alert(1)</script>`)},
		{Name: "fake.png", ContentType: "image/png", Payload: []byte(`<html><script>`)},
		{Name: "dump.bin", ContentType: "application/octet-stream", Payload: []byte{0, 1, 2}},
		{Name: "m.dmp", AttachmentType: "event.minidump", Payload: []byte("MDMP")},
		{Name: "bad.txt", ContentType: "text/plain", Payload: []byte{0xff, 0xfe, 'a'}},
	}, redact)
	for i, m := range r.Metas {
		if i == 1 {
			continue // html is kept, but as plain text
		}
		if m.Stored {
			t.Fatalf("meta %d (%s) stored", i, m.Name)
		}
	}
	if len(r.Files) != 1 || r.Files[0].ContentType != "text/plain" || r.Files[0].Idx != 1 {
		t.Fatalf("files = %+v", r.Files)
	}
}

func TestPrepareScrubsAndKeepsInvalidJSONAsText(t *testing.T) {
	data := []byte("email me at a@example.test\n" + strings.Repeat("x", 100) + "\n")
	r := Prepare([]Input{{Name: "n.txt", ContentType: "text/plain", Payload: data}}, func(s string) string {
		return strings.ReplaceAll(s, "a@example.test", "[email]")
	})
	if strings.Contains(string(r.Files[0].Data), "example.test") {
		t.Fatalf("not scrubbed: %q", r.Files[0].Data)
	}
	r = Prepare([]Input{{Name: "j.json", ContentType: "application/json", Payload: []byte(`{"a":"1"}`)}},
		func(string) string { return "not json" })
	if r.Files[0].ContentType != "text/plain" {
		t.Fatalf("content type = %q", r.Files[0].ContentType)
	}
}

func TestPrepareScrubsLongLinesInPieces(t *testing.T) {
	long := strings.Repeat("y", 3*maxScrubLine+10)
	var seen int
	r := Prepare([]Input{{Name: "", ContentType: "text/plain", Payload: []byte(long)}}, func(s string) string {
		if len(s) > maxScrubLine {
			t.Fatalf("scrubber got %d bytes", len(s))
		}
		seen += len(s)
		return s
	})
	if seen != len(long) || len(r.Files[0].Data) != len(long) {
		t.Fatalf("seen=%d stored=%d", seen, len(r.Files[0].Data))
	}
}

func TestPrepareLimitsAttachmentCountAndSanitisesName(t *testing.T) {
	in := make([]Input, MaxPerEvent+5)
	for i := range in {
		in[i] = Input{Name: "a.txt", ContentType: "text/plain", Payload: []byte("x")}
	}
	if r := Prepare(in, func(s string) string { return s }); len(r.Metas) != MaxPerEvent {
		t.Fatalf("metas = %d", len(r.Metas))
	}
	r := Prepare([]Input{{Name: "../../etc/pa\r\nss\"wd/" + strings.Repeat("n", 300), ContentType: "text/plain", Payload: []byte("x")}},
		func(s string) string { return s })
	n := r.Metas[0].Name
	if len(n) > MaxNameLen || strings.ContainsAny(n, "/\\\r\n\"") {
		t.Fatalf("name = %q", n)
	}
}

func TestInlineAllowlist(t *testing.T) {
	for ct, want := range map[string]bool{"image/png": true, "image/jpeg": true, "image/webp": true, "image/svg+xml": false, "text/html": false, "text/plain": false, "image/gif": false, "": false} {
		if Inline(ct) != want {
			t.Fatalf("Inline(%q) = %v", ct, !want)
		}
	}
}
