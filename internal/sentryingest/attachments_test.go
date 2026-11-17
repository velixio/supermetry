package sentryingest

import (
	"fmt"
	"strings"
	"testing"

	"supermetry/internal/appattach"
)

func attachmentItem(ctype, atype, name, payload string) string {
	h := fmt.Sprintf(`{"type":"attachment","length":%d,"filename":%q`, len(payload), name)
	if ctype != "" {
		h += fmt.Sprintf(`,"content_type":%q`, ctype)
	}
	if atype != "" {
		h += fmt.Sprintf(`,"attachment_type":%q`, atype)
	}
	return h + "}\n" + payload + "\n"
}

const attEvent = `{"event_id":"11111111111111111111111111111111"}` + "\n" + `{"type":"event"}` + "\n" + `{"message":"hi"}` + "\n"

func TestAttachmentsAreScrubbedCappedAndHandedToTheBodyWriter(t *testing.T) {
	e := newEnv(t)
	png := "\x89PNG\r\n\x1a\n" + strings.Repeat("\x00", 40)
	env := attEvent +
		attachmentItem("text/plain", "", "../../home/alice/log.txt", "mail alice@example.test token=abcdef123456 ok") +
		attachmentItem("image/png", "", "shot.png", png) +
		attachmentItem("image/svg+xml", "", "x.svg", `<svg onload="alert(1)"/>`) +
		attachmentItem("application/octet-stream", "event.minidump", "m.dmp", "MDMP")
	if w := e.do(post(fdPath, fdQuery, env, nil)); w.Code != 200 {
		t.Fatalf("status %d", w.Code)
	}
	if len(e.bodies.items) != 1 {
		t.Fatalf("bodies = %d", len(e.bodies.items))
	}
	b := e.bodies.items[0]
	if len(b.Attachments) != 4 || len(b.Files) != 2 {
		t.Fatalf("metas=%+v files=%d", b.Attachments, len(b.Files))
	}
	if got := string(b.Files[0].Data); strings.Contains(got, "alice@") || strings.Contains(got, "abcdef123456") {
		t.Fatalf("text not scrubbed: %q", got)
	}
	if b.Attachments[0].Name != "log.txt" || !b.Attachments[0].Stored || b.Attachments[2].Stored || b.Attachments[3].Stored {
		t.Fatalf("metas = %+v", b.Attachments)
	}
	if b.Files[1].ContentType != appattach.TypePNG {
		t.Fatalf("png type = %q", b.Files[1].ContentType)
	}
}

func TestAttachmentCapsCountedAndMetadataKept(t *testing.T) {
	e := newEnv(t)
	big := strings.Repeat("a", MaxAttachment+1)
	ok := strings.Repeat("b", appattach.MaxFile)
	env := attEvent + attachmentItem("text/plain", "", "big.txt", big)
	for range 6 {
		env += attachmentItem("text/plain", "", "f.txt", ok)
	}
	if w := e.do(post(fdPath, fdQuery, env, nil)); w.Code != 200 {
		t.Fatalf("status %d", w.Code)
	}
	b := e.bodies.items[0]
	if len(b.Attachments) != 7 || len(b.Files) != 5 || b.Attachments[0].Stored || b.Attachments[0].Size != MaxAttachment+1 {
		t.Fatalf("metas=%d files=%d first=%+v", len(b.Attachments), len(b.Files), b.Attachments[0])
	}
	// One oversize file (counted per item) plus the sixth 1 MiB file over the 5 MiB event cap.
	if got := e.h.Stats().Snapshot()["attachment_too_large"]; got != 2 {
		t.Fatalf("attachment_too_large = %d, want 2", got)
	}
}
