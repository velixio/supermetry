package mail

import (
	"context"
	"errors"
	"strings"
	"testing"
)

func TestNotice_MessageText(t *testing.T) {
	notice := Notice{
		Subject:     "Deletion scheduled",
		Heading:     "Example Site",
		Paragraphs:  []string{"The site is scheduled for deletion.", "It will be deleted on 2026-10-18 12:00 UTC."},
		Items:       []string{"First site", "Second site"},
		ActionLabel: "Restore site",
		ActionURL:   "https://example.test/app/example/sites",
		Footer:      "You can ignore this message if this was expected.",
	}

	message := notice.Message("owner@example.test")
	want := "Example Site\n\n" +
		"The site is scheduled for deletion.\n\n" +
		"It will be deleted on 2026-10-18 12:00 UTC.\n\n" +
		"- First site\n" +
		"- Second site\n\n" +
		"Restore site: https://example.test/app/example/sites\n\n" +
		"You can ignore this message if this was expected.\n"
	if message.Text != want {
		t.Fatalf("Message().Text = %q, want %q", message.Text, want)
	}
	if message.To != "owner@example.test" || message.Subject != notice.Subject {
		t.Fatalf("Message() envelope = (%q, %q), want (%q, %q)", message.To, message.Subject, "owner@example.test", notice.Subject)
	}
}

func TestNotice_MessageEscapesHTML(t *testing.T) {
	message := (Notice{
		Heading:    "Heading <script>alert(1)</script>",
		Paragraphs: []string{"Paragraph <script>alert(2)</script>"},
		Items:      []string{"Item <script>alert(3)</script>"},
	}).Message("owner@example.test")

	if strings.Contains(message.HTML, "<script>") {
		t.Fatalf("Message().HTML contains executable script: %s", message.HTML)
	}
	for _, escaped := range []string{
		"Heading &lt;script&gt;alert(1)&lt;/script&gt;",
		"Paragraph &lt;script&gt;alert(2)&lt;/script&gt;",
		"Item &lt;script&gt;alert(3)&lt;/script&gt;",
	} {
		if !strings.Contains(message.HTML, escaped) {
			t.Fatalf("Message().HTML = %q, want substring %q", message.HTML, escaped)
		}
	}
}

func TestNotice_MessageOmitsActionWithoutURL(t *testing.T) {
	message := (Notice{Heading: "Heading", ActionLabel: "Restore", ActionURL: ""}).Message("owner@example.test")
	if strings.Contains(message.Text, "Restore") || strings.Contains(message.HTML, "Restore") {
		t.Fatalf("Message() rendered action without URL: text=%q html=%q", message.Text, message.HTML)
	}
}

func TestSendAllContinuesAndJoinsFailures(t *testing.T) {
	firstErr := errors.New("first failure")
	thirdErr := errors.New("third failure")
	sender := &noticeTestSender{
		enabled: true,
		failures: map[string]error{
			"first@example.test": firstErr,
			"third@example.test": thirdErr,
		},
	}
	recipients := []string{"first@example.test", "second@example.test", "third@example.test"}

	err := SendAll(context.Background(), sender, recipients, Notice{Subject: "Subject", Heading: "Heading"})
	if !errors.Is(err, firstErr) || !errors.Is(err, thirdErr) {
		t.Fatalf("SendAll() error = %v, want both delivery failures", err)
	}
	if got := strings.Join(sender.attempts, ","); got != strings.Join(recipients, ",") {
		t.Fatalf("SendAll() attempts = %q, want %q", got, strings.Join(recipients, ","))
	}
	if len(sender.Sent) != 1 || sender.Sent[0].To != "second@example.test" {
		t.Fatalf("SendAll() successful messages = %+v, want only second recipient", sender.Sent)
	}
}

func TestSendAllDoesNothingWhenDisabled(t *testing.T) {
	sender := &noticeTestSender{enabled: false}
	if err := SendAll(context.Background(), sender, []string{"owner@example.test"}, Notice{Heading: "Heading"}); err != nil {
		t.Fatal(err)
	}
	if len(sender.attempts) != 0 || len(sender.Sent) != 0 {
		t.Fatalf("disabled SendAll() attempts=%v sent=%v, want none", sender.attempts, sender.Sent)
	}
}

func TestSendEachReturnsResultsAlignedWithRecipients(t *testing.T) {
	failed := errors.New("recipient rejected")
	sender := &noticeTestSender{
		enabled:  true,
		failures: map[string]error{"bad@example.test": failed},
	}
	recipients := []string{"first@example.test", "bad@example.test", "last@example.test"}

	results := SendEach(context.Background(), sender, recipients, Notice{Subject: "Subject"})

	if len(results) != len(recipients) || results[0] != nil || !errors.Is(results[1], failed) || results[2] != nil {
		t.Fatalf("SendEach() = %v, want [nil, failure, nil]", results)
	}
}

type noticeTestSender struct {
	Fake
	enabled  bool
	failures map[string]error
	attempts []string
}

func (s *noticeTestSender) Enabled() bool { return s.enabled }

func (s *noticeTestSender) Send(ctx context.Context, message Message) error {
	s.attempts = append(s.attempts, message.To)
	if err := s.failures[message.To]; err != nil {
		return err
	}
	return s.Fake.Send(ctx, message)
}
