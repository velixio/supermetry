package alerts

import (
	"bytes"
	"errors"
	"fmt"
	"html"
	"regexp"
	"strconv"
	"strings"
	"text/template"
	"time"
	"unicode"
	"unicode/utf8"
)

const (
	MaxTemplateSubjectBytes = 200
	MaxTemplateBodyBytes    = 4096
	MaxDeliveryErrorBytes   = 512
)

var (
	ErrInvalidTemplate = errors.New("invalid notification template")
	secretErrorPattern = regexp.MustCompile(`(?i)(?:bearer|token|secret|authorization)[\s=:]+[^\s,;]+`)
	TemplateVariables  = []string{"alert_id", "alert_kind", "site_id", "value", "threshold", "from", "to", "fired_at",
		"app_name", "issue_title", "issue_kind", "release", "events", "installs", "crash_free", "metric", "baseline", "issue_url"}
)

type TemplateInput struct {
	Subject string `json:"subject,omitempty"`
	Body    string `json:"body"`
}

type MessageTemplate struct {
	Kind      ChannelKind `json:"kind"`
	Subject   string      `json:"subject,omitempty"`
	Body      string      `json:"body"`
	IsDefault bool        `json:"is_default"`
	UpdatedAt *time.Time  `json:"updated_at,omitempty"`
}

type RenderedMessage struct {
	Subject string `json:"subject,omitempty"`
	Body    string `json:"body"`
}

func DefaultTemplate(kind ChannelKind) TemplateInput {
	body := "{{.alert_kind}} alert for site {{.site_id}}: value {{.value}}, threshold {{.threshold}}"
	if kind == ChannelEmail {
		return TemplateInput{Subject: "Supermetry analytics alert", Body: body}
	}
	return TemplateInput{Body: body}
}

// DefaultTemplateFor is the default message for an alert kind on a channel
// kind: the generic text for traffic and goal alerts, a written one per app
// kind. Messages carry no stack frames or tags.
func DefaultTemplateFor(kind ChannelKind, alert AlertKind) TemplateInput {
	var subject, body string
	switch alert {
	case AlertAppNewIssue:
		subject = "New issue in {{.app_name}}"
		body = "New issue in {{.app_name}} ({{.issue_kind}}, release {{.release}}): {{.issue_title}} ({{.events}} events)\n{{.issue_url}}"
	case AlertAppIssueRegressed:
		subject = "Regression in {{.app_name}}"
		body = "Regression in {{.app_name}} ({{.issue_kind}}, release {{.release}}): {{.issue_title}}\n{{.issue_url}}"
	case AlertAppCrashFreeBelow:
		subject = "Crash-free {{.metric}} below threshold in {{.app_name}}"
		body = "Crash-free {{.metric}} for {{.app_name}} {{.release}} is {{.crash_free}}%, below the {{.threshold}}% threshold ({{.installs}} installs in the last 24 hours)"
	case AlertAppIssueSpike:
		subject = "Spike in {{.app_name}}"
		body = "Spike in {{.app_name}} ({{.issue_kind}}, release {{.release}}): {{.issue_title}}\n{{.events}} events in the last full hour against an average of {{.baseline}} for this hour of the day\n{{.issue_url}}"
	default:
		return DefaultTemplate(kind)
	}
	if kind != ChannelEmail {
		subject = ""
	}
	return TemplateInput{Subject: subject, Body: body}
}

func RenderTemplate(kind ChannelKind, input TemplateInput, notification Notification) (RenderedMessage, error) {
	if !validChannelKind(kind) || input.Body == "" || len(input.Body) > MaxTemplateBodyBytes || len(input.Subject) > MaxTemplateSubjectBytes {
		return RenderedMessage{}, ErrInvalidTemplate
	}
	if kind != ChannelEmail && input.Subject != "" {
		return RenderedMessage{}, ErrInvalidTemplate
	}
	if strings.ContainsAny(input.Subject, "\r\n") || containsUnsafeControl(input.Subject) || containsUnsafeControl(input.Body) {
		return RenderedMessage{}, ErrInvalidTemplate
	}
	values := templateValues(kind, notification)
	body, err := executeTemplate("body", input.Body, values)
	if err != nil || len(body) > MaxTemplateBodyBytes {
		return RenderedMessage{}, ErrInvalidTemplate
	}
	subject := ""
	if input.Subject != "" {
		subject, err = executeTemplate("subject", input.Subject, values)
		if err != nil || len(subject) > MaxTemplateSubjectBytes || strings.ContainsAny(subject, "\r\n") {
			return RenderedMessage{}, ErrInvalidTemplate
		}
	}
	return RenderedMessage{Subject: subject, Body: body}, nil
}

