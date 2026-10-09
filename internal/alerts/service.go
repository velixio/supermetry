package alerts

import (
	"context"
	"encoding/base64"
	"encoding/json"
	"errors"
	"fmt"
	"net/mail"
	"net/url"
	"strings"
	"time"

	"github.com/jackc/pgx/v5"

	"panel/internal/platform/audit"
	"panel/internal/platform/authz"
	platformcrypto "panel/internal/platform/crypto"
	"panel/internal/platform/store"
)

var (
	ErrForbidden = errors.New("forbidden")
	ErrInvalid   = errors.New("invalid alert configuration")
	// ErrTestDelivery is safe to return to channel managers after a provider rejects a test.
	ErrTestDelivery = errors.New("test notification could not be delivered; verify the channel credentials and destination")
	// ErrChannelInUse means alerts still reference the channel.
	ErrChannelInUse = errors.New("notification channel is used by one or more alerts")
)

type Service struct {
	store     *store.Store
	box       *platformcrypto.SecretBox
	deliverer Deliverer
	now       func() time.Time
}

func New(data *store.Store, box *platformcrypto.SecretBox, deliverer Deliverer) *Service {
	return &Service{store: data, box: box, deliverer: deliverer, now: time.Now}
}

type storedChannelConfig struct {
	Recipients []string `json:"recipients,omitempty"`
	URL        string   `json:"url,omitempty"`
	Secret     string   `json:"secret,omitempty"`
	BotToken   string   `json:"bot_token,omitempty"`
	ChatID     string   `json:"chat_id,omitempty"`
}

func (s *Service) ListChannels(ctx context.Context, principal authz.Principal) ([]Channel, error) {
	if !authz.Can(principal, authz.ActionChannelsRead, 0) {
		return nil, ErrForbidden
	}
	rows, err := s.store.Pool.Query(ctx, `select id,org_id,kind,name,enabled,config,created_at from notification_channels where org_id=$1 order by id`, principal.OrgID)
	if err != nil {
		return nil, fmt.Errorf("list notification channels: %w", err)
	}
	defer rows.Close()
	channels := make([]Channel, 0)
	for rows.Next() {
		channel, err := scanChannel(rows)
		if err != nil {
			return nil, err
		}
		channels = append(channels, channel)
	}
	if err := rows.Err(); err != nil {
		return nil, fmt.Errorf("iterate notification channels: %w", err)
	}
	return channels, nil
}

func (s *Service) CreateChannel(ctx context.Context, principal authz.Principal, input ChannelInput) (Channel, error) {
	if !authz.Can(principal, authz.ActionChannelsManage, 0) {
		return Channel{}, ErrForbidden
	}
	encoded, err := s.encodeChannel(input, nil)
	if err != nil {
		return Channel{}, err
	}
	var created Channel
	enabled := true
	if input.Enabled != nil {
		enabled = *input.Enabled
	}
	err = s.store.Tx(ctx, func(tx pgx.Tx) error {
		row := tx.QueryRow(ctx, `insert into notification_channels(org_id,kind,name,enabled,config) values ($1,$2,$3,$4,$5) returning id,org_id,kind,name,enabled,config,created_at`, principal.OrgID, input.Kind, strings.TrimSpace(input.Name), enabled, encoded)
		created, err = scanChannel(row)
		if err != nil {
			return fmt.Errorf("create notification channel: %w", err)
		}
		return audit.Write(ctx, tx, principal, "notification_channel.created", "notification_channel", created.ID, map[string]any{"kind": created.Kind})
	})
	return created, err
}

