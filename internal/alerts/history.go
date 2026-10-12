package alerts

import (
	"context"
	"encoding/json"
	"errors"
	"fmt"
	"time"

	"github.com/jackc/pgx/v5"

	"supermetry/internal/platform/audit"
	"supermetry/internal/platform/authz"
	"supermetry/internal/scheduler"
)

const (
	DefaultHistoryRetentionDays = 30
	minHistoryRetentionDays     = 1
	maxHistoryRetentionDays     = 3650
	maxHistoryPageSize          = 100
)

var channelKinds = []ChannelKind{ChannelEmail, ChannelSlack, ChannelDiscord, ChannelTelegram, ChannelWebhook}

func (s *Service) RecordDelivery(ctx context.Context, alertID int64, channel DeliveryChannel, notification Notification, attempts int, status DeliveryStatus, errorText string, at time.Time) error {
	encoded, err := json.Marshal(notification)
	if err != nil {
		return fmt.Errorf("encode delivery notification: %w", err)
	}
	if channel.OrgID == 0 {
		if err := s.store.Pool.QueryRow(ctx, `select org_id from sites where id=$1`, notification.SiteID).Scan(&channel.OrgID); err != nil {
			return fmt.Errorf("resolve delivery organization: %w", err)
		}
	}
	if channel.Name == "" {
		channel.Name = "Notification channel"
	}
	_, err = s.store.Pool.Exec(ctx, `insert into alert_delivery_log
		(org_id,site_id,alert_id,channel_id,channel_kind,channel_name,status,attempts,error_text,notification,fired_at,last_attempt_at,delivered_at,updated_at)
		values($1,$2,$3,$4,$5,$6,$7,$8,$9,$10,$11,case when $8::integer>0 then $12::timestamptz end,case when $7::text='succeeded' then $12::timestamptz end,$12::timestamptz)
		on conflict(alert_id,channel_id,fired_at) do update set
		status=excluded.status,attempts=greatest(alert_delivery_log.attempts,excluded.attempts),error_text=excluded.error_text,
		last_attempt_at=coalesce(excluded.last_attempt_at,alert_delivery_log.last_attempt_at),
		delivered_at=coalesce(excluded.delivered_at,alert_delivery_log.delivered_at),updated_at=excluded.updated_at`,
		channel.OrgID, notification.SiteID, alertID, channel.ID, channel.Kind, channel.Name, status, attempts,
		scrubDeliveryError(channel, errorText), encoded, notification.FiredAt, at)
	if err != nil {
		return fmt.Errorf("record alert delivery: %w", err)
	}
	return nil
}

func (s *Service) ListHistory(ctx context.Context, principal authz.Principal, siteID int32, filter HistoryFilter) ([]DeliveryRecord, error) {
	if !authz.Can(principal, authz.ActionAlertsManage, siteID) {
		return nil, ErrForbidden
	}
	if filter.Limit <= 0 || filter.Limit > maxHistoryPageSize {
		filter.Limit = 50
	}
	rows, err := s.store.Pool.Query(ctx, `select l.id,l.alert_id,l.site_id,l.channel_id,l.channel_kind,l.channel_name,l.status,l.attempts,l.error_text,
		l.fired_at,l.last_attempt_at,l.delivered_at,l.created_at,l.updated_at
		from alert_delivery_log l join sites s on s.id=l.site_id
		where l.site_id=$1 and s.org_id=$2 and s.deleted_at is null
		and ($3='' or l.status=$3) and ($4='' or l.channel_kind=$4) and ($5=0 or l.alert_id=$5)
		order by l.created_at desc,l.id desc limit $6`, siteID, principal.OrgID, filter.Status, filter.ChannelKind, filter.AlertID, filter.Limit)
	if err != nil {
		return nil, fmt.Errorf("list notification history: %w", err)
	}
	defer rows.Close()
	result := make([]DeliveryRecord, 0)
	for rows.Next() {
		var item DeliveryRecord
		if err := rows.Scan(&item.ID, &item.AlertID, &item.SiteID, &item.ChannelID, &item.ChannelKind, &item.ChannelName,
			&item.Status, &item.Attempts, &item.ErrorText, &item.FiredAt, &item.LastAttemptAt, &item.DeliveredAt, &item.CreatedAt, &item.UpdatedAt); err != nil {
			return nil, err
		}
		result = append(result, item)
	}
	return result, rows.Err()
}

