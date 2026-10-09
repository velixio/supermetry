package alerts

import (
	"context"
	"errors"
	"fmt"

	"panel/internal/platform/mail"
)

type Dispatcher struct {
	mailer   mail.Sender
	webhooks *WebhookClient
	telegram TelegramSender
}

func NewDispatcher(mailer mail.Sender, webhooks *WebhookClient, telegram TelegramSender) *Dispatcher {
	return &Dispatcher{mailer: mailer, webhooks: webhooks, telegram: telegram}
}

func (d *Dispatcher) Deliver(ctx context.Context, channel DeliveryChannel, notification Notification) error {
	switch channel.Kind {
	case ChannelEmail:
		if d.mailer == nil || !d.mailer.Enabled() {
			return errors.New("email delivery is disabled")
		}
		message := notificationText(notification)
		for _, recipient := range channel.Recipients {
			if err := d.mailer.Send(ctx, mail.Message{
				To: recipient, Subject: "Panel analytics alert", Text: message,
			}); err != nil {
				return fmt.Errorf("send alert email: %w", err)
			}
		}
		return nil
	case ChannelWebhook, ChannelSlack, ChannelDiscord:
		if d.webhooks == nil {
			return errors.New("webhook delivery is unavailable")
		}
		return d.webhooks.Send(ctx, channel.Kind, channel.URL, channel.Secret, notification)
	case ChannelTelegram:
		if d.telegram == nil {
			return errors.New("Telegram delivery is unavailable")
		}
		return d.telegram.SendMessage(ctx, channel.BotToken, channel.ChatID, notificationText(notification))
	default:
		return fmt.Errorf("unknown channel kind %q", channel.Kind)
	}
}

func notificationText(notification Notification) string {
	return fmt.Sprintf("%s alert for site %d: value %.2f, threshold %.2f", notification.Kind, notification.SiteID, notification.Value, notification.Threshold)
}
