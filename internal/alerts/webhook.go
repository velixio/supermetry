package alerts

import (
	"bytes"
	"context"
	"crypto/hmac"
	"crypto/sha256"
	"encoding/hex"
	"encoding/json"
	"errors"
	"fmt"
	"io"
	"net"
	"net/http"
	"net/netip"
	"net/url"
	"strconv"
	"time"

	"supermetry/internal/safehttp"
)

const (
	webhookTimeout  = 5 * time.Second
	webhookAttempts = 3

	// WebhookSignatureTolerance is the maximum request age receivers should
	// accept. Parse X-Supermetry-Timestamp as Unix seconds, reject timestamps
	// outside this tolerance, and verify X-Supermetry-Signature as the HMAC-SHA256
	// of "timestamp.body" using the exact request body bytes.
	WebhookSignatureTolerance = 5 * time.Minute
)

type Resolver = safehttp.Resolver

type DialContextFunc = safehttp.DialContextFunc

type WebhookOptions struct {
	AllowPrivate bool
	Resolver     Resolver
	DialContext  DialContextFunc
	Sleep        func(context.Context, time.Duration) error
	OnFailure    func(error)
}

type WebhookClient struct {
	client    *http.Client
	sleep     func(context.Context, time.Duration) error
	onFailure func(error)
}

func NewWebhookClient(options WebhookOptions) *WebhookClient {
	resolver := options.Resolver
	if resolver == nil {
		resolver = net.DefaultResolver
	}
	baseDial := options.DialContext
	if baseDial == nil {
		dialer := &net.Dialer{Timeout: webhookTimeout}
		baseDial = dialer.DialContext
	}
	transport := &http.Transport{
		Proxy:                 nil,
		DialContext:           safehttp.GuardedDial(resolver, baseDial, options.AllowPrivate),
		TLSHandshakeTimeout:   webhookTimeout,
		ResponseHeaderTimeout: webhookTimeout,
	}
	sleep := options.Sleep
	if sleep == nil {
		sleep = sleepContext
	}
	return &WebhookClient{
		client: &http.Client{
			Timeout:       webhookTimeout,
			Transport:     transport,
			CheckRedirect: func(*http.Request, []*http.Request) error { return http.ErrUseLastResponse },
		},
		sleep: sleep, onFailure: options.OnFailure,
	}
}

func (c *WebhookClient) Send(ctx context.Context, kind ChannelKind, rawURL, secret string, notification Notification) error {
	parsed, err := url.Parse(rawURL)
	if err != nil || (parsed.Scheme != "http" && parsed.Scheme != "https") || parsed.Hostname() == "" || parsed.User != nil {
		return errors.New("invalid webhook URL")
	}
	body, err := webhookBody(kind, notification)
	if err != nil {
		return err
	}
	timestamp := strconv.FormatInt(time.Now().Unix(), 10)
	var lastErr error
	for attempt := 0; attempt < webhookAttempts; attempt++ {
		request, requestErr := http.NewRequestWithContext(ctx, http.MethodPost, rawURL, bytes.NewReader(body))
		if requestErr != nil {
			return errors.New("build webhook request")
		}
		request.Header.Set("Content-Type", "application/json")
		if kind == ChannelWebhook && secret != "" {
			request.Header.Set("X-Supermetry-Timestamp", timestamp)
			request.Header.Set("X-Supermetry-Signature", signWebhook(secret, timestamp, body))
		}
		response, requestErr := c.client.Do(request)
		if requestErr == nil {
			_, _ = io.Copy(io.Discard, io.LimitReader(response.Body, 4096))
			_ = response.Body.Close()
			if response.StatusCode >= 200 && response.StatusCode < 300 {
				return nil
			}
			lastErr = fmt.Errorf("webhook delivery returned status %d", response.StatusCode)
		} else if ctx.Err() != nil {
			return ctx.Err()
		} else {
			lastErr = errors.New("webhook request failed")
		}
		if attempt+1 < webhookAttempts {
			if err := c.sleep(ctx, time.Duration(1<<attempt)*100*time.Millisecond); err != nil {
				return err
			}
		}
	}
	if c.onFailure != nil {
		c.onFailure(lastErr)
	}
	return lastErr
}

type webhookPayload struct {
	Alert     AlertKind `json:"alert"`
	Site      int32     `json:"site"`
	Value     float64   `json:"value"`
	Threshold float64   `json:"threshold"`
	FiredAt   time.Time `json:"fired_at"`
}

func webhookBody(kind ChannelKind, notification Notification) ([]byte, error) {
	switch kind {
	case ChannelWebhook:
		return json.Marshal(webhookPayload{
			Alert: notification.Kind, Site: notification.SiteID, Value: notification.Value,
			Threshold: notification.Threshold, FiredAt: notification.FiredAt,
		})
	case ChannelSlack:
		return json.Marshal(struct {
			Text string `json:"text"`
		}{Text: notificationText(notification)})
	case ChannelDiscord:
		return json.Marshal(struct {
			Content string `json:"content"`
		}{Content: notificationText(notification)})
	default:
		return nil, fmt.Errorf("unsupported webhook channel %q", kind)
	}
}

func signWebhook(secret, timestamp string, body []byte) string {
	mac := hmac.New(sha256.New, []byte(secret))
	_, _ = mac.Write([]byte(timestamp))
	_, _ = mac.Write([]byte("."))
	_, _ = mac.Write(body)
	return "sha256=" + hex.EncodeToString(mac.Sum(nil))
}

func guardedDial(resolver Resolver, dial DialContextFunc, allowPrivate bool) DialContextFunc {
	return safehttp.GuardedDial(resolver, dial, allowPrivate)
}

func blockedAddress(address netip.Addr) bool {
	return safehttp.BlockedAddress(address)
}

func sleepContext(ctx context.Context, duration time.Duration) error {
	timer := time.NewTimer(duration)
	defer timer.Stop()
	select {
	case <-timer.C:
		return nil
	case <-ctx.Done():
		return ctx.Err()
	}
}
