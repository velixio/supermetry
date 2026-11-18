package alerts

import (
	"context"
	"encoding/json"
	"errors"
	"fmt"
	"time"

	"github.com/jackc/pgx/v5"
)

// ErrAlreadyFired means the dedup key of a keyed delivery was recorded before:
// the notification was (or is being) sent and must not be sent again.
var ErrAlreadyFired = errors.New("alert key already fired")

// RuleRepository is the storage the rule evaluator needs beyond Repository.
type RuleRepository interface {
	EnabledRules(ctx context.Context) ([]Alert, error)
	// SaveDeliveryStateWithKey records the dedup key and saves the delivery
	// state in one transaction. It returns ErrAlreadyFired, changing nothing,
	// when the key exists.
	SaveDeliveryStateWithKey(ctx context.Context, alertID int64, state DeliveryState, key string, at time.Time) error
	OpenIncident(ctx context.Context, incident Incident) (Incident, error)
	UpdateIncident(ctx context.Context, incident Incident) error
	OpenIncidents(ctx context.Context, alertID int64) ([]Incident, error)
}

// SaveDeliveryStateWithKey implements RuleRepository.
func (s *Service) SaveDeliveryStateWithKey(ctx context.Context, alertID int64, state DeliveryState, key string, at time.Time) error {
	encoded, err := json.Marshal(state)
	if err != nil {
		return fmt.Errorf("encode alert delivery state: %w", err)
	}
	return s.store.Tx(ctx, func(tx pgx.Tx) error {
		inserted, err := tx.Exec(ctx, `insert into app_alert_fired(alert_id, dedup_key, fired_at) values ($1, $2, $3) on conflict (alert_id, dedup_key) do nothing`, alertID, key, at)
		if err != nil {
			return fmt.Errorf("record alert key: %w", err)
		}
		if inserted.RowsAffected() == 0 {
			return ErrAlreadyFired
		}
		saved, err := tx.Exec(ctx, `update alerts set delivery_state=$2 where id=$1 and enabled`, alertID, encoded)
		if err != nil {
			return fmt.Errorf("save alert delivery state: %w", err)
		}
		if saved.RowsAffected() == 0 {
			return pgx.ErrNoRows
		}
		if _, err := tx.Exec(ctx, `delete from app_alert_fired where alert_id=$1 and fired_at < $2`, alertID, at.Add(-firedRetention)); err != nil {
			return fmt.Errorf("prune alert keys: %w", err)
		}
		return nil
	})
}

const incidentColumns = `id,alert_id,site_id,group_key,severity,opened_at,resolved_at,last_value,peak_value,threshold,notified_at,renotified_at`

func scanIncident(row rowScanner) (Incident, error) {
	var i Incident
	err := row.Scan(&i.ID, &i.AlertID, &i.SiteID, &i.GroupKey, &i.Severity, &i.OpenedAt, &i.ResolvedAt, &i.LastValue, &i.PeakValue, &i.Threshold, &i.NotifiedAt, &i.RenotifiedAt)
	return i, err
}

// OpenIncident inserts an incident; the unique open-group index rejects a
// second open incident for the same alert and group.
func (s *Service) OpenIncident(ctx context.Context, i Incident) (Incident, error) {
	created, err := scanIncident(s.store.Pool.QueryRow(ctx, `insert into alert_incidents(alert_id,site_id,group_key,severity,opened_at,resolved_at,last_value,peak_value,threshold,notified_at,renotified_at)
		values ($1,$2,$3,$4,$5,$6,$7,$8,$9,$10,$11) returning `+incidentColumns,
		i.AlertID, i.SiteID, i.GroupKey, i.Severity, i.OpenedAt, i.ResolvedAt, i.LastValue, i.PeakValue, i.Threshold, i.NotifiedAt, i.RenotifiedAt))
	if err != nil {
		return Incident{}, fmt.Errorf("open incident: %w", err)
	}
	return created, nil
}

func (s *Service) UpdateIncident(ctx context.Context, i Incident) error {
	command, err := s.store.Pool.Exec(ctx, `update alert_incidents set severity=$2,resolved_at=$3,last_value=$4,peak_value=$5,threshold=$6,notified_at=$7,renotified_at=$8 where id=$1`,
		i.ID, i.Severity, i.ResolvedAt, i.LastValue, i.PeakValue, i.Threshold, i.NotifiedAt, i.RenotifiedAt)
	if err != nil {
		return fmt.Errorf("update incident: %w", err)
	}
	if command.RowsAffected() == 0 {
		return pgx.ErrNoRows
	}
	return nil
}

func (s *Service) OpenIncidents(ctx context.Context, alertID int64) ([]Incident, error) {
	rows, err := s.store.Pool.Query(ctx, `select `+incidentColumns+` from alert_incidents where alert_id=$1 and resolved_at is null order by id`, alertID)
	if err != nil {
		return nil, fmt.Errorf("list open incidents: %w", err)
	}
	defer rows.Close()
	var out []Incident
	for rows.Next() {
		i, err := scanIncident(rows)
		if err != nil {
			return nil, err
		}
		out = append(out, i)
	}
	return out, rows.Err()
}

var _ RuleRepository = (*Service)(nil)
