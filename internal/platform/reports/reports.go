// Package reports schedules, renders, and sends analytics email reports.
package reports

import (
	"context"
	"errors"
	"fmt"
	"time"

	"github.com/jackc/pgx/v5"
	openapi_types "github.com/oapi-codegen/runtime/types"

	"panel/internal/api"
	"panel/internal/platform/audit"
	"panel/internal/platform/authz"
	"panel/internal/platform/mail"
	"panel/internal/platform/store"
	"panel/internal/scheduler"
	"panel/internal/sites"
)

const (
	Weekly  = "weekly"
	Monthly = "monthly"
)

var (
	ErrForbidden      = errors.New("forbidden")
	ErrInvalidCadence = errors.New("cadence must be weekly or monthly")
	ErrSiteNotFound   = errors.New("site not found")
)

// Subscription is a report that is ready for delivery.
type Subscription struct {
	ID         int64
	SiteID     int32
	UserID     int64
	Email      string
	Cadence    string
	LastSentAt *time.Time
	Site       sites.Site
	Boundary   time.Time
}

// Queryer is implemented by query.Engine.
type Queryer interface {
	Query(context.Context, api.QueryRequest) (api.QueryResponse, error)
}

// Reports manages subscriptions and the scheduled delivery job.
type Reports struct {
	store    *store.Store
	engine   Queryer
	registry sites.Registry
	mailer   mail.Sender
	now      func() time.Time
}

// New constructs the email report service.
func New(data *store.Store, engine Queryer, registry sites.Registry, sender mail.Sender) *Reports {
	return &Reports{store: data, engine: engine, registry: registry, mailer: sender, now: time.Now}
}

// Subscribe adds the current user to a site's report cadence.
func (r *Reports) Subscribe(ctx context.Context, principal authz.Principal, siteID int32, cadence string) error {
	if cadence != Weekly && cadence != Monthly {
		return ErrInvalidCadence
	}
	if principal.UserID == 0 || !authz.Can(principal, authz.ActionReportsManage, siteID) {
		return ErrForbidden
	}
	return r.store.Tx(ctx, func(tx pgx.Tx) error {
		current, err := store.CurrentMemberForUpdate(ctx, tx, principal.OrgID, principal.UserID)
		if errors.Is(err, pgx.ErrNoRows) {
			return ErrForbidden
		}
		if err != nil {
			return err
		}
		if !authz.Can(current, authz.ActionReportsManage, siteID) {
			return ErrForbidden
		}
		id, created, err := store.SubscribeReport(ctx, tx, principal.OrgID, principal.UserID, siteID, cadence)
		if err != nil {
			if errors.Is(err, pgx.ErrNoRows) {
				return ErrSiteNotFound
			}
			return err
		}
		if !created {
			return nil
		}
		return audit.Write(ctx, tx, principal, "report.subscribed", "report_subscription", id, map[string]any{
			"site_id": siteID, "cadence": cadence,
		})
	})
}

// Due returns reports whose site-local delivery boundary has passed and which
// have not yet been sent in that period.
func (r *Reports) Due(now time.Time) ([]Subscription, error) {
	stored, err := store.ReportSubscriptions(context.Background(), r.store)
	if err != nil {
		return nil, err
	}
	due := make([]Subscription, 0, len(stored))
	for _, candidate := range stored {
		site, ok := r.registry.Site(candidate.SiteID)
		if !ok || site.TZ == nil {
			continue
		}
		boundary := deliveryBoundary(now, site.TZ, candidate.Cadence)
		if now.Before(boundary) || candidate.LastSentAt != nil && !candidate.LastSentAt.Before(boundary) {
			continue
		}
		due = append(due, Subscription{
			ID: candidate.ID, SiteID: candidate.SiteID, UserID: candidate.UserID,
			Email: candidate.Email, Cadence: candidate.Cadence,
			LastSentAt: candidate.LastSentAt, Site: site, Boundary: boundary,
		})
	}
	return due, nil
}

// Job returns the scheduler job that delivers due reports.
func (r *Reports) Job() scheduler.Job {
	return scheduler.Job{Name: "email-reports", Every: 15 * time.Minute, Run: func(ctx context.Context) error {
		if r.mailer == nil || !r.mailer.Enabled() {
			return nil
		}
		now := r.now()
		due, err := r.Due(now)
		if err != nil {
			return err
		}
		for _, subscription := range due {
			currentRange, previousRange := reportRanges(subscription.Boundary, subscription.Cadence, subscription.Site.TZ)
			current, err := r.summary(ctx, subscription.Site, subscription.Cadence, currentRange)
			if err != nil {
				return fmt.Errorf("build current report for subscription %d: %w", subscription.ID, err)
			}
			previous, err := r.summary(ctx, subscription.Site, subscription.Cadence, previousRange)
			if err != nil {
				return fmt.Errorf("build previous report for subscription %d: %w", subscription.ID, err)
			}
			// Rendering/querying may take time. Recheck after that work so a
			// queued report cannot deliver data after access has been removed.
			recipient, allowed, err := store.ReportRecipient(ctx, r.store, subscription.ID)
			if err != nil {
				return err
			}
			if !allowed {
				continue
			}
			message := Render(subscription.Site, current, previous)
			message.To = recipient
			if err := r.mailer.Send(ctx, message); err != nil {
				return fmt.Errorf("send report subscription %d: %w", subscription.ID, err)
			}
			if err := store.MarkReportSent(ctx, r.store, subscription.ID, now); err != nil {
				return err
			}
		}
		return nil
	}}
}

