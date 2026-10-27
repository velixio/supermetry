// Package notifications delivers organization-level event notifications
// (Telegram first) to the channels an org's owners configured.
//
// Messages carry repository names, counts and links only. They must never
// contain stargazer logins, emails or other personal data.
package notifications

import "context"

// Kind identifies a class of event an owner can opt into.
type Kind string

const (
	StarMilestone   Kind = "star_milestone"
	StarSpike       Kind = "star_spike"
	UnstarSurge     Kind = "unstar_surge"
	TrendingEntered Kind = "trending_entered"
	TrendingLeft    Kind = "trending_left"
)

// KindInfo describes a registered event kind for the settings UI.
type KindInfo struct {
	Kind        Kind   `json:"kind"`
	Label       string `json:"label"`
	Description string `json:"description"`
}

// registry lists every kind in display order. Add new kinds here; every kind
// is off by default until an owner ticks it.
var registry = []KindInfo{
	{StarMilestone, "Star milestones", "A tracked repository reaches a round star count such as 100, 500 or 1,000."},
	{StarSpike, "Star spikes", "A tracked repository gains stars much faster than usual."},
	{UnstarSurge, "Unstar surges", "A tracked repository loses an unusual number of stars in a short time."},
	{TrendingEntered, "Entered trending", "A tracked repository appears on GitHub trending."},
	{TrendingLeft, "Left trending", "A tracked repository drops off GitHub trending."},
}

// Kinds returns the registered event kinds in display order.
func Kinds() []KindInfo { return append([]KindInfo(nil), registry...) }

// Valid reports whether k is a registered kind.
func Valid(k Kind) bool {
	for _, info := range registry {
		if info.Kind == k {
			return true
		}
	}
	return false
}

// Event is one notification. Key makes it idempotent: the same (org, Kind,
// Key) is delivered at most once, so use something stable such as
// "owner/repo:1000" for a milestone.
type Event struct {
	Kind  Kind
	Key   string
	Title string
	Body  string
}

// Notifier is the interface other packages call. Delivery is best effort:
// Notify never returns an error and does not wait for the network.
type Notifier interface {
	Notify(ctx context.Context, orgID int64, event Event)
}

// Noop discards every event. Use it when notifications are not configured.
type Noop struct{}

func (Noop) Notify(context.Context, int64, Event) {}