func executeTemplate(name, source string, values map[string]string) (string, error) {
	parsed, err := template.New(name).Option("missingkey=error").Parse(source)
	if err != nil {
		return "", err
	}
	var output bytes.Buffer
	if err := parsed.Execute(&output, values); err != nil {
		return "", err
	}
	return output.String(), nil
}

func templateValues(kind ChannelKind, notification Notification) map[string]string {
	escape := func(value string) string { return escapeTemplateValue(kind, value) }
	app := AppNotification{}
	crashFree := ""
	if notification.App != nil {
		app = *notification.App
	}
	if notification.Kind == AlertAppCrashFreeBelow {
		crashFree = fmt.Sprintf("%.2f", notification.Value)
	}
	values := map[string]string{
		"app_name": escape(app.AppName), "issue_title": escape(app.IssueTitle), "issue_kind": escape(app.IssueKind),
		"release": escape(app.Release), "events": strconv.FormatInt(app.Events, 10), "installs": strconv.FormatInt(app.Installs, 10),
		"crash_free": crashFree, "metric": escape(app.Metric), "baseline": fmt.Sprintf("%.2f", app.Baseline), "issue_url": escape(app.IssueURL),
	}
	if notification.App == nil {
		values["events"], values["installs"], values["baseline"] = "", "", ""
	}
	for key, value := range map[string]string{
		"alert_id": strconv.FormatInt(notification.AlertID, 10), "site_id": strconv.FormatInt(int64(notification.SiteID), 10),
		"alert_kind": escape(string(notification.Kind)), "value": fmt.Sprintf("%.2f", notification.Value),
		"threshold": fmt.Sprintf("%.2f", notification.Threshold), "from": notification.From.UTC().Format(time.RFC3339),
		"to": notification.To.UTC().Format(time.RFC3339), "fired_at": notification.FiredAt.UTC().Format(time.RFC3339),
	} {
		values[key] = value
	}
	return values
}

func escapeTemplateValue(kind ChannelKind, value string) string {
	switch kind {
	case ChannelSlack:
		return html.EscapeString(value)
	case ChannelDiscord:
		replacer := strings.NewReplacer("\\", "\\\\", "`", "\\`", "*", "\\*", "_", "\\_", "~", "\\~", "<", "\\<", ">", "\\>", "|", "\\|", "@", "@\u200b")
		return replacer.Replace(value)
	default:
		return value
	}
}

func containsUnsafeControl(value string) bool {
	return strings.IndexFunc(value, func(r rune) bool {
		return (unicode.IsControl(r) && r != '\n' && r != '\t') || r == utf8.RuneError
	}) >= 0
}

func validChannelKind(kind ChannelKind) bool {
	switch kind {
	case ChannelEmail, ChannelWebhook, ChannelSlack, ChannelDiscord, ChannelTelegram:
		return true
	default:
		return false
	}
}

func scrubDeliveryError(channel DeliveryChannel, raw string) string {
	clean := raw
	for _, secret := range []string{channel.URL, channel.Secret, channel.BotToken} {
		if secret != "" {
			clean = strings.ReplaceAll(clean, secret, "[redacted]")
		}
	}
	clean = secretErrorPattern.ReplaceAllString(clean, "[redacted]")
	clean = strings.Map(func(r rune) rune {
		if unicode.IsControl(r) && r != ' ' {
			return ' '
		}
		return r
	}, clean)
	clean = strings.Join(strings.Fields(clean), " ")
	if len(clean) > MaxDeliveryErrorBytes {
		clean = clean[:MaxDeliveryErrorBytes]
		for !utf8.ValidString(clean) {
			clean = clean[:len(clean)-1]
		}
	}
	return clean
}