type reportRange struct {
	from time.Time
	to   time.Time
}

func (r *Reports) summary(ctx context.Context, site sites.Site, cadence string, dateRange reportRange) (Summary, error) {
	summary := Summary{Cadence: cadence}
	response, err := r.engine.Query(ctx, queryRequest(site, dateRange, ""))
	if err != nil {
		return Summary{}, err
	}
	summary.Visitors = int64(response.Totals[string(api.Visitors)])
	summary.Pageviews = int64(response.Totals[string(api.Pageviews)])

	response, err = r.engine.Query(ctx, queryRequest(site, dateRange, "page"))
	if err != nil {
		return Summary{}, err
	}
	summary.Pages = breakdowns(response.Rows, "page")
	response, err = r.engine.Query(ctx, queryRequest(site, dateRange, "source"))
	if err != nil {
		return Summary{}, err
	}
	summary.Sources = breakdowns(response.Rows, "source")
	return summary, nil
}

func queryRequest(site sites.Site, dateRange reportRange, dimension string) api.QueryRequest {
	request := api.QueryRequest{
		SiteId: site.ID,
		DateRange: &api.DateRange{
			From: openapi_types.Date{Time: dateRange.from},
			To:   openapi_types.Date{Time: dateRange.to},
		},
		Metrics: []api.Metric{api.Visitors, api.Pageviews},
	}
	if site.TZ != nil {
		name := site.TZ.String()
		request.Timezone = &name
	}
	if dimension != "" {
		request.Dimensions = &[]string{dimension}
		limit := 5
		request.Limit = &limit
		desc := api.Desc
		request.OrderBy = &[]api.OrderBy{{Column: string(api.Visitors), Direction: &desc}}
	}
	return request
}

func breakdowns(rows []map[string]any, dimension string) []Breakdown {
	items := make([]Breakdown, 0, min(5, len(rows)))
	for _, row := range rows {
		value, ok := row[dimension].(string)
		if !ok {
			continue
		}
		items = append(items, Breakdown{
			Value: value, Visitors: numeric(row[string(api.Visitors)]), Pageviews: numeric(row[string(api.Pageviews)]),
		})
		if len(items) == 5 {
			break
		}
	}
	return items
}

func numeric(value any) int64 {
	switch value := value.(type) {
	case float64:
		return int64(value)
	case int64:
		return value
	case int:
		return int64(value)
	default:
		return 0
	}
}

func deliveryBoundary(now time.Time, location *time.Location, cadence string) time.Time {
	local := now.In(location)
	if cadence == Monthly {
		return time.Date(local.Year(), local.Month(), 1, 8, 0, 0, 0, location)
	}
	daysSinceMonday := (int(local.Weekday()) + 6) % 7
	monday := local.AddDate(0, 0, -daysSinceMonday)
	return time.Date(monday.Year(), monday.Month(), monday.Day(), 8, 0, 0, 0, location)
}

func reportRanges(boundary time.Time, cadence string, location *time.Location) (reportRange, reportRange) {
	localBoundary := boundary.In(location)
	if cadence == Monthly {
		currentStart := time.Date(localBoundary.Year(), localBoundary.Month()-1, 1, 0, 0, 0, 0, location)
		currentEnd := time.Date(localBoundary.Year(), localBoundary.Month(), 0, 0, 0, 0, 0, location)
		previousStart := time.Date(localBoundary.Year(), localBoundary.Month()-2, 1, 0, 0, 0, 0, location)
		previousEnd := currentStart.AddDate(0, 0, -1)
		return reportRange{from: currentStart, to: currentEnd}, reportRange{from: previousStart, to: previousEnd}
	}
	currentStart := localBoundary.AddDate(0, 0, -7)
	currentEnd := localBoundary.AddDate(0, 0, -1)
	previousStart := currentStart.AddDate(0, 0, -7)
	previousEnd := currentStart.AddDate(0, 0, -1)
	return reportRange{from: currentStart, to: currentEnd}, reportRange{from: previousStart, to: previousEnd}
}
