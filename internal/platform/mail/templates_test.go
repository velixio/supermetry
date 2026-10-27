package mail

import (
	"context"
	"os"
	"path/filepath"
	"regexp"
	"strings"
	"sync/atomic"
	"testing"
	"time"
)

const (
	testBase    = "https://stats.example.test"
	hostileName = `<script>alert(1)</script> "Ünïcode" 日本語 & Co`
)

type previewCase struct {
	name   string
	notice Notice
	link   string // the URL that must appear in HTML and text
}

func allEmails(base string) []previewCase {
	reset := "https://stats.example.test/reset/abc123"
	invite := "https://stats.example.test/invite?token=tok%2B1"
	confirm := "https://stats.example.test/api/private/auth/email-change/confirm?token=xyz"
	return []previewCase{
		{"password-reset", PasswordReset(base, reset, time.Hour), reset},
		{"invitation", Invitation(base, invite, InvitationInfo{OrgName: "Acme Analytics", InviterName: "Dana Reyes", InviterEmail: "dana@acme.test", Role: "editor", Lifetime: 7 * 24 * time.Hour}), invite},
		{"invitation-hostile", Invitation(base, invite, InvitationInfo{OrgName: hostileName, InviterName: hostileName, InviterEmail: "x@y.test", Role: "viewer", Lifetime: 7 * 24 * time.Hour}), invite},
		{"welcome", Welcome(base, "Dana Reyes"), base + "/app"},
		{"welcome-hostile", Welcome(base, hostileName), base + "/app"},
		{"confirm-email", ConfirmNewEmail(base, confirm, "new@example.test", time.Hour), confirm},
		{"email-changed", EmailChanged(base, "new@example.test"), ""},
		{"deletion-site", Notice{
			Subject: "Blog is scheduled for deletion on 2026-11-20", Heading: "Blog is scheduled for deletion on 2026-11-20",
			Preheader:  "Restore it before 2026-11-20 to keep its data.",
			Paragraphs: []string{"The site will be permanently deleted on 2026-11-20 10:00 UTC.", "Restore the site before then to keep its data."},
			Items:      []string{"blog.example.test"}, ActionLabel: "Restore site", ActionURL: base + "/app/acme/sites",
			Reason: "You are receiving this because you manage this site on Supermetry.", BaseURL: base,
		}, base + "/app/acme/sites"},
		{"report", Notice{
			Subject: "blog — weekly report", Heading: "blog — weekly report", Preheader: "1200 visitors, 3400 pageviews",
			Sections: []Section{
				{Heading: "Overview", Rows: []Row{{"Visitors", "1200 (+12.5%)"}, {"Pageviews", "3400 (-3.0%)"}}},
				{Heading: "Top pages", Rows: []Row{{"/pricing", "400 visitors, 900 pageviews"}, {"/docs/getting-started/install", "250 visitors, 600 pageviews"}}},
			},
			ActionLabel: "Open dashboard", ActionURL: base + "/app",
			Reason: "You are receiving this because you subscribed to weekly reports for this site.", BaseURL: base,
		}, base + "/app"},
		{"no-base-url", PasswordReset("", reset, time.Hour), reset},
	}
}

func TestEmails(t *testing.T) {
	externalRef := regexp.MustCompile(`(?:src|href)="(https?://[^"]+)"`)
	for _, c := range allEmails(testBase) {
		t.Run(c.name, func(t *testing.T) {
			message := c.notice.Message("to@example.test")
			if message.Subject == "" || len(message.Subject) > 80 && !strings.Contains(c.name, "hostile") {
				t.Errorf("subject %q should be short and set", message.Subject)
			}
			if message.Text == "" || message.HTML == "" {
				t.Fatal("both parts are required")
			}
			if len(message.HTML) > 100*1024 {
				t.Errorf("html is %d bytes", len(message.HTML))
			}
			if strings.Contains(message.HTML, "<script") {
				t.Errorf("html contains a script element: %s", message.HTML)
			}
			if c.link != "" {
				if !strings.Contains(message.Text, "\n"+c.link+"\n") {
					t.Errorf("text part needs %q on its own line:\n%s", c.link, message.Text)
				}
				if !strings.Contains(message.HTML, `href="`+c.link+`"`) && !strings.Contains(message.HTML, `href="`+strings.ReplaceAll(c.link, "%2B", "%2B")+`"`) {
					t.Errorf("html part needs link %q", c.link)
				}
			}
			if !strings.Contains(message.HTML, `alt="Supermetry"`) && !strings.Contains(message.HTML, ">Supermetry</div>") {
				t.Error("logo or wordmark missing")
			}
			// Only the logo, the links the email is about, and the footer link may be external.
			for _, match := range externalRef.FindAllStringSubmatch(message.HTML, -1) {
				ref := match[1]
				if strings.HasPrefix(ref, testBase) || ref == DocsURL {
					continue
				}
				t.Errorf("unexpected external reference %q", ref)
			}
			if strings.Contains(message.HTML, "src=\"") && !strings.Contains(message.HTML, `src="`+testBase+LogoPath+`"`) && c.name != "no-base-url" {
				t.Errorf("logo URL should derive from the base URL")
			}
		})
	}
}

func TestLogoFallsBackToWordmarkWithoutBaseURL(t *testing.T) {
	for _, base := range []string{"", "javascript:alert(1)", "ftp://x.test", "not a url"} {
		html := PasswordReset(base, "https://x.test/r", time.Hour).Message("a@b.test").HTML
		if strings.Contains(html, "<img") {
			t.Errorf("base %q: image should not render", base)
		}
		if !strings.Contains(html, ">Supermetry</div>") {
			t.Errorf("base %q: text wordmark missing", base)
		}
	}
}

