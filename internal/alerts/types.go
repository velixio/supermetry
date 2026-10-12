// Package alerts manages alert rules, notification channels, evaluation, and delivery.
package alerts

import "time"

type ChannelKind string

const (
	ChannelEmail    ChannelKind = "email"
	ChannelWebhook  ChannelKind = "webhook"
	ChannelSlack    ChannelKind = "slack"
	ChannelDiscord  ChannelKind = "discord"
	ChannelTelegram ChannelKind = "telegram"
)

type ChannelConfig struct {
	Recipients []string `json:"recipients,omitempty"`
	ChatID     string   `json:"chat_id,omitempty"`
	Configured bool     `json:"configured"`
}

type Channel struct {
	ID        int64         `json:"id"`
	OrgID     int64         `json:"org_id"`
	Kind      ChannelKind   `json:"kind"`
	Name      string        `json:"name"`
	Enabled   bool          `json:"enabled"`
	Config    ChannelConfig `json:"config"`
	CreatedAt time.Time     `json:"created_at"`
}

type ChannelInput struct {
	Kind       ChannelKind `json:"kind"`
	Name       string      `json:"name"`
	Recipients []string    `json:"recipients,omitempty"`
	URL        string      `json:"url,omitempty"`
	Secret     string      `json:"secret,omitempty"`
	BotToken   string      `json:"bot_token,omitempty"`
	ChatID     string      `json:"chat_id,omitempty"`
	Enabled    *bool       `json:"enabled,omitempty"`
}

type AlertKind string

const (
	AlertTrafficSpike AlertKind = "traffic_spike"
	AlertTrafficDrop  AlertKind = "traffic_drop"
	AlertGoalReached  AlertKind = "goal_reached"
)

type Alert struct {
	ID            int64          `json:"id"`
	SiteID        int32          `json:"site_id"`
	Kind          AlertKind      `json:"kind"`
	Threshold     float64        `json:"threshold"`
	GoalID        *int64         `json:"goal_id,omitempty"`
	ChannelIDs    []int64        `json:"channel_ids"`
	Enabled       bool           `json:"enabled"`
	LastFiredAt   *time.Time     `json:"last_fired_at,omitempty"`
	CreatedAt     time.Time      `json:"created_at"`
	DeliveryState *DeliveryState `json:"-"`
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
	OrgID      int64
	Kind       ChannelKind
	Name       string
	Enabled    bool
	Recipients []string
	URL        string
	Secret     string
	BotToken   string
	ChatID     string
	Template   TemplateInput
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

// DeliveryState is the durable progress for one fired evaluation. ChannelIDs
// captures the enabled targets at firing time so later configuration changes
// cannot replay the notification to a newly enabled channel. DeliveredChannelIDs
// contains every settled target, including disabled, deleted, or permanently
// failed channels that must not be retried.
type DeliveryState struct {
	Notification        Notification  `json:"notification"`
	ChannelIDs          []int64       `json:"channel_ids"`
	DeliveredChannelIDs []int64       `json:"delivered_channel_ids"`
	AttemptCounts       map[int64]int `json:"attempt_counts,omitempty"`
}

type DeliveryStatus string

const (
	DeliveryPending   DeliveryStatus = "pending"
	DeliverySucceeded DeliveryStatus = "succeeded"
	DeliveryFailed    DeliveryStatus = "failed"
)

type DeliveryRecord struct {
	ID            int64          `json:"id"`
	AlertID       int64          `json:"alert_id"`
	SiteID        int32          `json:"site_id"`
	ChannelID     int64          `json:"channel_id"`
	ChannelKind   ChannelKind    `json:"channel_kind"`
	ChannelName   string         `json:"channel_name"`
	Status        DeliveryStatus `json:"status"`
	Attempts      int            `json:"attempts"`
	ErrorText     string         `json:"error_text,omitempty"`
	Notification  Notification   `json:"-"`
	FiredAt       time.Time      `json:"fired_at"`
	LastAttemptAt *time.Time     `json:"last_attempt_at,omitempty"`
	DeliveredAt   *time.Time     `json:"delivered_at,omitempty"`
	CreatedAt     time.Time      `json:"created_at"`
	UpdatedAt     time.Time      `json:"updated_at"`
}

type HistoryFilter struct {
	Status      DeliveryStatus
	ChannelKind ChannelKind
	AlertID     int64
	Limit       int
}

type NotificationSettings struct {
	HistoryRetentionDays int `json:"history_retention_days"`
}
