package alerts

import (
	"context"
	"encoding/json"
	"fmt"
	"slices"
	"strconv"
	"strings"
	"time"

	"github.com/jackc/pgx/v5"

	"supermetry/internal/platform/audit"
	"supermetry/internal/platform/authz"
)

const (
	SilenceKindSilence     = "silence"
	SilenceKindMaintenance = "maintenance"

	// MaxSilencesPerSite caps the active and future silences of a site.
	MaxSilencesPerSite = 50
	maxSilenceReason   = 200
	maxSilenceSpan     = 366 * 24 * time.Hour

	DeliverySuppressed DeliveryStatus = "suppressed"
	// ChannelNone is the channel kind of a suppressed history row, which has no channel.
	ChannelNone ChannelKind = "none"

	suppressedKeyPrefix = "suppressed:"
)

// Silence mutes the alert rules of a site: one rule (AlertID set) or all of
// them. A silence is a fixed interval; a maintenance window recurs weekly at a
// wall-clock time in its own time zone, bounded by StartsAt and EndsAt.
type Silence struct {
	ID              int64      `json:"id"`
	SiteID          int32      `json:"site_id"`
	AlertID         *int64     `json:"alert_id,omitempty"`
	Kind            string     `json:"kind"`
	StartsAt        time.Time  `json:"starts_at"`
	EndsAt          *time.Time `json:"ends_at,omitempty"`
	Weekdays        []int16    `json:"weekdays,omitempty"`   // 0 = Sunday
	StartTime       string     `json:"start_time,omitempty"` // HH:MM wall clock
	DurationMinutes int        `json:"duration_minutes,omitempty"`
	TZ              string     `json:"tz,omitempty"`
	Reason          string     `json:"reason"`
	CreatedBy       *int64     `json:"created_by,omitempty"`
	CreatedAt       time.Time  `json:"created_at"`
}

// SilenceInput creates a silence or a maintenance window.
type SilenceInput struct {
	AlertID         *int64     `json:"alert_id,omitempty"`
	Kind            string     `json:"kind"`
	StartsAt        *time.Time `json:"starts_at,omitempty"` // default now
	EndsAt          *time.Time `json:"ends_at,omitempty"`
	Weekdays        []int16    `json:"weekdays,omitempty"`
	StartTime       string     `json:"start_time,omitempty"`
	DurationMinutes int        `json:"duration_minutes,omitempty"`
	TZ              string     `json:"tz,omitempty"` // default the site time zone
	Reason          string     `json:"reason,omitempty"`
}

// ActiveAt reports whether the silence mutes at t. A maintenance window starts
// at its local wall-clock time and lasts its duration in absolute time, so a
// DST switch inside the window neither shortens nor stretches it. Windows that
// began the previous local day (overnight) are covered.
func (s Silence) ActiveAt(t time.Time) bool {
	if t.Before(s.StartsAt) || (s.EndsAt != nil && !t.Before(*s.EndsAt)) {
		return false
	}
	if s.Kind != SilenceKindMaintenance {
		return true
	}
	loc, err := time.LoadLocation(s.TZ)
	if err != nil {
		return false
	}
	hour, minute, ok := parseClock(s.StartTime)
	if !ok {
		return false
	}
	local := t.In(loc)
	for back := 0; back <= 1; back++ {
		day := local.AddDate(0, 0, -back)
		if !slices.Contains(s.Weekdays, int16(day.Weekday())) {
			continue
		}
		start := time.Date(day.Year(), day.Month(), day.Day(), hour, minute, 0, 0, loc)
		end := start.Add(time.Duration(s.DurationMinutes) * time.Minute)
		if !t.Before(start) && t.Before(end) {
			return true
		}
	}
	return false
}

func parseClock(value string) (hour, minute int, ok bool) {
	h, m, found := strings.Cut(value, ":")
	if !found || len(h) != 2 || len(m) != 2 {
		return 0, 0, false
	}
	hh, err1 := strconv.Atoi(h)
	mm, err2 := strconv.Atoi(m)
	if err1 != nil || err2 != nil || hh < 0 || hh > 23 || mm < 0 || mm > 59 {
		return 0, 0, false
	}
	return hh, mm, true
}

// Silenced returns the first silence that mutes alertID at t: one scoped to
// that alert or a site-wide one.
func Silenced(list []Silence, alertID int64, t time.Time) (Silence, bool) {
	for _, s := range list {
		if (s.AlertID == nil || *s.AlertID == alertID) && s.ActiveAt(t) {
			return s, true
		}
	}
	return Silence{}, false
}

// SilenceRepository is the storage the rule evaluator needs for silences.
type SilenceRepository interface {
	// ActiveSilences lists the silences of a site that have not ended at now.
	ActiveSilences(ctx context.Context, siteID int32, now time.Time) ([]Silence, error)
	// RecordSuppressed logs one suppressed notification. With commit the
	// dedup key is recorded as fired (the candidate is gone for good);
	// without it only a marker is, so a withheld candidate is logged once and
	// stays eligible for delivery after the silence. It reports false, writing
	// nothing, when the key or marker already exists.
	RecordSuppressed(ctx context.Context, alert Alert, key string, commit bool, n Notification, reason string, at time.Time) (bool, error)
}

