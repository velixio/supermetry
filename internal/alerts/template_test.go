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

func appNotification(kind AlertKind) Notification {
	n := Notification{
		AlertID: 3, SiteID: 9, Kind: kind, FiredAt: time.Date(2026, 11, 15, 12, 30, 0, 0, time.UTC),
		From: time.Date(2026, 11, 15, 11, 0, 0, 0, time.UTC), To: time.Date(2026, 11, 15, 12, 0, 0, 0, time.UTC),
		App: &AppNotification{
			AppName: "VoiceStudio", IssueID: 42, IssueTitle: "SegFault in render <loop>", IssueKind: "crash", Release: "1.4.0",
			IssueURL: "/app/acme/voicestudio/issues/42", Events: 30, Installs: 250, Metric: MetricUsers, Baseline: 5,
		},
	}
	switch kind {
	case AlertAppCrashFreeBelow:
		n.Value, n.Threshold = 97.25, 99
	case AlertAppIssueSpike:
		n.Value, n.Threshold = 30, 15
	default:
		n.Value, n.Threshold = 30, 1
	}
	return n
}

func TestDefaultTemplateForEachAppKind(t *testing.T) {
	cases := []struct {
		kind AlertKind
		want []string
	}{
		{AlertAppNewIssue, []string{"New issue in VoiceStudio", "SegFault in render <loop>", "crash", "1.4.0", "/app/acme/voicestudio/issues/42"}},
		{AlertAppIssueRegressed, []string{"Regression in VoiceStudio", "SegFault in render <loop>", "1.4.0", "/app/acme/voicestudio/issues/42"}},
		{AlertAppCrashFreeBelow, []string{"Crash-free users", "VoiceStudio 1.4.0", "97.25%", "99.00%", "250 installs"}},
		{AlertAppIssueSpike, []string{"Spike in VoiceStudio", "SegFault in render <loop>", "30 events", "5.00", "/app/acme/voicestudio/issues/42"}},
	}
	for _, c := range cases {
		t.Run(string(c.kind), func(t *testing.T) {
			for _, channel := range []ChannelKind{ChannelEmail, ChannelWebhook, ChannelTelegram} {
				rendered, err := RenderTemplate(channel, DefaultTemplateFor(channel, c.kind), appNotification(c.kind))
				if err != nil {
					t.Fatalf("%s: %v", channel, err)
				}
				for _, want := range c.want {
					if !strings.Contains(rendered.Body, want) {
						t.Errorf("%s body %q lacks %q", channel, rendered.Body, want)
					}
				}
				if strings.Contains(strings.ToLower(rendered.Body), "frame") || strings.Contains(strings.ToLower(rendered.Body), "tag") {
					t.Errorf("%s body leaks frames or tags: %q", channel, rendered.Body)
				}
				if channel == ChannelEmail && !strings.Contains(rendered.Subject, "VoiceStudio") {
					t.Errorf("email subject %q lacks the app name", rendered.Subject)
				}
			}
		})
	}
}

func TestAppTemplateVariablesAreEscapedPerChannelAndEmptyForWebAlerts(t *testing.T) {
	custom := TemplateInput{Body: "{{.app_name}}|{{.issue_title}}|{{.issue_kind}}|{{.release}}|{{.events}}|{{.installs}}|{{.crash_free}}|{{.issue_url}}"}
	slack, err := RenderTemplate(ChannelSlack, custom, appNotification(AlertAppNewIssue))
	if err != nil || !strings.Contains(slack.Body, "SegFault in render &lt;loop&gt;") {
		t.Fatalf("slack = %q, %v", slack.Body, err)
	}
	web, err := RenderTemplate(ChannelEmail, TemplateInput{Subject: "s", Body: "[{{.app_name}}][{{.issue_title}}][{{.events}}]"}, Notification{Kind: AlertTrafficSpike, FiredAt: time.Now()})
	if err != nil || web.Body != "[][][]" {
		t.Fatalf("web alert with app variables = %q, %v", web.Body, err)
	}
	for _, name := range []string{"app_name", "issue_title", "issue_kind", "release", "events", "installs", "crash_free", "issue_url"} {
		found := false
		for _, v := range TemplateVariables {
			found = found || v == name
		}
		if !found {
			t.Errorf("TemplateVariables lacks %q", name)
		}
	}
}

func TestDefaultTemplateForTrafficKindsIsUnchanged(t *testing.T) {
	if got, want := DefaultTemplateFor(ChannelEmail, AlertTrafficSpike), DefaultTemplate(ChannelEmail); got != want {
		t.Fatalf("got %+v, want %+v", got, want)
	}
}
