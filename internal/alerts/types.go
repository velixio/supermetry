// Package alerts manages alert rules, notification channels, evaluation, and delivery.
package alerts

import "time"

type ChannelKind string

const (
	ChannelEmail   ChannelKind = "email"
	ChannelWebhook ChannelKind = "webhook"
	ChannelSlack   ChannelKind = "slack"
	ChannelDiscord ChannelKind = "discord"
)

type ChannelConfig struct {
	Recipients []string `json:"recipients,omitempty"`
	Configured bool     `json:"configured"`
}

type Channel struct {
	ID        int64         `json:"id"`
	OrgID     int64         `json:"org_id"`
	Kind      ChannelKind   `json:"kind"`
	Name      string        `json:"name"`
	Config    ChannelConfig `json:"config"`
	CreatedAt time.Time     `json:"created_at"`
}

type ChannelInput struct {
	Kind       ChannelKind `json:"kind"`
	Name       string      `json:"name"`
	Recipients []string    `json:"recipients,omitempty"`
	URL        string      `json:"url,omitempty"`
	Secret     string      `json:"secret,omitempty"`
}

type AlertKind string

const (
	AlertTrafficSpike AlertKind = "traffic_spike"
	AlertTrafficDrop  AlertKind = "traffic_drop"
	AlertGoalReached  AlertKind = "goal_reached"
)

type Alert struct {
	ID          int64      `json:"id"`
	SiteID      int32      `json:"site_id"`
	Kind        AlertKind  `json:"kind"`
	Threshold   float64    `json:"threshold"`
	GoalID      *int64     `json:"goal_id,omitempty"`
	ChannelIDs  []int64    `json:"channel_ids"`
	Enabled     bool       `json:"enabled"`
	LastFiredAt *time.Time `json:"last_fired_at,omitempty"`
	CreatedAt   time.Time  `json:"created_at"`
}

type AlertInput struct {
	Kind       AlertKind `json:"kind"`
	Threshold  float64   `json:"threshold"`
	GoalID     *int64    `json:"goal_id,omitempty"`
	ChannelIDs []int64   `json:"channel_ids"`
	Enabled    *bool     `json:"enabled,omitempty"`
}

type DeliveryChannel struct {
	ID         int64
	Kind       ChannelKind
	Recipients []string
	URL        string
	Secret     string
}

type Notification struct {
	AlertID   int64     `json:"alert_id"`
	SiteID    int32     `json:"site_id"`
	Kind      AlertKind `json:"kind"`
	Value     float64   `json:"value"`
	Threshold float64   `json:"threshold"`
	From      time.Time `json:"from"`
	To        time.Time `json:"to"`
	FiredAt   time.Time `json:"fired_at"`
}
