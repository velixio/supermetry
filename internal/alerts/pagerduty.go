package alerts

import (
	"bytes"
	"context"
	"encoding/json"
	"errors"
	"fmt"
	"io"
	"net/http"
	"net/url"
	"regexp"
	"strconv"
	"strings"
	"time"
)

// PagerDutyEnqueueURL is the Events API v2 endpoint.
const PagerDutyEnqueueURL = "https://events.pagerduty.com/v2/enqueue"

const pagerDutySummaryMax = 1024

var pagerDutyRoutingKeyPattern = regexp.MustCompile(`^[A-Za-z0-9]{20,64}$`)

// ValidPagerDutyRoutingKey reports whether key has the shape of a PagerDuty
// Events API v2 integration (routing) key.
func ValidPagerDutyRoutingKey(key string) bool { return pagerDutyRoutingKeyPattern.MatchString(key) }

// permanentError marks a delivery failure that retrying cannot fix, so the
// delivery state settles it as failed at once instead of retrying.
type permanentError struct{ err error }

func (e permanentError) Error() string { return e.err.Error() }
func (e permanentError) Unwrap() error { return e.err }

func isPermanentDelivery(err error) bool {
	var permanent permanentError
	return errors.As(err, &permanent)
}

type pagerDutyEvent struct {
	RoutingKey  string            `json:"routing_key"`
	EventAction string            `json:"event_action"`
	DedupKey    string            `json:"dedup_key"`
	Payload     *pagerDutyPayload `json:"payload,omitempty"`
	Links       []pagerDutyLink   `json:"links,omitempty"`
}

type pagerDutyPayload struct {
	Summary   string `json:"summary"`
	Source    string `json:"source"`
	Severity  string `json:"severity"`
	Timestamp string `json:"timestamp"`
}

type pagerDutyLink struct {
	Href string `json:"href"`
}

// pagerDutyDedupKey ties the trigger and resolve events of one incident (or
// one keyed candidate) together. It holds no secret.
func pagerDutyDedupKey(n Notification) string {
	suffix := ""
	switch {
	case n.Rule != nil && n.Rule.IncidentID != 0:
		suffix = strconv.FormatInt(n.Rule.IncidentID, 10)
	case n.Rule != nil && n.Rule.Key != "":
		suffix = n.Rule.Key
	default:
		suffix = strconv.FormatInt(n.FiredAt.Unix(), 10)
	}
	return "supermetry-" + strconv.FormatInt(n.AlertID, 10) + "-" + suffix
}

func pagerDutySeverity(n Notification) string {
	if n.Rule != nil {
		switch n.Rule.Severity {
		case severityCritical:
			return "critical"
		case "warning":
			return "warning"
		}
	}
	return "error"
}

// BuildPagerDutyEvent renders the Events API v2 body for a notification: a
// trigger, or a resolve when the rule's incident recovered. summary is the
// rendered message text; baseURL (optional) makes the issue link absolute.
// Resolve events carry only the routing key, action and dedup key.
func BuildPagerDutyEvent(routingKey string, n Notification, summary, baseURL string) ([]byte, error) {
	event := pagerDutyEvent{RoutingKey: routingKey, EventAction: "trigger", DedupKey: pagerDutyDedupKey(n)}
	if n.Rule != nil && n.Rule.Status == ruleStatusResolve {
		event.EventAction = "resolve"
		return json.Marshal(event)
	}
	source := "Supermetry"
	if n.App != nil && n.App.AppName != "" {
		source = n.App.AppName
	}
	summary = strings.Join(strings.Fields(summary), " ")
	if summary == "" {
		summary = "Supermetry alert"
	}
	if runes := []rune(summary); len(runes) > pagerDutySummaryMax {
		summary = string(runes[:pagerDutySummaryMax-1]) + "…"
	}
	event.Payload = &pagerDutyPayload{
		Summary: summary, Source: source, Severity: pagerDutySeverity(n),
		Timestamp: n.FiredAt.UTC().Format(time.RFC3339),
	}
	if n.App != nil && strings.HasPrefix(n.App.IssueURL, "/") && baseURL != "" {
		if parsed, err := url.Parse(strings.TrimRight(baseURL, "/") + n.App.IssueURL); err == nil && parsed.Host != "" {
			event.Links = []pagerDutyLink{{Href: parsed.String()}}
		}
	}
	return json.Marshal(event)
}

// SendPagerDuty posts one event body to endpoint in a single attempt, through
// the same guarded transport as webhooks. The delivery state retries pending
// failures, so this does not: 2xx succeeds, 429 and 5xx are transient, any
// other 4xx is permanent. Errors never carry the body (which holds the key).
func (c *WebhookClient) SendPagerDuty(ctx context.Context, endpoint string, body []byte) error {
	parsed, err := url.Parse(endpoint)
	if err != nil || (parsed.Scheme != "http" && parsed.Scheme != "https") || parsed.Hostname() == "" || parsed.User != nil {
		return permanentError{errors.New("invalid PagerDuty endpoint")}
	}
	request, err := http.NewRequestWithContext(ctx, http.MethodPost, endpoint, bytes.NewReader(body))
	if err != nil {
		return permanentError{errors.New("build PagerDuty request")}
	}
	request.Header.Set("Content-Type", "application/json")
	response, err := c.client.Do(request)
	if err != nil {
		if ctx.Err() != nil {
			return ctx.Err()
		}
		return errors.New("PagerDuty request failed")
	}
	_, _ = io.Copy(io.Discard, io.LimitReader(response.Body, 4096))
	_ = response.Body.Close()
	switch {
	case response.StatusCode >= 200 && response.StatusCode < 300:
		return nil
	case response.StatusCode == http.StatusTooManyRequests || response.StatusCode >= 500:
		return fmt.Errorf("PagerDuty returned status %d", response.StatusCode)
	default:
		return permanentError{fmt.Errorf("PagerDuty rejected the event with status %d", response.StatusCode)}
	}
}