func (s *Service) UpdateChannel(ctx context.Context, principal authz.Principal, id int64, input ChannelInput) (Channel, error) {
	if !authz.Can(principal, authz.ActionChannelsManage, 0) {
		return Channel{}, ErrForbidden
	}
	var updated Channel
	err := s.store.Tx(ctx, func(tx pgx.Tx) error {
		var currentKind ChannelKind
		var currentEnabled bool
		var raw []byte
		if err := tx.QueryRow(ctx, `select kind,enabled,config from notification_channels where id=$1 and org_id=$2 for update`, id, principal.OrgID).Scan(&currentKind, &currentEnabled, &raw); err != nil {
			return err
		}
		var current storedChannelConfig
		if err := json.Unmarshal(raw, &current); err != nil {
			return errors.New("decode channel config")
		}
		var existing *storedChannelConfig
		if currentKind == input.Kind {
			existing = &current
		}
		encoded, err := s.encodeChannel(input, existing)
		if err != nil {
			return err
		}
		enabled := currentEnabled
		if input.Enabled != nil {
			enabled = *input.Enabled
		}
		row := tx.QueryRow(ctx, `update notification_channels set kind=$3,name=$4,enabled=$5,config=$6 where id=$1 and org_id=$2 returning id,org_id,kind,name,enabled,config,created_at`, id, principal.OrgID, input.Kind, strings.TrimSpace(input.Name), enabled, encoded)
		updated, err = scanChannel(row)
		if err != nil {
			return fmt.Errorf("update notification channel: %w", err)
		}
		return audit.Write(ctx, tx, principal, "notification_channel.updated", "notification_channel", id, map[string]any{"kind": updated.Kind})
	})
	return updated, err
}

func (s *Service) DeleteChannel(ctx context.Context, principal authz.Principal, id int64) error {
	if !authz.Can(principal, authz.ActionChannelsManage, 0) {
		return ErrForbidden
	}
	return s.store.Tx(ctx, func(tx pgx.Tx) error {
		var inUse bool
		if err := tx.QueryRow(ctx, `select exists(select 1 from alerts a join sites s on s.id=a.site_id where s.org_id=$2 and $1=any(a.channel_ids))`, id, principal.OrgID).Scan(&inUse); err != nil {
			return fmt.Errorf("check notification channel usage: %w", err)
		}
		if inUse {
			return ErrChannelInUse
		}
		command, err := tx.Exec(ctx, `delete from notification_channels where id=$1 and org_id=$2`, id, principal.OrgID)
		if err != nil {
			return fmt.Errorf("delete notification channel: %w", err)
		}
		if command.RowsAffected() == 0 {
			return pgx.ErrNoRows
		}
		return audit.Write(ctx, tx, principal, "notification_channel.deleted", "notification_channel", id, nil)
	})
}

func (s *Service) TestChannel(ctx context.Context, principal authz.Principal, id int64) error {
	if !authz.Can(principal, authz.ActionChannelsManage, 0) {
		return ErrForbidden
	}
	if s.deliverer == nil {
		return errors.New("alert delivery is unavailable")
	}
	channel, err := s.deliveryChannel(ctx, id, principal.OrgID)
	if err != nil {
		return err
	}
	now := s.now()
	if err := s.deliverer.Deliver(ctx, channel, Notification{Kind: AlertTrafficSpike, FiredAt: now}); err != nil {
		return fmt.Errorf("%w: %v", ErrTestDelivery, err)
	}
	return s.store.Tx(ctx, func(tx pgx.Tx) error {
		return audit.Write(ctx, tx, principal, "notification_channel.tested", "notification_channel", id, nil)
	})
}

func (s *Service) ListAlerts(ctx context.Context, principal authz.Principal, siteID int32) ([]Alert, error) {
	if !authz.Can(principal, authz.ActionAlertsManage, siteID) {
		return nil, ErrForbidden
	}
	rows, err := s.store.Pool.Query(ctx, `select a.id,a.site_id,a.kind,a.threshold,a.goal_id,a.channel_ids,a.enabled,a.last_fired_at,a.created_at from alerts a join sites s on s.id=a.site_id where a.site_id=$1 and s.org_id=$2 and s.deleted_at is null order by a.id`, siteID, principal.OrgID)
	if err != nil {
		return nil, fmt.Errorf("list alerts: %w", err)
	}
	defer rows.Close()
	alerts := make([]Alert, 0)
	for rows.Next() {
		alert, err := scanAlert(rows)
		if err != nil {
			return nil, err
		}
		alerts = append(alerts, alert)
	}
	if err := rows.Err(); err != nil {
		return nil, fmt.Errorf("iterate alerts: %w", err)
	}
	return alerts, nil
}

