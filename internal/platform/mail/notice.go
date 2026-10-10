package mail

import (
	"bytes"
	"context"
	"errors"
	"html/template"
	"strings"
)

// Notice is a generic transactional notice rendered as text and HTML with one layout.
type Notice struct {
	Subject     string
	Heading     string
	Paragraphs  []string
	Items       []string
	ActionLabel string
	ActionURL   string
	Footer      string
}

var noticeHTML = template.Must(template.New("notice").Parse(`<!doctype html>
<html><body style="margin:0;background:#f6f7f9;color:#17191c;font-family:Arial,sans-serif">
<div style="max-width:640px;margin:0 auto;padding:32px 20px">
<div style="background:#ffffff;border:1px solid #e4e7eb;border-radius:8px;padding:28px">
{{if .Heading}}<h1 style="font-size:22px;line-height:1.3;margin:0 0 20px">{{.Heading}}</h1>{{end}}
{{range .Paragraphs}}<p style="font-size:15px;line-height:1.6;margin:0 0 16px">{{.}}</p>{{end}}
{{if .Items}}<ul style="font-size:15px;line-height:1.6;margin:0 0 20px;padding-left:22px">{{range .Items}}<li>{{.}}</li>{{end}}</ul>{{end}}
{{if and .ActionLabel .ActionURL}}<p style="margin:24px 0"><a href="{{.ActionURL}}" style="display:inline-block;background:#17191c;color:#ffffff;text-decoration:none;border-radius:6px;padding:11px 16px">{{.ActionLabel}}</a></p>{{end}}
{{if .Footer}}<p style="font-size:12px;line-height:1.5;color:#68707a;margin:24px 0 0">{{.Footer}}</p>{{end}}
</div></div></body></html>`))

// Message renders the notice for one recipient.
func (n Notice) Message(to string) Message {
	sections := make([]string, 0, 4+len(n.Paragraphs))
	if n.Heading != "" {
		sections = append(sections, n.Heading)
	}
	sections = append(sections, n.Paragraphs...)
	if len(n.Items) > 0 {
		items := make([]string, len(n.Items))
		for index, item := range n.Items {
			items[index] = "- " + item
		}
		sections = append(sections, strings.Join(items, "\n"))
	}
	if n.ActionLabel != "" && n.ActionURL != "" {
		sections = append(sections, n.ActionLabel+": "+n.ActionURL)
	}
	if n.Footer != "" {
		sections = append(sections, n.Footer)
	}

	var html bytes.Buffer
	if err := noticeHTML.Execute(&html, n); err != nil {
		panic(err)
	}
	return Message{
		To:      to,
		Subject: n.Subject,
		Text:    strings.Join(sections, "\n\n") + "\n",
		HTML:    html.String(),
	}
}

// SendAll sends a notice to every recipient and joins any delivery failures.
func SendAll(ctx context.Context, sender Sender, recipients []string, notice Notice) error {
	var result error
	for _, err := range SendEach(ctx, sender, recipients, notice) {
		if err != nil {
			result = errors.Join(result, err)
		}
	}
	return result
}

// SendEach sends a notice to every recipient and returns one result per
// recipient in the same order.
func SendEach(ctx context.Context, sender Sender, recipients []string, notice Notice) []error {
	results := make([]error, len(recipients))
	if sender == nil || !sender.Enabled() {
		return results
	}
	for index, recipient := range recipients {
		results[index] = sender.Send(ctx, notice.Message(recipient))
	}
	return results
}