func (s *Service) RetryDelivery(ctx context.Context, principal authz.Principal, siteID int32, id int64) (DeliveryRecord, error) {
	if !authz.Can(principal, authz.ActionAlertsManage, siteID) {
		return DeliveryRecord{}, ErrForbidden
	}
	var record DeliveryRecord
	var encoded []byte
	err := s.store.Tx(ctx, func(tx pgx.Tx) error {
		row := tx.QueryRow(ctx, `select l.id,l.alert_id,l.site_id,l.channel_id,l.channel_kind,l.channel_name,l.status,l.attempts,l.error_text,
			l.notification,l.fired_at,l.last_attempt_at,l.delivered_at,l.created_at,l.updated_at
			from alert_delivery_log l join sites s on s.id=l.site_id
			where l.id=$1 and l.site_id=$2 and s.org_id=$3 and s.deleted_at is null for update`, id, siteID, principal.OrgID)
		if err := row.Scan(&record.ID, &record.AlertID, &record.SiteID, &record.ChannelID, &record.ChannelKind, &record.ChannelName,
			&record.Status, &record.Attempts, &record.ErrorText, &encoded, &record.FiredAt, &record.LastAttemptAt, &record.DeliveredAt, &record.CreatedAt, &record.UpdatedAt); err != nil {
			return err
		}
		if record.Status != DeliveryFailed {
			return ErrInvalid
		}
		if err := json.Unmarshal(encoded, &record.Notification); err != nil {
			return fmt.Errorf("decode recorded notification: %w", err)
		}
		command, err := tx.Exec(ctx, `update alert_delivery_log set status='pending',error_text='',updated_at=$2 where id=$1 and status='failed'`, id, s.now())
		if err != nil || command.RowsAffected() != 1 {
			return errors.Join(ErrInvalid, err)
		}
		return nil
	})
	if err != nil {
		return DeliveryRecord{}, err
	}

	channel, deliveryErr := s.deliveryChannel(ctx, record.ChannelID, principal.OrgID)
	if deliveryErr == nil && !channel.Enabled {
		deliveryErr = errors.New("delivery channel is disabled")
	}
	if deliveryErr == nil && s.deliverer == nil {
		deliveryErr = errors.New("alert delivery is unavailable")
	}
	if deliveryErr == nil {
		deliveryErr = s.deliverer.Deliver(ctx, channel, record.Notification)
	}
	record.Attempts++
	record.Status = DeliverySucceeded
	record.ErrorText = ""
	if deliveryErr != nil {
		record.Status = DeliveryFailed
		record.ErrorText = scrubDeliveryError(channel, deliveryErr.Error())
	}
	now := s.now()
	record.LastAttemptAt = &now
	if record.Status == DeliverySucceeded {
		record.DeliveredAt = &now
	}
	err = s.store.Tx(ctx, func(tx pgx.Tx) error {
		_, err := tx.Exec(ctx, `update alert_delivery_log set status=$2,attempts=$3,error_text=$4,last_attempt_at=$5,
			delivered_at=case when $2='succeeded' then $5 else delivered_at end,updated_at=$5 where id=$1 and status='pending'`,
			record.ID, record.Status, record.Attempts, record.ErrorText, now)
		if err != nil {
			return err
		}
		return audit.Write(ctx, tx, principal, "alert_delivery.retried", "alert_delivery", record.ID,
			map[string]any{"site_id": siteID, "status": record.Status, "channel_kind": record.ChannelKind})
	})
	return record, err
}

func (s *Service) ListTemplates(ctx context.Context, principal authz.Principal) ([]MessageTemplate, NotificationSettings, error) {
	if !authz.Can(principal, authz.ActionChannelsRead, 0) {
		return nil, NotificationSettings{}, ErrForbidden
	}
	byKind := make(map[ChannelKind]MessageTemplate)
	rows, err := s.store.Pool.Query(ctx, `select kind,subject_template,body_template,updated_at from notification_templates where org_id=$1`, principal.OrgID)
	if err != nil {
		return nil, NotificationSettings{}, err
	}
	for rows.Next() {
		var item MessageTemplate
		if err := rows.Scan(&item.Kind, &item.Subject, &item.Body, &item.UpdatedAt); err != nil {
			rows.Close()
			return nil, NotificationSettings{}, err
		}
		byKind[item.Kind] = item
	}
	rows.Close()
	items := make([]MessageTemplate, 0, len(channelKinds))
	for _, kind := range channelKinds {
		if item, ok := byKind[kind]; ok {
			items = append(items, item)
			continue
		}
		defaults := DefaultTemplate(kind)
		items = append(items, MessageTemplate{Kind: kind, Subject: defaults.Subject, Body: defaults.Body, IsDefault: true})
	}
	settings := NotificationSettings{HistoryRetentionDays: DefaultHistoryRetentionDays}
	err = s.store.Pool.QueryRow(ctx, `select history_retention_days from notification_settings where org_id=$1`, principal.OrgID).Scan(&settings.HistoryRetentionDays)
	if errors.Is(err, pgx.ErrNoRows) {
		err = nil
	}
	return items, settings, err
}

