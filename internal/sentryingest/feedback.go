package sentryingest

import (
	"encoding/json"
	"errors"
	"strings"
	"time"

	"supermetry/internal/appissues"
	"supermetry/internal/sites"
)

// MaxFeedbackBytes caps one feedback or user_report item.
const MaxFeedbackBytes = 64 << 10

const (
	maxFeedbackMessage = 5000
	maxFeedbackURL     = 2048
)

// FeedbackSink takes scrubbed user reports for storage; Add must not block
// and reports false when the report was dropped.
type FeedbackSink interface {
	Add(appissues.FeedbackItem) bool
}

// ParseFeedback turns a "feedback" item (the modern widget and
// captureFeedback payload) or a legacy "user_report" item into a scrubbed
// report. The message goes through ScrubText, the URL is reduced to its path,
// and the contact email is kept only as a keyed hash (never raw) when the
// project already has its secret. The reporter's name is read by neither
// format: names are not kept until the opt-in exists.
func ParseFeedback(itemType string, payload []byte, site sites.Site, now time.Time) (appissues.FeedbackItem, error) {
	var in struct {
		EventID  string `json:"event_id"`
		Email    string `json:"email"`
		Comments string `json:"comments"`
		Contexts struct {
			Feedback struct {
				Message           string `json:"message"`
				ContactEmail      string `json:"contact_email"`
				AssociatedEventID string `json:"associated_event_id"`
				URL               string `json:"url"`
			} `json:"feedback"`
		} `json:"contexts"`
	}
	if len(payload) > MaxFeedbackBytes {
		return appissues.FeedbackItem{}, ErrInvalid
	}
	if err := json.Unmarshal(payload, &in); err != nil {
		return appissues.FeedbackItem{}, ErrInvalid
	}
	var message, email, eventID, link string
	if itemType == "user_report" {
		message, email, eventID = in.Comments, in.Email, in.EventID
	} else {
		fb := in.Contexts.Feedback
		message, email, eventID, link = fb.Message, fb.ContactEmail, fb.AssociatedEventID, fb.URL
	}
	message = strings.TrimSpace(scrubClip(message, maxFeedbackMessage))
	if message == "" {
		return appissues.FeedbackItem{}, errors.Join(ErrInvalid, errors.New("empty feedback message"))
	}
	it := appissues.FeedbackItem{SiteID: site.ID, At: now, EventID: normaliseEventID(eventID), Message: message}
	if link != "" {
		it.URL = clip(urlPath(link), maxFeedbackURL)
	}
	if email = strings.ToLower(strings.TrimSpace(clip(email, 254))); email != "" && site.App != nil {
		// The "email:" prefix keeps the MAC apart from user-id hashes.
		it.EmailHash = HashUser(site.App.HashSecret, "email:"+email)
	}
	return it, nil
}

// normaliseEventID returns a 32-character lowercase hex event id (dashes
// allowed on input), or "" for anything else.
func normaliseEventID(s string) string {
	s = strings.ToLower(strings.ReplaceAll(strings.TrimSpace(s), "-", ""))
	if len(s) != 32 {
		return ""
	}
	for i := 0; i < len(s); i++ {
		if c := s[i]; !(c >= '0' && c <= '9' || c >= 'a' && c <= 'f') {
			return ""
		}
	}
	return s
}
