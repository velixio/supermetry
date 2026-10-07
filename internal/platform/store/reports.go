package store

import (
	"context"
	"errors"
	"fmt"
	"time"

	"github.com/jackc/pgx/v5"
)

// ReportSubscription is the persisted delivery information for one report.
type ReportSubscription struct {
	ID         int64
	SiteID     int32
	UserID     int64
	Email      string
	Cadence    string
	LastSentAt *time.Time
}

// SubscribeReport creates a subscription for a user and site in the same
// organization. It returns false when the subscription already exists.
func SubscribeReport(ctx context.Context, tx pgx.Tx, orgID, userID int64, siteID int32, cadence string) (int64, bool, error) {
	var id int64
	err := tx.QueryRow(ctx, `
		insert into report_subscriptions (site_id, user_id, cadence)
		select id, $1, $2
		from sites
		where id = $3 and org_id = $4 and deleted_at is null
		on conflict (site_id, user_id, cadence) do nothing
		returning report_subscriptions.id`, userID, cadence, siteID, orgID).Scan(&id)
	if err == nil {
		return id, true, nil
	}
	if !errors.Is(err, pgx.ErrNoRows) {
		return 0, false, fmt.Errorf("subscribe report: %w", err)
	}

	err = tx.QueryRow(ctx, `
		select rs.id
		from report_subscriptions rs
		join sites s on s.id = rs.site_id
		where rs.site_id = $1 and rs.user_id = $2 and rs.cadence = $3
		  and s.org_id = $4 and s.deleted_at is null`, siteID, userID, cadence, orgID).Scan(&id)
	if err != nil {
		return 0, false, err
	}
	return id, false, nil
}

// ReportSubscriptions returns active subscriptions whose recipient still has
// access to the site, with the recipient address.
func ReportSubscriptions(ctx context.Context, data *Store) ([]ReportSubscription, error) {
	rows, err := data.Pool.Query(ctx, `
		select rs.id, rs.site_id, rs.user_id, u.email, rs.cadence, rs.last_sent_at
		from report_subscriptions rs
		join users u on u.id = rs.user_id
		join sites s on s.id = rs.site_id
		-- Re-check access at delivery time: the recipient must still be a member of
		-- the site's org, and a site restriction must still include this site.
		join memberships m on m.user_id = rs.user_id and m.org_id = s.org_id
		where s.deleted_at is null
		  and (m.site_ids is null or rs.site_id = any(m.site_ids))
		order by rs.id`)
	if err != nil {
		return nil, fmt.Errorf("list report subscriptions: %w", err)
	}
	defer rows.Close()

	subscriptions := make([]ReportSubscription, 0)
	for rows.Next() {
		var subscription ReportSubscription
		if err := rows.Scan(
			&subscription.ID, &subscription.SiteID, &subscription.UserID,
			&subscription.Email, &subscription.Cadence, &subscription.LastSentAt,
		); err != nil {
			return nil, fmt.Errorf("scan report subscription: %w", err)
		}
		subscriptions = append(subscriptions, subscription)
	}
	if err := rows.Err(); err != nil {
		return nil, fmt.Errorf("iterate report subscriptions: %w", err)
	}
	return subscriptions, nil
}

// MarkReportSent records a successful delivery.
func MarkReportSent(ctx context.Context, data *Store, id int64, sentAt time.Time) error {
	command, err := data.Pool.Exec(ctx, `
		update report_subscriptions
		set last_sent_at = $2
		where id = $1`, id, sentAt)
	if err != nil {
		return fmt.Errorf("mark report sent: %w", err)
	}
	if command.RowsAffected() == 0 {
		return pgx.ErrNoRows
	}
	return nil
}
