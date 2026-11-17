package main

import (
	"context"
	"errors"
	"time"

	"supermetry/internal/alerts"
	"supermetry/internal/appissues"
	"supermetry/internal/scheduler"
)

// issueWebhookSender sends issue webhook messages through the organisation's
// notification channels, with the same signing and delivery as alerts.
type issueWebhookSender struct {
	channels interface {
		DeliveryChannels(context.Context, []int64) ([]alerts.DeliveryChannel, error)
	}
	dispatcher *alerts.Dispatcher
}

func (s issueWebhookSender) Send(ctx context.Context, channelID int64, m appissues.WebhookMessage) error {
	list, err := s.channels.DeliveryChannels(ctx, []int64{channelID})
	if err != nil {
		return err
	}
	if len(list) == 0 {
		return nil // the channel was deleted: nothing left to send to
	}
	if !list[0].Enabled {
		return nil
	}
	return s.dispatcher.DeliverEvent(ctx, list[0], "issue_"+m.Type, m.Subject, m.Text, m.Payload)
}

// issueWebhookJob drains the issue webhook outbox every 15 seconds.
func issueWebhookJob(svc *appissues.Service, sender appissues.WebhookSender, baseURL string) scheduler.Job {
	return scheduler.Job{Name: "app-issue-webhooks", Every: 15 * time.Second, Run: func(ctx context.Context) error {
		err := svc.DeliverWebhooks(ctx, sender, baseURL)
		if errors.Is(err, context.Canceled) {
			return nil
		}
		return err
	}}
}