func (s *Service) SaveTemplate(ctx context.Context, principal authz.Principal, kind ChannelKind, input TemplateInput) (MessageTemplate, error) {
	if !authz.Can(principal, authz.ActionChannelsManage, 0) {
		return MessageTemplate{}, ErrForbidden
	}
	if _, err := RenderTemplate(kind, input, sampleNotification()); err != nil {
		return MessageTemplate{}, err
	}
	var item MessageTemplate
	err := s.store.Tx(ctx, func(tx pgx.Tx) error {
		if err := tx.QueryRow(ctx, `insert into notification_templates(org_id,kind,subject_template,body_template)
			values($1,$2,$3,$4) on conflict(org_id,kind) do update set subject_template=$3,body_template=$4,updated_at=now()
			returning kind,subject_template,body_template,updated_at`, principal.OrgID, kind, input.Subject, input.Body).
			Scan(&item.Kind, &item.Subject, &item.Body, &item.UpdatedAt); err != nil {
			return err
		}
		return audit.Write(ctx, tx, principal, "notification_template.updated", "notification_template", 0, map[string]any{"kind": kind})
	})
	return item, err
}

func (s *Service) ResetTemplate(ctx context.Context, principal authz.Principal, kind ChannelKind) (MessageTemplate, error) {
	if !authz.Can(principal, authz.ActionChannelsManage, 0) || !validChannelKind(kind) {
		return MessageTemplate{}, ErrForbidden
	}
	err := s.store.Tx(ctx, func(tx pgx.Tx) error {
		if _, err := tx.Exec(ctx, `delete from notification_templates where org_id=$1 and kind=$2`, principal.OrgID, kind); err != nil {
			return err
		}
		return audit.Write(ctx, tx, principal, "notification_template.reset", "notification_template", 0, map[string]any{"kind": kind})
	})
	defaults := DefaultTemplate(kind)
	return MessageTemplate{Kind: kind, Subject: defaults.Subject, Body: defaults.Body, IsDefault: true}, err
}

func (s *Service) PreviewTemplate(_ context.Context, principal authz.Principal, kind ChannelKind, input TemplateInput) (RenderedMessage, error) {
	if !authz.Can(principal, authz.ActionChannelsRead, 0) {
		return RenderedMessage{}, ErrForbidden
	}
	return RenderTemplate(kind, input, sampleNotification())
}

func (s *Service) SaveSettings(ctx context.Context, principal authz.Principal, retentionDays int) (NotificationSettings, error) {
	if !authz.Can(principal, authz.ActionChannelsManage, 0) {
		return NotificationSettings{}, ErrForbidden
	}
	if retentionDays < minHistoryRetentionDays || retentionDays > maxHistoryRetentionDays {
		return NotificationSettings{}, ErrInvalid
	}
	err := s.store.Tx(ctx, func(tx pgx.Tx) error {
		if _, err := tx.Exec(ctx, `insert into notification_settings(org_id,history_retention_days) values($1,$2)
			on conflict(org_id) do update set history_retention_days=$2,updated_at=now()`, principal.OrgID, retentionDays); err != nil {
			return err
		}
		return audit.Write(ctx, tx, principal, "notification_settings.updated", "organization", principal.OrgID,
			map[string]any{"history_retention_days": retentionDays})
	})
	return NotificationSettings{HistoryRetentionDays: retentionDays}, err
}

func (s *Service) PurgeHistory(ctx context.Context, now time.Time) (int64, error) {
	command, err := s.store.Pool.Exec(ctx, `with doomed as (
		select l.id from alert_delivery_log l left join notification_settings n on n.org_id=l.org_id
		where l.created_at < $1::timestamptz - make_interval(days => coalesce(n.history_retention_days,30)::int)
		order by l.created_at,l.id limit 5000 for update of l skip locked
	) delete from alert_delivery_log l using doomed where l.id=doomed.id`, now)
	if err != nil {
		return 0, fmt.Errorf("purge notification history: %w", err)
	}
	return command.RowsAffected(), nil
}

func (s *Service) RetentionJob() scheduler.Job {
	return scheduler.Job{Name: "notification-history-retention", Every: time.Hour, Run: func(ctx context.Context) error {
		_, err := s.PurgeHistory(ctx, s.now())
		return err
	}}
}

func sampleNotification() Notification {
	return Notification{AlertID: 42, SiteID: 7, Kind: AlertTrafficSpike, Value: 125, Threshold: 100,
		From: time.Date(2026, 10, 12, 9, 0, 0, 0, time.UTC), To: time.Date(2026, 10, 12, 10, 0, 0, 0, time.UTC),
		FiredAt: time.Date(2026, 10, 12, 10, 0, 0, 0, time.UTC)}
}