func (s *Service) CreateAlert(ctx context.Context, principal authz.Principal, siteID int32, input AlertInput) (Alert, error) {
	if !authz.Can(principal, authz.ActionAlertsManage, siteID) {
		return Alert{}, ErrForbidden
	}
	if err := validateAlertInput(input); err != nil {
		return Alert{}, err
	}
	enabled := true
	if input.Enabled != nil {
		enabled = *input.Enabled
	}
	var created Alert
	err := s.store.Tx(ctx, func(tx pgx.Tx) error {
		if err := validateAlertRelations(ctx, tx, principal.OrgID, siteID, input); err != nil {
			return err
		}
		var err error
		created, err = scanAlert(tx.QueryRow(ctx, `insert into alerts(site_id,kind,threshold,goal_id,channel_ids,enabled) values ($1,$2,$3,$4,$5,$6) returning id,site_id,kind,threshold,goal_id,channel_ids,enabled,last_fired_at,created_at`, siteID, input.Kind, input.Threshold, input.GoalID, input.ChannelIDs, enabled))
		if err != nil {
			return fmt.Errorf("create alert: %w", err)
		}
		return audit.Write(ctx, tx, principal, "alert.created", "alert", created.ID, map[string]any{"site_id": siteID, "kind": created.Kind})
	})
	return created, err
}

func (s *Service) UpdateAlert(ctx context.Context, principal authz.Principal, siteID int32, id int64, input AlertInput) (Alert, error) {
	if err := validateAlertInput(input); err != nil {
		return Alert{}, err
	}
	var updated Alert
	err := s.store.Tx(ctx, func(tx pgx.Tx) error {
		var exists bool
		if err := tx.QueryRow(ctx, `select exists(select 1 from alerts a join sites s on s.id=a.site_id where a.id=$1 and a.site_id=$2 and s.org_id=$3 and s.deleted_at is null)`, id, siteID, principal.OrgID).Scan(&exists); err != nil {
			return err
		}
		if !exists {
			return pgx.ErrNoRows
		}
		if !authz.Can(principal, authz.ActionAlertsManage, siteID) {
			return ErrForbidden
		}
		if err := validateAlertRelations(ctx, tx, principal.OrgID, siteID, input); err != nil {
			return err
		}
		var enabled bool
		if input.Enabled == nil {
			if err := tx.QueryRow(ctx, `select enabled from alerts where id=$1`, id).Scan(&enabled); err != nil {
				return err
			}
		} else {
			enabled = *input.Enabled
		}
		var err error
		updated, err = scanAlert(tx.QueryRow(ctx, `update alerts set kind=$2,threshold=$3,goal_id=$4,channel_ids=$5,enabled=$6 where id=$1 returning id,site_id,kind,threshold,goal_id,channel_ids,enabled,last_fired_at,created_at`, id, input.Kind, input.Threshold, input.GoalID, input.ChannelIDs, enabled))
		if err != nil {
			return fmt.Errorf("update alert: %w", err)
		}
		return audit.Write(ctx, tx, principal, "alert.updated", "alert", id, map[string]any{"site_id": siteID, "kind": updated.Kind})
	})
	return updated, err
}

func (s *Service) DeleteAlert(ctx context.Context, principal authz.Principal, siteID int32, id int64) error {
	return s.store.Tx(ctx, func(tx pgx.Tx) error {
		var exists bool
		if err := tx.QueryRow(ctx, `select exists(select 1 from alerts a join sites s on s.id=a.site_id where a.id=$1 and a.site_id=$2 and s.org_id=$3 and s.deleted_at is null)`, id, siteID, principal.OrgID).Scan(&exists); err != nil {
			return err
		}
		if !exists {
			return pgx.ErrNoRows
		}
		if !authz.Can(principal, authz.ActionAlertsManage, siteID) {
			return ErrForbidden
		}
		if _, err := tx.Exec(ctx, `delete from alerts where id=$1`, id); err != nil {
			return fmt.Errorf("delete alert: %w", err)
		}
		return audit.Write(ctx, tx, principal, "alert.deleted", "alert", id, map[string]any{"site_id": siteID})
	})
}

func (s *Service) EnabledAlerts(ctx context.Context) ([]Alert, error) {
	rows, err := s.store.Pool.Query(ctx, `select a.id,a.site_id,a.kind,a.threshold,a.goal_id,a.channel_ids,a.enabled,a.last_fired_at,a.created_at from alerts a join sites s on s.id=a.site_id where a.enabled and s.deleted_at is null order by a.id`)
	if err != nil {
		return nil, fmt.Errorf("load enabled alerts: %w", err)
	}
	defer rows.Close()
	result := make([]Alert, 0)
	for rows.Next() {
		alert, err := scanAlert(rows)
		if err != nil {
			return nil, err
		}
		result = append(result, alert)
	}
	if err := rows.Err(); err != nil {
		return nil, fmt.Errorf("iterate enabled alerts: %w", err)
	}
	return result, nil
}