const silenceColumns = `id,site_id,alert_id,kind,starts_at,ends_at,weekdays,to_char(start_time,'HH24:MI'),duration_minutes,tz,reason,created_by,created_at`

func scanSilence(row rowScanner) (Silence, error) {
	var s Silence
	var startTime, tz *string
	var duration *int32
	err := row.Scan(&s.ID, &s.SiteID, &s.AlertID, &s.Kind, &s.StartsAt, &s.EndsAt, &s.Weekdays, &startTime, &duration, &tz, &s.Reason, &s.CreatedBy, &s.CreatedAt)
	if startTime != nil {
		s.StartTime = *startTime
	}
	if duration != nil {
		s.DurationMinutes = int(*duration)
	}
	if tz != nil {
		s.TZ = *tz
	}
	return s, err
}

// ActiveSilences implements SilenceRepository.
func (s *Service) ActiveSilences(ctx context.Context, siteID int32, now time.Time) ([]Silence, error) {
	rows, err := s.store.Pool.Query(ctx, `select `+silenceColumns+` from alert_silences where site_id=$1 and (ends_at is null or ends_at > $2) order by id`, siteID, now)
	if err != nil {
		return nil, fmt.Errorf("load silences: %w", err)
	}
	defer rows.Close()
	var out []Silence
	for rows.Next() {
		item, err := scanSilence(rows)
		if err != nil {
			return nil, err
		}
		out = append(out, item)
	}
	return out, rows.Err()
}

// RecordSuppressed implements SilenceRepository.
func (s *Service) RecordSuppressed(ctx context.Context, alert Alert, key string, commit bool, n Notification, reason string, at time.Time) (bool, error) {
	dedupKey := key
	if !commit {
		dedupKey = suppressedKeyPrefix + key
	}
	encoded, err := json.Marshal(n)
	if err != nil {
		return false, fmt.Errorf("encode suppressed notification: %w", err)
	}
	text := "silenced"
	if reason != "" {
		text += ": " + reason
	}
	recorded := false
	err = s.store.Tx(ctx, func(tx pgx.Tx) error {
		inserted, err := tx.Exec(ctx, `insert into app_alert_fired(alert_id,dedup_key,fired_at) values ($1,$2,$3) on conflict (alert_id,dedup_key) do nothing`, alert.ID, dedupKey, at)
		if err != nil {
			return fmt.Errorf("record suppressed key: %w", err)
		}
		if inserted.RowsAffected() == 0 {
			return nil
		}
		if commit {
			// A committed key is gone for good; the marker tells a later
			// recovery that this notice was never sent.
			if _, err := tx.Exec(ctx, `insert into app_alert_fired(alert_id,dedup_key,fired_at) values ($1,$2,$3) on conflict (alert_id,dedup_key) do nothing`, alert.ID, suppressedKeyPrefix+key, at); err != nil {
				return fmt.Errorf("record suppressed marker: %w", err)
			}
		}
		logged, err := tx.Exec(ctx, `insert into alert_delivery_log
			(org_id,site_id,alert_id,channel_id,channel_kind,channel_name,status,attempts,error_text,notification,fired_at,updated_at)
			select s.org_id,$1,$2,0,'none','Silenced','suppressed',0,left($3,512),$4,$5,$5 from sites s where s.id=$1
			on conflict (alert_id,channel_id,fired_at) do nothing`, alert.SiteID, alert.ID, text, encoded, at)
		if err != nil {
			return fmt.Errorf("log suppressed notification: %w", err)
		}
		recorded = logged.RowsAffected() > 0
		return nil
	})
	return recorded, err
}

var _ SilenceRepository = (*Service)(nil)

func (s *Service) ListSilences(ctx context.Context, principal authz.Principal, siteID int32) ([]Silence, error) {
	if !authz.Can(principal, authz.ActionAlertsManage, siteID) {
		return nil, ErrForbidden
	}
	rows, err := s.store.Pool.Query(ctx, `select `+silenceColumns+` from alert_silences
		where site_id=$1 and exists (select 1 from sites s where s.id=$1 and s.org_id=$2 and s.deleted_at is null) order by id desc`, siteID, principal.OrgID)
	if err != nil {
		return nil, fmt.Errorf("list silences: %w", err)
	}
	defer rows.Close()
	out := make([]Silence, 0)
	for rows.Next() {
		item, err := scanSilence(rows)
		if err != nil {
			return nil, err
		}
		out = append(out, item)
	}
	return out, rows.Err()
}

