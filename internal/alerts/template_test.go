package alerts

import (
	"strings"
	"testing"
	"time"
)

func TestRenderTemplate_ChannelFixtures(t *testing.T) {
	notification := Notification{
		AlertID: 12, SiteID: 34, Kind: AlertKind(`<traffic&drop>`), Value: 7.5, Threshold: 10,
		From: time.Date(2026, 10, 12, 9, 0, 0, 0, time.UTC),
		To:   time.Date(2026, 10, 12, 10, 0, 0, 0, time.UTC), FiredAt: time.Date(2026, 10, 12, 10, 0, 0, 0, time.UTC),
	}
	tests := []struct {
		name string
		kind ChannelKind
		want string
	}{
		{name: "email escapes control characters", kind: ChannelEmail, want: "<traffic&drop> for 34: 7.50"},
		{name: "slack escapes entities", kind: ChannelSlack, want: "&lt;traffic&amp;drop&gt; for 34: 7.50"},
		{name: "discord neutralizes mentions and markdown", kind: ChannelDiscord, want: `\<traffic&drop\> for 34: 7.50`},
		{name: "telegram uses plain text", kind: ChannelTelegram, want: "<traffic&drop> for 34: 7.50"},
		{name: "webhook is later JSON encoded", kind: ChannelWebhook, want: "<traffic&drop> for 34: 7.50"},
	}
	for _, test := range tests {
		t.Run(test.name, func(t *testing.T) {
			rendered, err := RenderTemplate(test.kind, TemplateInput{Body: "{{.alert_kind}} for {{.site_id}}: {{.value}}"}, notification)
			if err != nil {
				t.Fatal(err)
			}
			if rendered.Body != test.want {
				t.Fatalf("body=%q, want %q", rendered.Body, test.want)
			}
		})
	}
}

func TestRenderTemplate_Validation(t *testing.T) {
	notification := Notification{Kind: AlertTrafficSpike, FiredAt: time.Now()}
	tests := []struct {
		name  string
		input TemplateInput
	}{
		{name: "unknown variable", input: TemplateInput{Body: "{{.secret}}"}},
		{name: "oversized body", input: TemplateInput{Body: strings.Repeat("x", MaxTemplateBodyBytes+1)}},
		{name: "email subject newline", input: TemplateInput{Subject: "Alert\nBcc: hidden@example.test", Body: "ok"}},
		{name: "non-email subject", input: TemplateInput{Subject: "subject", Body: "ok"}},
	}
	for _, test := range tests {
		t.Run(test.name, func(t *testing.T) {
			if _, err := RenderTemplate(ChannelSlack, test.input, notification); err == nil {
				t.Fatal("RenderTemplate accepted invalid template")
			}
		})
	}
}

func TestScrubDeliveryError(t *testing.T) {
	channel := DeliveryChannel{URL: "https://hooks.example.test/private", Secret: "shared-secret", BotToken: "123456:token-value"}
	err := scrubDeliveryError(channel, strings.Repeat("prefix ", 100)+channel.URL+" bearer abc.def "+channel.Secret+" "+channel.BotToken)
	if len(err) > MaxDeliveryErrorBytes {
		t.Fatalf("error length=%d, want <=%d", len(err), MaxDeliveryErrorBytes)
	}
	for _, secret := range []string{channel.URL, channel.Secret, channel.BotToken, "abc.def"} {
		if strings.Contains(err, secret) {
			t.Fatalf("scrubbed error contains %q: %q", secret, err)
		}
	}
}
