package mail

import (
	"context"
	"fmt"
	"log/slog"
	"strings"
	"time"
	"unicode"
	"unicode/utf8"
)

// welcomeTimeout bounds one background welcome delivery.
const welcomeTimeout = 30 * time.Second

// cleanName makes a user-supplied name safe for a subject line or a sentence:
// control characters become spaces, runs of space collapse, and the result is
// cut to max runes. HTML escaping is still done by the template.
func cleanName(value string, max int) string {
	value = strings.Map(func(r rune) rune {
		if unicode.IsControl(r) {
			return ' '
		}
		return r
	}, value)
	value = strings.Join(strings.Fields(value), " ")
	if utf8.RuneCountInString(value) > max {
		runes := []rune(value)
		value = strings.TrimSpace(string(runes[:max])) + "…"
	}
	return value
}

func titleCase(value string) string {
	if value == "" {
		return value
	}
	r, size := utf8.DecodeRuneInString(value)
	return string(unicode.ToUpper(r)) + value[size:]
}

// Lifetime renders a link lifetime such as "1 hour" or "7 days".
func Lifetime(d time.Duration) string {
	switch {
	case d >= 48*time.Hour && d%(24*time.Hour) == 0:
		return fmt.Sprintf("%d days", int(d/(24*time.Hour)))
	case d >= 2*time.Hour && d%time.Hour == 0:
		return fmt.Sprintf("%d hours", int(d/time.Hour))
	case d == time.Hour:
		return "1 hour"
	case d == 24*time.Hour:
		return "1 day"
	default:
		return fmt.Sprintf("%d minutes", int(d/time.Minute))
	}
}

// formatTime renders a moment for a details row, or "" for the zero time.
func formatTime(t time.Time) string {
	if t.IsZero() {
		return ""
	}
	return t.UTC().Format("2 Jan 2006, 15:04 UTC")
}

// detailRows builds rows, dropping any with an empty value.
func detailRows(rows ...Row) []Section {
	kept := make([]Row, 0, len(rows))
	for _, row := range rows {
		if row.Value != "" {
			kept = append(kept, row)
		}
	}
	if len(kept) == 0 {
		return nil
	}
	return []Section{{Rows: kept}}
}

// PasswordReset is the email carrying a one-time password reset link. account
// is the address being reset and at the time of the request; both are shown in
// the details panel.
func PasswordReset(baseURL, link, account string, at time.Time, lifetime time.Duration) Notice {
	return Notice{
		Subject:   "Reset your Supermetry password",
		Preheader: "Use this one-time link within " + Lifetime(lifetime) + " to choose a new password.",
		Eyebrow:   "Account security",
		Heading:   "Reset your password",
		Paragraphs: []string{
			"We received a request to reset the password for your Supermetry account. Choose a new one with the button below. The link works once.",
		},
		Sections: detailRows(
			Row{"Account", cleanName(account, 80)},
			Row{"Requested", formatTime(at)},
			Row{"Link expires in", Lifetime(lifetime)},
		),
		ActionLabel: "Choose a new password",
		ActionURL:   link,
		Footer:      "If you did not ask for this, you can safely ignore this email. Your password stays the same.",
		Reason:      "You are receiving this because a password reset was requested for your account.",
		BaseURL:     baseURL,
	}
}

// InvitationInfo describes an invitation for the email.
type InvitationInfo struct {
	OrgName      string
	InviterName  string
	InviterEmail string
	InviteeEmail string
	Role         string
	Lifetime     time.Duration
}

// Invitation is the email inviting someone to an organization.
func Invitation(baseURL, link string, info InvitationInfo) Notice {
	org := cleanName(info.OrgName, 60)
	if org == "" {
		org = "an organization"
	}
	inviter := cleanName(info.InviterName, 60)
	if inviter == "" {
		inviter = cleanName(info.InviterEmail, 80)
	}
	who := "A teammate"
	if inviter != "" {
		who = inviter
	}
	rows := []Row{{Label: "Organization", Value: org}}
	if inviter != "" {
		value := inviter
		if email := cleanName(info.InviterEmail, 80); email != "" && email != inviter {
			value += " (" + email + ")"
		}
		rows = append(rows, Row{Label: "Invited by", Value: value})
	}
	if invitee := cleanName(info.InviteeEmail, 80); invitee != "" {
		rows = append(rows, Row{Label: "Invited address", Value: invitee})
	}
	if info.Role != "" {
		rows = append(rows, Row{Label: "Role", Value: titleCase(cleanName(info.Role, 30))})
	}
	rows = append(rows, Row{Label: "Link expires in", Value: Lifetime(info.Lifetime)})
	return Notice{
		Subject:     "You are invited to join " + org + " on Supermetry",
		Preheader:   who + " invited you to " + org + ". Accept within " + Lifetime(info.Lifetime) + ".",
		Eyebrow:     "Invitation",
		Heading:     "Join " + org + " on Supermetry",
		Paragraphs:  []string{who + " invited you to " + org + " on Supermetry, a privacy-friendly web analytics tool. Accept the invitation to create your account or sign in."},
		Sections:    []Section{{Rows: rows}},
		ActionLabel: "Accept invitation",
		ActionURL:   link,
		Footer:      "If you were not expecting this invitation, you can ignore this email. Nothing happens until you accept.",
		Reason:      "You are receiving this because someone invited this address to Supermetry.",
		BaseURL:     baseURL,
	}
}