func (s *Service) DeliveryChannels(ctx context.Context, ids []int64) ([]DeliveryChannel, error) {
	rows, err := s.store.Pool.Query(ctx, `select id,kind,enabled,config from notification_channels where id=any($1)`, ids)
	if err != nil {
		return nil, fmt.Errorf("load delivery channels: %w", err)
	}
	defer rows.Close()
	byID := make(map[int64]DeliveryChannel, len(ids))
	for rows.Next() {
		channel, err := s.scanDeliveryChannel(rows)
		if err != nil {
			return nil, err
		}
		byID[channel.ID] = channel
	}
	if err := rows.Err(); err != nil {
		return nil, fmt.Errorf("iterate delivery channels: %w", err)
	}
	result := make([]DeliveryChannel, 0, len(ids))
	for _, id := range ids {
		if channel, ok := byID[id]; ok {
			result = append(result, channel)
		}
	}
	return result, nil
}

func (s *Service) MarkFired(ctx context.Context, id int64, at time.Time) error {
	command, err := s.store.Pool.Exec(ctx, `update alerts set last_fired_at=$2 where id=$1 and enabled`, id, at)
	if err != nil {
		return fmt.Errorf("mark alert fired: %w", err)
	}
	if command.RowsAffected() == 0 {
		return pgx.ErrNoRows
	}
	return nil
}

func (s *Service) encodeChannel(input ChannelInput, existing *storedChannelConfig) ([]byte, error) {
	input.Name = strings.TrimSpace(input.Name)
	if input.Name == "" || s.box == nil {
		return nil, ErrInvalid
	}
	config := storedChannelConfig{}
	switch input.Kind {
	case ChannelEmail:
		if len(input.Recipients) == 0 || input.URL != "" || input.Secret != "" {
			return nil, ErrInvalid
		}
		for _, recipient := range input.Recipients {
			address, err := mail.ParseAddress(recipient)
			if err != nil || address.Address != recipient {
				return nil, ErrInvalid
			}
		}
		config.Recipients = append([]string(nil), input.Recipients...)
	case ChannelWebhook, ChannelSlack, ChannelDiscord:
		parsed, err := url.ParseRequestURI(input.URL)
		if err != nil || (parsed.Scheme != "http" && parsed.Scheme != "https") || parsed.Host == "" || len(input.Recipients) != 0 {
			return nil, ErrInvalid
		}
		config.URL = base64.StdEncoding.EncodeToString(s.box.Seal([]byte(input.URL)))
		if input.Kind == ChannelWebhook && input.Secret != "" {
			config.Secret = base64.StdEncoding.EncodeToString(s.box.Seal([]byte(input.Secret)))
		} else if input.Secret != "" {
			return nil, ErrInvalid
		}
	case ChannelTelegram:
		input.ChatID = strings.TrimSpace(input.ChatID)
		if len(input.Recipients) != 0 || input.URL != "" || input.Secret != "" || !ValidTelegramChatID(input.ChatID) {
			return nil, ErrInvalid
		}
		config.ChatID = input.ChatID
		if input.BotToken != "" {
			if !ValidTelegramToken(input.BotToken) {
				return nil, ErrInvalid
			}
			config.BotToken = base64.StdEncoding.EncodeToString(s.box.Seal([]byte(input.BotToken)))
		} else if existing != nil && existing.BotToken != "" {
			config.BotToken = existing.BotToken
		} else {
			return nil, ErrInvalid
		}
	default:
		return nil, ErrInvalid
	}
	encoded, err := json.Marshal(config)
	if err != nil {
		return nil, fmt.Errorf("encode channel config: %w", err)
	}
	return encoded, nil
}

type rowScanner interface{ Scan(...any) error }

func scanChannel(row rowScanner) (Channel, error) {
	var channel Channel
	var encoded []byte
	if err := row.Scan(&channel.ID, &channel.OrgID, &channel.Kind, &channel.Name, &channel.Enabled, &encoded, &channel.CreatedAt); err != nil {
		return Channel{}, err
	}
	var config storedChannelConfig
	if err := json.Unmarshal(encoded, &config); err != nil {
		return Channel{}, fmt.Errorf("decode channel config: %w", err)
	}
	channel.Config = ChannelConfig{
		Recipients: append([]string(nil), config.Recipients...), ChatID: config.ChatID,
		Configured: len(config.Recipients) > 0 || config.URL != "" || (config.BotToken != "" && config.ChatID != ""),
	}
	return channel, nil
}

