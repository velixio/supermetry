package alerts

import (
	"context"
	"errors"
	"fmt"
	"time"

	"supermetry/internal/platform/mail"
)

// DeliverMessage sends an already written message through a channel without
// the alert template, so other features can reuse the same channel config and
// senders. subject is used by email only. kind names the source in webhook
// payloads. Failures are generic strings that never contain secrets.
func (d *Dispatcher) DeliverMessage(ctx context.Context, channel DeliveryChannel, kind, subject, text string) error {
	switch channel.Kind {
	case ChannelEmail:
		if d.mailer == nil || !d.mailer.Enabled() {
			return errors.New("email delivery is disabled")
		}
		for _, recipient := range channel.Recipients {
			if err := d.mailer.Send(ctx, mail.Message{To: recipient, Subject: subject, Text: text}); err != nil {
				return fmt.Errorf("send email: %w", err)
			}
		}
		return nil
	case ChannelWebhook, ChannelSlack, ChannelDiscord:
		if d.webhooks == nil {
			return errors.New("webhook delivery is unavailable")
		}
		return d.webhooks.SendRendered(ctx, channel.Kind, channel.URL, channel.Secret, Notification{Kind: AlertKind(kind), FiredAt: time.Now()}, text)
	case ChannelTelegram:
		if d.telegram == nil {
			return errors.New("Telegram delivery is unavailable")
		}
		return d.telegram.SendMessage(ctx, channel.BotToken, channel.ChatID, text)
	default:
		return fmt.Errorf("unknown channel kind %q", channel.Kind)
	}
}

type Dispatcher struct {
	mailer   mail.Sender
	webhooks *WebhookClient
	telegram TelegramSender
}

func NewDispatcher(mailer mail.Sender, webhooks *WebhookClient, telegram TelegramSender) *Dispatcher {
	return &Dispatcher{mailer: mailer, webhooks: webhooks, telegram: telegram}
}

func (d *Dispatcher) Deliver(ctx context.Context, channel DeliveryChannel, notification Notification) error {
	templateInput := channel.Template
	if templateInput.Body == "" {
		templateInput = DefaultTemplate(channel.Kind)
	}
	message, err := RenderTemplate(channel.Kind, templateInput, notification)
	if err != nil {
		return err
	}
	switch channel.Kind {
	case ChannelEmail:
		if d.mailer == nil || !d.mailer.Enabled() {
			return errors.New("email delivery is disabled")
		}
		for _, recipient := range channel.Recipients {
			if err := d.mailer.Send(ctx, mail.Message{
				To: recipient, Subject: message.Subject, Text: message.Body,
			}); err != nil {
				return fmt.Errorf("send alert email: %w", err)
			}
		}
		return nil
	case ChannelWebhook, ChannelSlack, ChannelDiscord:
		if d.webhooks == nil {
			return errors.New("webhook delivery is unavailable")
		}
		return d.webhooks.SendRendered(ctx, channel.Kind, channel.URL, channel.Secret, notification, message.Body)
	case ChannelTelegram:
		if d.telegram == nil {
			return errors.New("Telegram delivery is unavailable")
		}
		return d.telegram.SendMessage(ctx, channel.BotToken, channel.ChatID, message.Body)
	default:
		return fmt.Errorf("unknown channel kind %q", channel.Kind)
	}
}

func notificationText(notification Notification) string {
	rendered, err := RenderTemplate(ChannelWebhook, DefaultTemplate(ChannelWebhook), notification)
	if err != nil {
		return fmt.Sprintf("%s alert", notification.Kind)
	}
	return rendered.Body
}
