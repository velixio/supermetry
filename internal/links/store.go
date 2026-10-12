package links

import (
	"context"
	"errors"
	"time"

	"github.com/jackc/pgx/v5"
	"github.com/jackc/pgx/v5/pgconn"
)

// ErrNotFound is returned when a link does not exist.
var ErrNotFound = errors.New("link not found")

// ErrSlugTaken is returned when a slug is already used by any link.
var ErrSlugTaken = errors.New("slug is already in use")

// ErrLimit is returned when a site already has the maximum number of links.
var ErrLimit = errors.New("link limit reached for this site")

// DBTX is the subset of pgx shared by pools and transactions.
type DBTX interface {
	Exec(context.Context, string, ...any) (pgconn.CommandTag, error)
	Query(context.Context, string, ...any) (pgx.Rows, error)
	QueryRow(context.Context, string, ...any) pgx.Row
}

// Link is a managed short link.
type Link struct {
	ID          int64      `json:"id"`
	SiteID      int32      `json:"site_id"`
	Slug        string     `json:"slug"`
	Name        string     `json:"name"`
	Destination string     `json:"destination"`
	UTM         UTM        `json:"-"`
	Enabled     bool       `json:"enabled"`
	ExpiresAt   *time.Time `json:"expires_at"`
	ClickLimit  *int32     `json:"click_limit"`
	Clicks      int64      `json:"clicks"`
	CreatedBy   *int64     `json:"created_by"`
	CreatedAt   time.Time  `json:"created_at"`
	UpdatedAt   time.Time  `json:"updated_at"`
}

// Status values reported by Link.Status.
const (
	StatusActive       = "active"
	StatusDisabled     = "disabled"
	StatusExpired      = "expired"
	StatusLimitReached = "limit_reached"
)

// Status classifies the link at now, given extra clicks not yet flushed.
func (l Link) Status(now time.Time, pending int64) string {
	switch {
	case !l.Enabled:
		return StatusDisabled
	case l.ExpiresAt != nil && !now.Before(*l.ExpiresAt):
		return StatusExpired
	case l.ClickLimit != nil && l.Clicks+pending >= int64(*l.ClickLimit):
		return StatusLimitReached
	default:
		return StatusActive
	}
}

const linkColumns = `id, site_id, slug, name, destination,
	coalesce(utm_source,''), coalesce(utm_medium,''), coalesce(utm_campaign,''), coalesce(utm_term,''), coalesce(utm_content,''),
	enabled, expires_at, click_limit, clicks, created_by, created_at, updated_at`

func scanLink(row pgx.Row) (Link, error) {
	var l Link
	err := row.Scan(&l.ID, &l.SiteID, &l.Slug, &l.Name, &l.Destination,
		&l.UTM.Source, &l.UTM.Medium, &l.UTM.Campaign, &l.UTM.Term, &l.UTM.Content,
		&l.Enabled, &l.ExpiresAt, &l.ClickLimit, &l.Clicks, &l.CreatedBy, &l.CreatedAt, &l.UpdatedAt)
	if errors.Is(err, pgx.ErrNoRows) {
		return Link{}, ErrNotFound
	}
	return l, err
}

func nullable(value string) any {
	if value == "" {
		return nil
	}
	return value
}

func mapWriteError(err error) error {
	var pgErr *pgconn.PgError
	if errors.As(err, &pgErr) && pgErr.Code == "23505" && pgErr.ConstraintName == "links_slug_key" {
		return ErrSlugTaken
	}
	return err
}

// Create inserts a link. Callers pass validated, normalized values.
func Create(ctx context.Context, db DBTX, l Link) (Link, error) {
	var count int
	if err := db.QueryRow(ctx, `select count(*) from links where site_id=$1`, l.SiteID).Scan(&count); err != nil {
		return Link{}, err
	}
	if count >= MaxLinksPerSite {
		return Link{}, ErrLimit
	}
	created, err := scanLink(db.QueryRow(ctx, `
		insert into links(site_id, slug, name, destination, utm_source, utm_medium, utm_campaign, utm_term, utm_content,
			enabled, expires_at, click_limit, created_by)
		values ($1,$2,$3,$4,$5,$6,$7,$8,$9,$10,$11,$12,$13)
		returning `+linkColumns,
		l.SiteID, l.Slug, l.Name, l.Destination,
		nullable(l.UTM.Source), nullable(l.UTM.Medium), nullable(l.UTM.Campaign), nullable(l.UTM.Term), nullable(l.UTM.Content),
		l.Enabled, l.ExpiresAt, l.ClickLimit, l.CreatedBy))
	return created, mapWriteError(err)
}

// Update replaces every editable column of an existing link.
func Update(ctx context.Context, db DBTX, l Link) (Link, error) {
	updated, err := scanLink(db.QueryRow(ctx, `
		update links set slug=$2, name=$3, destination=$4, utm_source=$5, utm_medium=$6, utm_campaign=$7,
			utm_term=$8, utm_content=$9, enabled=$10, expires_at=$11, click_limit=$12, updated_at=now()
		where id=$1
		returning `+linkColumns,
		l.ID, l.Slug, l.Name, l.Destination,
		nullable(l.UTM.Source), nullable(l.UTM.Medium), nullable(l.UTM.Campaign), nullable(l.UTM.Term), nullable(l.UTM.Content),
		l.Enabled, l.ExpiresAt, l.ClickLimit))
	return updated, mapWriteError(err)
}

// Delete removes a link and, by cascade, its click aggregates.
func Delete(ctx context.Context, db DBTX, id int64) error {
	tag, err := db.Exec(ctx, `delete from links where id=$1`, id)
	if err != nil {
		return err
	}
	if tag.RowsAffected() == 0 {
		return ErrNotFound
	}
	return nil
}