func TestHostileNamesAreEscapedAndUnicodeSurvives(t *testing.T) {
	invite := Invitation(testBase, testBase+"/invite/x", InvitationInfo{OrgName: hostileName, InviterName: hostileName, InviterEmail: "a@b.test", Lifetime: 24 * time.Hour}).Message("a@b.test")
	welcome := Welcome(testBase, hostileName).Message("a@b.test")
	for _, message := range []Message{invite, welcome} {
		if strings.Contains(message.HTML, "<script>") || strings.Contains(message.HTML, "alert(1)</script>") {
			t.Fatalf("unescaped script in html: %s", message.HTML)
		}
		if !strings.Contains(message.HTML, "&lt;script&gt;") {
			t.Error("escaped form missing")
		}
		if !strings.Contains(message.HTML, "日本語") || !strings.Contains(message.Text, "Ünïcode") {
			t.Error("unicode lost")
		}
	}
	if strings.ContainsAny(invite.Subject, "\r\n") {
		t.Error("subject must be one line")
	}
	if got := cleanName("a\r\nBcc: evil@x.test", 50); strings.ContainsAny(got, "\r\n") {
		t.Errorf("cleanName kept a line break: %q", got)
	}
}

func TestUnsafeActionURLDropsButton(t *testing.T) {
	n := Notice{Subject: "s", Heading: "h", ActionLabel: "Go", ActionURL: "javascript:alert(1)", Links: []Link{{"Docs", "data:text/html,x"}}, BaseURL: testBase}
	message := n.Message("a@b.test")
	for _, part := range []string{message.HTML, message.Text} {
		if strings.Contains(part, "javascript:") || strings.Contains(part, "data:text") || strings.Contains(part, "Go") && strings.Contains(part, ">Go<") {
			t.Fatalf("unsafe URL leaked: %s", part)
		}
	}
}

func TestInvitationContent(t *testing.T) {
	m := Invitation(testBase, testBase+"/invite/x", InvitationInfo{OrgName: "Acme", InviterName: "Dana", InviterEmail: "dana@acme.test", Role: "admin", Lifetime: 7 * 24 * time.Hour}).Message("a@b.test")
	for _, want := range []string{"Acme", "Dana (dana@acme.test)", "Admin", "7 days"} {
		if !strings.Contains(m.Text, want) {
			t.Errorf("text missing %q:\n%s", want, m.Text)
		}
	}
	if !strings.Contains(m.Subject, "Acme") {
		t.Errorf("subject %q", m.Subject)
	}
}

// slowSender blocks in Send until released.
type slowSender struct {
	started chan struct{}
	release chan struct{}
	sent    atomic.Int32
	enabled bool
}

func (s *slowSender) Enabled() bool { return s.enabled }
func (s *slowSender) Send(ctx context.Context, _ Message) error {
	s.started <- struct{}{}
	select {
	case <-s.release:
	case <-ctx.Done():
	}
	s.sent.Add(1)
	return nil
}

func TestSendWelcomeAsyncDoesNotBlock(t *testing.T) {
	sender := &slowSender{started: make(chan struct{}, 1), release: make(chan struct{}), enabled: true}
	done := make(chan struct{})
	go func() {
		SendWelcomeAsync(sender, testBase, "new@example.test", "New")
		close(done)
	}()
	select {
	case <-done:
	case <-time.After(time.Second):
		t.Fatal("SendWelcomeAsync blocked on a slow SMTP server")
	}
	select {
	case <-sender.started:
	case <-time.After(2 * time.Second):
		t.Fatal("welcome was never sent")
	}
	close(sender.release)
}

func TestSendWelcomeAsyncSkipsWhenMailDisabled(t *testing.T) {
	sender := &slowSender{started: make(chan struct{}, 1), release: make(chan struct{}), enabled: false}
	SendWelcomeAsync(sender, testBase, "new@example.test", "New")
	SendWelcomeAsync(nil, testBase, "new@example.test", "New")
	select {
	case <-sender.started:
		t.Fatal("disabled sender must not be called")
	case <-time.After(100 * time.Millisecond):
	}
}

// TestPreviewWritesEveryEmail writes each email as .html and .txt for visual
// review: SUPERMETRY_EMAIL_PREVIEW_DIR=/tmp/x go test ./internal/platform/mail -run Preview
func TestPreviewWritesEveryEmail(t *testing.T) {
	dir := os.Getenv("SUPERMETRY_EMAIL_PREVIEW_DIR")
	if dir == "" {
		t.Skip("set SUPERMETRY_EMAIL_PREVIEW_DIR to write previews")
	}
	if err := os.MkdirAll(dir, 0o755); err != nil {
		t.Fatal(err)
	}
	// SUPERMETRY_EMAIL_PREVIEW_BASE lets the logo load from a local server.
	base := os.Getenv("SUPERMETRY_EMAIL_PREVIEW_BASE")
	if base == "" {
		base = testBase
	}
	for _, c := range allEmails(base) {
		message := c.notice.Message("to@example.test")
		if err := os.WriteFile(filepath.Join(dir, c.name+".html"), []byte(message.HTML), 0o644); err != nil {
			t.Fatal(err)
		}
		text := "Subject: " + message.Subject + "\n\n" + message.Text
		if err := os.WriteFile(filepath.Join(dir, c.name+".txt"), []byte(text), 0o644); err != nil {
			t.Fatal(err)
		}
	}
}