func scanAlert(row rowScanner) (Alert, error) {
	var alert Alert
	if err := row.Scan(&alert.ID, &alert.SiteID, &alert.Kind, &alert.Threshold, &alert.GoalID, &alert.ChannelIDs, &alert.Enabled, &alert.LastFiredAt, &alert.CreatedAt); err != nil {
		return Alert{}, err
	}
	return alert, nil
}

func (s *Service) deliveryChannel(ctx context.Context, id, orgID int64) (DeliveryChannel, error) {
	return s.scanDeliveryChannel(s.store.Pool.QueryRow(ctx, `select id,kind,enabled,config from notification_channels where id=$1 and org_id=$2`, id, orgID))
}

func (s *Service) scanDeliveryChannel(row rowScanner) (DeliveryChannel, error) {
	var channel DeliveryChannel
	var encoded []byte
	if err := row.Scan(&channel.ID, &channel.Kind, &channel.Enabled, &encoded); err != nil {
		return DeliveryChannel{}, err
	}
	var config storedChannelConfig
	if err := json.Unmarshal(encoded, &config); err != nil {
		return DeliveryChannel{}, fmt.Errorf("decode channel config: %w", err)
	}
	channel.Recipients = append([]string(nil), config.Recipients...)
	var err error
	if config.URL != "" {
		channel.URL, err = s.openConfig(config.URL)
		if err != nil {
			return DeliveryChannel{}, err
		}
	}
	if config.Secret != "" {
		channel.Secret, err = s.openConfig(config.Secret)
		if err != nil {
			return DeliveryChannel{}, err
		}
	}
	channel.ChatID = config.ChatID
	if config.BotToken != "" {
		channel.BotToken, err = s.openConfig(config.BotToken)
		if err != nil {
			return DeliveryChannel{}, err
		}
	}
	return channel, nil
}

func (s *Service) openConfig(value string) (string, error) {
	sealed, err := base64.StdEncoding.DecodeString(value)
	if err != nil {
		return "", errors.New("decode encrypted channel config")
	}
	plain, err := s.box.Open(sealed)
	if err != nil {
		return "", err
	}
	return string(plain), nil
}

func validateAlertInput(input AlertInput) error {
	if input.Threshold <= 0 || len(input.ChannelIDs) == 0 {
		return ErrInvalid
	}
	seen := make(map[int64]bool, len(input.ChannelIDs))
	for _, id := range input.ChannelIDs {
		if id <= 0 || seen[id] {
			return ErrInvalid
		}
		seen[id] = true
	}
	switch input.Kind {
	case AlertGoalReached:
		if input.GoalID == nil || *input.GoalID <= 0 {
			return ErrInvalid
		}
	case AlertTrafficSpike, AlertTrafficDrop:
		if input.GoalID != nil {
			return ErrInvalid
		}
	default:
		return ErrInvalid
	}
	return nil
}

func validateAlertRelations(ctx context.Context, tx pgx.Tx, orgID int64, siteID int32, input AlertInput) error {
	var siteExists bool
	if err := tx.QueryRow(ctx, `select exists(select 1 from sites where id=$1 and org_id=$2 and deleted_at is null)`, siteID, orgID).Scan(&siteExists); err != nil {
		return err
	}
	if !siteExists {
		return pgx.ErrNoRows
	}
	var channelCount int
	if err := tx.QueryRow(ctx, `select count(*) from notification_channels where org_id=$1 and id=any($2)`, orgID, input.ChannelIDs).Scan(&channelCount); err != nil {
		return err
	}
	if channelCount != len(input.ChannelIDs) {
		return ErrInvalid
	}
	if input.GoalID != nil {
		var goalExists bool
		if err := tx.QueryRow(ctx, `select exists(select 1 from goals where id=$1 and site_id=$2)`, *input.GoalID, siteID).Scan(&goalExists); err != nil {
			return err
		}
		if !goalExists {
			return ErrInvalid
		}
	}
	return nil
}

var _ Repository = (*Service)(nil)
