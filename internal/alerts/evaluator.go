package alerts

import (
	"context"
	"errors"
	"fmt"
	"time"

	"panel/internal/scheduler"
	"panel/internal/sites"
)

const (
	jobInterval     = 5 * time.Minute
	trafficCooldown = 6 * time.Hour
)

type MetricSource interface {
	Visitors(context.Context, sites.Site, time.Time, time.Time) (float64, error)
	Conversions(context.Context, sites.Site, int64, time.Time, time.Time) (float64, error)
}

type Repository interface {
	EnabledAlerts(context.Context) ([]Alert, error)
	DeliveryChannels(context.Context, []int64) ([]DeliveryChannel, error)
	MarkFired(context.Context, int64, time.Time) error
}

type Deliverer interface {
	Deliver(context.Context, DeliveryChannel, Notification) error
}

type DeliveryFailureFunc func(alertID, channelID int64, kind ChannelKind, err error)

type Evaluator struct {
	repository Repository
	metrics    MetricSource
	sites      sites.Registry
	deliverer  Deliverer
	now        func() time.Time
	onFailure  DeliveryFailureFunc
}

func NewEvaluator(repository Repository, metrics MetricSource, registry sites.Registry, deliverer Deliverer, now func() time.Time, failures ...DeliveryFailureFunc) *Evaluator {
	if now == nil {
		now = time.Now
	}
	var onFailure DeliveryFailureFunc
	if len(failures) > 0 {
		onFailure = failures[0]
	}
	return &Evaluator{repository: repository, metrics: metrics, sites: registry, deliverer: deliverer, now: now, onFailure: onFailure}
}

func (e *Evaluator) Job() scheduler.Job {
	return scheduler.Job{Name: "alerts", Every: jobInterval, Run: e.Run}
}

func (e *Evaluator) Run(ctx context.Context) error {
	alertRules, err := e.repository.EnabledAlerts(ctx)
	if err != nil {
		return fmt.Errorf("load enabled alerts: %w", err)
	}
	now := e.now()
	var runErr error
	for _, alert := range alertRules {
		if err := e.evaluate(ctx, alert, now); err != nil {
			runErr = errors.Join(runErr, fmt.Errorf("evaluate alert %d: %w", alert.ID, err))
		}
	}
	return runErr
}

func (e *Evaluator) evaluate(ctx context.Context, alert Alert, now time.Time) error {
	site, ok := e.sites.Site(alert.SiteID)
	if !ok || site.TZ == nil {
		return fmt.Errorf("site %d is unavailable", alert.SiteID)
	}
	var from, to time.Time
	var value float64
	var fire bool
	var err error
	switch alert.Kind {
	case AlertTrafficSpike:
		if withinCooldown(alert.LastFiredAt, now, trafficCooldown) {
			return nil
		}
		from, to = now.Add(-time.Hour), now
		value, err = e.metrics.Visitors(ctx, site, from, to)
		fire = value >= alert.Threshold
	case AlertTrafficDrop:
		if withinCooldown(alert.LastFiredAt, now, trafficCooldown) {
			return nil
		}
		from, to = now.Add(-24*time.Hour), now
		value, err = e.metrics.Visitors(ctx, site, from, to)
		if err == nil {
			var baseline float64
			baseline, err = e.metrics.Visitors(ctx, site, now.Add(-8*24*time.Hour), now.Add(-7*24*time.Hour))
			fire = baseline > 0 && value < baseline*alert.Threshold/100
		}
	case AlertGoalReached:
		if alert.GoalID == nil {
			return errors.New("goal alert has no goal")
		}
		localNow := now.In(site.TZ)
		from = time.Date(localNow.Year(), localNow.Month(), localNow.Day(), 0, 0, 0, 0, site.TZ)
		to = now
		if firedOnLocalDay(alert.LastFiredAt, localNow, site.TZ) {
			return nil
		}
		value, err = e.metrics.Conversions(ctx, site, *alert.GoalID, from, to)
		fire = value >= alert.Threshold
	default:
		return fmt.Errorf("unknown kind %q", alert.Kind)
	}
	if err != nil {
		return err
	}
	if !fire {
		return nil
	}
	channels, err := e.repository.DeliveryChannels(ctx, alert.ChannelIDs)
	if err != nil {
		return err
	}
	if len(channels) != len(alert.ChannelIDs) {
		return errors.New("one or more notification channels are unavailable")
	}
	notification := Notification{AlertID: alert.ID, SiteID: alert.SiteID, Kind: alert.Kind, Value: value, Threshold: alert.Threshold, From: from, To: to, FiredAt: now}
	var deliveryErr error
	for _, channel := range channels {
		if err := e.deliverer.Deliver(ctx, channel, notification); err != nil {
			if e.onFailure != nil {
				e.onFailure(alert.ID, channel.ID, channel.Kind, err)
			}
			deliveryErr = errors.Join(deliveryErr, fmt.Errorf("deliver channel %d: %w", channel.ID, err))
		}
	}
	markErr := e.repository.MarkFired(ctx, alert.ID, now)
	return errors.Join(deliveryErr, markErr)
}

func withinCooldown(last *time.Time, now time.Time, cooldown time.Duration) bool {
	return last != nil && now.Sub(*last) < cooldown
}

func firedOnLocalDay(last *time.Time, now time.Time, location *time.Location) bool {
	if last == nil {
		return false
	}
	local := last.In(location)
	return local.Year() == now.Year() && local.YearDay() == now.YearDay()
}
