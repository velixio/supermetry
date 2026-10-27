package mail

import (
	"context"
	"errors"
	"fmt"
	"strings"
)

// Notice is a transactional email rendered as text and HTML with one layout.
type Notice struct {
	Subject     string
	Preheader   string // hidden inbox preview text
	Eyebrow     string // small label above the heading, such as "Invitation"
	Heading     string
	Paragraphs  []string
	Items       []string
	Sections    []Section
	Steps       []Step // numbered "get started" list
	ActionLabel string
	ActionURL   string // http or https only; anything else drops the button
	Links       []Link // secondary links under the button
	Footer      string // small note under the content
	Reason      string // "You are receiving this because ..."
	BaseURL     string // public base URL: logo and footer link; empty gives a text wordmark
}

// Message renders the notice for one recipient.
func (n Notice) Message(to string) Message {
	sections := make([]string, 0, 6+len(n.Paragraphs))
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
	if len(n.Steps) > 0 {
		lines := make([]string, len(n.Steps))
		for index, step := range n.Steps {
			lines[index] = fmt.Sprintf("%d. %s", index+1, step.Title)
			if step.Body != "" {
				lines[index] += " - " + step.Body
			}
		}
		sections = append(sections, strings.Join(lines, "\n"))
	}
	for _, section := range n.Sections {
		lines := make([]string, 0, 1+len(section.Rows))
		if section.Heading != "" {
			lines = append(lines, section.Heading+":")
		}
		for _, row := range section.Rows {
			lines = append(lines, "- "+row.Label+": "+row.Value)
		}
		sections = append(sections, strings.Join(lines, "\n"))
	}
	if action := safeHTTPURL(n.ActionURL); n.ActionLabel != "" && action != "" {
		sections = append(sections, n.ActionLabel+":\n"+action)
	}
	for _, link := range n.Links {
		if target := safeHTTPURL(link.URL); link.Label != "" && target != "" {
			sections = append(sections, link.Label+":\n"+target)
		}
	}
	if n.Footer != "" {
		sections = append(sections, n.Footer)
	}
	tail := "Supermetry"
	if base := baseOf(n.BaseURL); base != "" {
		tail += " - " + base
	}
	if n.Reason != "" {
		tail = n.Reason + "\n" + tail
	}
	sections = append(sections, "--\n"+tail)

	return Message{
		To:      to,
		Subject: n.Subject,
		Text:    strings.Join(sections, "\n\n") + "\n",
		HTML:    renderLayout(n),
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