// Get loads one link by ID.
func Get(ctx context.Context, db DBTX, id int64) (Link, error) {
	return scanLink(db.QueryRow(ctx, `select `+linkColumns+` from links where id=$1`, id))
}

// BySlug loads one link by its (normalized) slug.
func BySlug(ctx context.Context, db DBTX, slug string) (Link, error) {
	return scanLink(db.QueryRow(ctx, `select `+linkColumns+` from links where slug=$1`, slug))
}

// List returns a site's links, newest first.
func List(ctx context.Context, db DBTX, siteID int32) ([]Link, error) {
	rows, err := db.Query(ctx, `select `+linkColumns+` from links where site_id=$1 order by id desc limit $2`, siteID, MaxLinksPerSite)
	if err != nil {
		return nil, err
	}
	defer rows.Close()
	result := make([]Link, 0)
	for rows.Next() {
		l, err := scanLink(rows)
		if err != nil {
			return nil, err
		}
		result = append(result, l)
	}
	return result, rows.Err()
}

// Count is one labelled total in a stats breakdown.
type Count struct {
	Value  string `json:"value"`
	Clicks int64  `json:"clicks"`
}

// DayCount is one local calendar day of a time series.
type DayCount struct {
	Day   string `json:"day"`
	Count int64  `json:"count"`
}

// Stats summarizes a link's clicks between two inclusive site-local days.
type Stats struct {
	Total     int64      `json:"total"`
	Daily     []DayCount `json:"daily"`
	Countries []Count    `json:"countries"`
	Referrers []Count    `json:"referrers"`
	Devices   []Count    `json:"devices"`
}

const breakdownLimit = 20

// ClickStats aggregates a link's daily rows for the inclusive day range. Days
// were bucketed in the site's time zone when written, so from and to are
// site-local calendar dates.
func ClickStats(ctx context.Context, db DBTX, linkID int64, from, to string) (Stats, error) {
	stats := Stats{Daily: []DayCount{}, Countries: []Count{}, Referrers: []Count{}, Devices: []Count{}}
	rows, err := db.Query(ctx, `
		select day::text, sum(clicks)::bigint from link_clicks_daily
		where link_id=$1 and day between $2::date and $3::date group by day order by day`, linkID, from, to)
	if err != nil {
		return Stats{}, err
	}
	for rows.Next() {
		var d DayCount
		if err := rows.Scan(&d.Day, &d.Count); err != nil {
			rows.Close()
			return Stats{}, err
		}
		stats.Daily = append(stats.Daily, d)
		stats.Total += d.Count
	}
	rows.Close()
	if err := rows.Err(); err != nil {
		return Stats{}, err
	}
	for _, spec := range []struct {
		column string
		target *[]Count
		limit  int
	}{{"country", &stats.Countries, breakdownLimit}, {"referrer", &stats.Referrers, breakdownLimit}, {"device", &stats.Devices, 3}} {
		// column comes from the fixed list above, never from input.
		breakdown, err := db.Query(ctx, `
			select `+spec.column+`, sum(clicks)::bigint as total from link_clicks_daily
			where link_id=$1 and day between $2::date and $3::date
			group by 1 order by total desc, 1 limit $4`, linkID, from, to, spec.limit)
		if err != nil {
			return Stats{}, err
		}
		for breakdown.Next() {
			var c Count
			if err := breakdown.Scan(&c.Value, &c.Clicks); err != nil {
				breakdown.Close()
				return Stats{}, err
			}
			*spec.target = append(*spec.target, c)
		}
		breakdown.Close()
		if err := breakdown.Err(); err != nil {
			return Stats{}, err
		}
	}
	return stats, nil
}

// CampaignCount is one pixel campaign's impressions.
type CampaignCount struct {
	Campaign    string `json:"campaign"`
	Impressions int64  `json:"impressions"`
}

// PixelStats summarizes pixel impressions for a site.
type PixelStats struct {
	Total     int64           `json:"total"`
	Daily     []DayCount      `json:"daily"`
	Campaigns []CampaignCount `json:"campaigns"`
}

// ImpressionStats aggregates a site's pixel rows for the inclusive day range.
func ImpressionStats(ctx context.Context, db DBTX, siteID int32, from, to string) (PixelStats, error) {
	stats := PixelStats{Daily: []DayCount{}, Campaigns: []CampaignCount{}}
	rows, err := db.Query(ctx, `
		select day::text, sum(impressions)::bigint from pixel_impressions_daily
		where site_id=$1 and day between $2::date and $3::date group by day order by day`, siteID, from, to)
	if err != nil {
		return PixelStats{}, err
	}
	for rows.Next() {
		var d DayCount
		if err := rows.Scan(&d.Day, &d.Count); err != nil {
			rows.Close()
			return PixelStats{}, err
		}
		stats.Daily = append(stats.Daily, d)
		stats.Total += d.Count
	}
	rows.Close()
	if err := rows.Err(); err != nil {
		return PixelStats{}, err
	}
	campaigns, err := db.Query(ctx, `
		select campaign, sum(impressions)::bigint as total from pixel_impressions_daily
		where site_id=$1 and day between $2::date and $3::date
		group by campaign order by total desc, campaign limit 50`, siteID, from, to)
	if err != nil {
		return PixelStats{}, err
	}
	defer campaigns.Close()
	for campaigns.Next() {
		var c CampaignCount
		if err := campaigns.Scan(&c.Campaign, &c.Impressions); err != nil {
			return PixelStats{}, err
		}
		stats.Campaigns = append(stats.Campaigns, c)
	}
	return stats, campaigns.Err()
}
