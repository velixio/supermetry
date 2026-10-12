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
	TemplateVariables  = []string{"alert_id", "alert_kind", "site_id", "value", "threshold", "from", "to", "fired_at"}
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
	return map[string]string{
		"alert_id": strconv.FormatInt(notification.AlertID, 10), "site_id": strconv.FormatInt(int64(notification.SiteID), 10),
		"alert_kind": escape(string(notification.Kind)), "value": fmt.Sprintf("%.2f", notification.Value),
		"threshold": fmt.Sprintf("%.2f", notification.Threshold), "from": notification.From.UTC().Format(time.RFC3339),
		"to": notification.To.UTC().Format(time.RFC3339), "fired_at": notification.FiredAt.UTC().Format(time.RFC3339),
	}
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