// validateSilence checks the input and returns it with the defaults filled.
func validateSilence(in SilenceInput, siteTZ string, now time.Time) (SilenceInput, error) {
	if len(in.Reason) > maxSilenceReason {
		return in, invalidField("reason", "must be at most %d characters", maxSilenceReason)
	}
	start := now
	if in.StartsAt != nil {
		start = *in.StartsAt
	}
	in.StartsAt = &start
	switch in.Kind {
	case SilenceKindSilence:
		if in.EndsAt == nil || !in.EndsAt.After(start) {
			return in, invalidField("ends_at", "must be after starts_at")
		}
		if in.EndsAt.Sub(start) > maxSilenceSpan {
			return in, invalidField("ends_at", "a silence lasts at most a year")
		}
		if len(in.Weekdays) > 0 || in.StartTime != "" || in.DurationMinutes != 0 || in.TZ != "" {
			return in, invalidField("kind", "silence takes no weekdays, start_time, duration_minutes or tz")
		}
	case SilenceKindMaintenance:
		if in.EndsAt != nil && !in.EndsAt.After(start) {
			return in, invalidField("ends_at", "must be after starts_at")
		}
		if len(in.Weekdays) == 0 || len(in.Weekdays) > 7 {
			return in, invalidField("weekdays", "needs 1 to 7 days (0 = Sunday)")
		}
		seen := map[int16]bool{}
		for _, d := range in.Weekdays {
			if d < 0 || d > 6 || seen[d] {
				return in, invalidField("weekdays", "must be distinct values 0 to 6")
			}
			seen[d] = true
		}
		slices.Sort(in.Weekdays)
		if _, _, ok := parseClock(in.StartTime); !ok {
			return in, invalidField("start_time", "must be HH:MM")
		}
		if in.DurationMinutes < 1 || in.DurationMinutes > 1440 {
			return in, invalidField("duration_minutes", "must be 1 to 1440")
		}
		if in.TZ == "" {
			in.TZ = siteTZ
		}
		if _, err := time.LoadLocation(in.TZ); err != nil || in.TZ == "" || in.TZ == "Local" {
			return in, invalidField("tz", "must be an IANA time zone")
		}
	default:
		return in, invalidField("kind", "must be silence or maintenance")
	}
	return in, nil
}

func (s *Service) CreateSilence(ctx context.Context, principal authz.Principal, siteID int32, in SilenceInput) (Silence, error) {
	if !authz.Can(principal, authz.ActionAlertsManage, siteID) {
		return Silence{}, ErrForbidden
	}
	now := s.now()
	var created Silence
	err := s.store.Tx(ctx, func(tx pgx.Tx) error {
		var siteTZ string
		if err := tx.QueryRow(ctx, `select timezone from sites where id=$1 and org_id=$2 and deleted_at is null`, siteID, principal.OrgID).Scan(&siteTZ); err != nil {
			return err
		}
		var err error
		if in, err = validateSilence(in, siteTZ, now); err != nil {
			return err
		}
		if in.AlertID != nil {
			var ok bool
			if err := tx.QueryRow(ctx, `select exists(select 1 from alerts where id=$1 and site_id=$2 and kind='app_rule')`, *in.AlertID, siteID).Scan(&ok); err != nil {
				return err
			}
			if !ok {
				return invalidField("alert_id", "is not an alert rule of this site")
			}
		}
		var active int
		if err := tx.QueryRow(ctx, `select count(*) from alert_silences where site_id=$1 and (ends_at is null or ends_at > $2)`, siteID, now).Scan(&active); err != nil {
			return err
		}
		if active >= MaxSilencesPerSite {
			return invalidField("silences", "a site can have at most %d active or future silences", MaxSilencesPerSite)
		}
		var weekdays, startTime, tz, duration any
		if in.Kind == SilenceKindMaintenance {
			weekdays, startTime, tz, duration = in.Weekdays, in.StartTime, in.TZ, in.DurationMinutes
		}
		var creator *int64
		if principal.UserID != 0 {
			creator = &principal.UserID
		}
		created, err = scanSilence(tx.QueryRow(ctx, `insert into alert_silences(site_id,alert_id,kind,starts_at,ends_at,weekdays,start_time,duration_minutes,tz,reason,created_by)
			values ($1,$2,$3,$4,$5,$6,$7::time,$8,$9,$10,$11) returning `+silenceColumns,
			siteID, in.AlertID, in.Kind, in.StartsAt, in.EndsAt, weekdays, startTime, duration, tz, in.Reason, creator))
		if err != nil {
			return fmt.Errorf("create silence: %w", err)
		}
		return audit.Write(ctx, tx, principal, "alert_silence.created", "alert_silence", created.ID, map[string]any{"site_id": siteID, "kind": created.Kind})
	})
	return created, err
}

func (s *Service) DeleteSilence(ctx context.Context, principal authz.Principal, siteID int32, id int64) error {
	if !authz.Can(principal, authz.ActionAlertsManage, siteID) {
		return ErrForbidden
	}
	return s.store.Tx(ctx, func(tx pgx.Tx) error {
		deleted, err := tx.Exec(ctx, `delete from alert_silences x using sites s where x.id=$1 and x.site_id=$2 and s.id=x.site_id and s.org_id=$3 and s.deleted_at is null`, id, siteID, principal.OrgID)
		if err != nil {
			return fmt.Errorf("delete silence: %w", err)
		}
		if deleted.RowsAffected() == 0 {
			return pgx.ErrNoRows
		}
		return audit.Write(ctx, tx, principal, "alert_silence.deleted", "alert_silence", id, map[string]any{"site_id": siteID})
	})
}