// Welcome is the one-time email after an account is first created.
func Welcome(baseURL, name string) Notice {
	heading := "Welcome to Supermetry"
	if clean := cleanName(name, 60); clean != "" {
		heading = "Welcome to Supermetry, " + clean
	}
	return Notice{
		Subject:   "Welcome to Supermetry",
		Preheader: "Your account is ready. Add a site and see your first visit in minutes.",
		Eyebrow:   "Welcome",
		Heading:   heading,
		Paragraphs: []string{
			"Your account is ready. Supermetry is simple, privacy-friendly web analytics, and your data stays on your own server. Here is how to get going:",
		},
		Steps: []Step{
			{Title: "Add your site", Body: "Create a site in the dashboard and enter its domain."},
			{Title: "Paste the snippet", Body: "Copy the tracking snippet into the head of your pages."},
			{Title: "See your first visit", Body: "Open your site once and watch it appear in real time."},
		},
		ActionLabel: "Open your dashboard",
		ActionURL:   baseOf(baseURL) + "/app",
		Links:       []Link{{Label: "Read the documentation", URL: DocsURL}},
		Reason:      "You are receiving this because an account was created with this email address.",
		BaseURL:     baseURL,
	}
}

// ConfirmNewEmail asks the owner of a new address to confirm the change.
func ConfirmNewEmail(baseURL, link, newEmail string, at time.Time, lifetime time.Duration) Notice {
	return Notice{
		Subject:   "Confirm your new Supermetry email",
		Preheader: "Confirm " + cleanName(newEmail, 80) + " as your Supermetry email within " + Lifetime(lifetime) + ".",
		Eyebrow:   "Account",
		Heading:   "Confirm your new email address",
		Paragraphs: []string{
			"Confirm that this address should become the email of your Supermetry account. You must be signed in to Supermetry in this browser. The link works once.",
		},
		Sections: detailRows(
			Row{"New email", cleanName(newEmail, 80)},
			Row{"Requested", formatTime(at)},
			Row{"Link expires in", Lifetime(lifetime)},
		),
		ActionLabel: "Confirm new email",
		ActionURL:   link,
		Footer:      "If you did not request this change, ignore this email. Your address stays as it is.",
		Reason:      "You are receiving this because this address was entered as a new email for a Supermetry account.",
		BaseURL:     baseURL,
	}
}

// EmailChanged tells the old address that the account email changed.
func EmailChanged(baseURL, newEmail string, at time.Time) Notice {
	return Notice{
		Subject:   "Your Supermetry email was changed",
		Preheader: "The email address on your account is now " + cleanName(newEmail, 80) + ".",
		Eyebrow:   "Account security",
		Heading:   "Your email address was changed",
		Paragraphs: []string{
			"The email address of your Supermetry account was changed. From now on, sign in and receive messages with the new address.",
		},
		Sections: detailRows(
			Row{"New email", cleanName(newEmail, 80)},
			Row{"Changed", formatTime(at)},
		),
		Footer:  "If you did not make this change, contact the administrator of your Supermetry instance right away.",
		Reason:  "You are receiving this because this was the email address of the account.",
		BaseURL: baseURL,
	}
}

// SendWelcomeAsync sends the welcome email in the background. It never blocks
// the caller, never fails the caller, and does nothing when mail is not
// configured.
func SendWelcomeAsync(sender Sender, baseURL, to, name string) {
	if sender == nil || !sender.Enabled() || strings.TrimSpace(to) == "" {
		return
	}
	message := Welcome(baseURL, name).Message(to)
	go func() {
		ctx, cancel := context.WithTimeout(context.Background(), welcomeTimeout)
		defer cancel()
		if err := sender.Send(ctx, message); err != nil {
			slog.Warn("send welcome email", "error", err)
		}
	}()
}
