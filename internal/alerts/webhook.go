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
	"time"
)

const (
	webhookTimeout  = 5 * time.Second
	webhookAttempts = 3
)

type Resolver interface {
	LookupNetIP(context.Context, string, string) ([]netip.Addr, error)
}

type DialContextFunc func(context.Context, string, string) (net.Conn, error)

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
		DialContext:           guardedDial(resolver, baseDial, options.AllowPrivate),
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
	var lastErr error
	for attempt := 0; attempt < webhookAttempts; attempt++ {
		request, requestErr := http.NewRequestWithContext(ctx, http.MethodPost, rawURL, bytes.NewReader(body))
		if requestErr != nil {
			return errors.New("build webhook request")
		}
		request.Header.Set("Content-Type", "application/json")
		if kind == ChannelWebhook && secret != "" {
			request.Header.Set("X-Panel-Signature", signWebhook(secret, body))
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

func signWebhook(secret string, body []byte) string {
	mac := hmac.New(sha256.New, []byte(secret))
	_, _ = mac.Write(body)
	return "sha256=" + hex.EncodeToString(mac.Sum(nil))
}

func guardedDial(resolver Resolver, dial DialContextFunc, allowPrivate bool) DialContextFunc {
	return func(ctx context.Context, network, address string) (net.Conn, error) {
		host, port, err := net.SplitHostPort(address)
		if err != nil {
			return nil, errors.New("invalid webhook address")
		}
		var addresses []netip.Addr
		if literal, parseErr := netip.ParseAddr(host); parseErr == nil {
			addresses = []netip.Addr{literal}
		} else {
			addresses, err = resolver.LookupNetIP(ctx, "ip", host)
			if err != nil || len(addresses) == 0 {
				return nil, errors.New("resolve webhook host")
			}
		}
		for _, address := range addresses {
			if !allowPrivate && blockedAddress(address) {
				return nil, errors.New("webhook host resolves to a blocked address")
			}
		}
		var dialErr error
		for _, address := range addresses {
			conn, err := dial(ctx, network, net.JoinHostPort(address.String(), port))
			if err == nil {
				return conn, nil
			}
			dialErr = err
		}
		_ = dialErr
		return nil, errors.New("connect to webhook host")
	}
}

// blockedPrefixes are special-purpose ranges not covered by the netip
// predicates: "this network", CGNAT, IETF protocol assignments, benchmarking,
// reserved/broadcast and deprecated IPv6 site-local.
var blockedPrefixes = []netip.Prefix{
	netip.MustParsePrefix("0.0.0.0/8"),
	netip.MustParsePrefix("100.64.0.0/10"),
	netip.MustParsePrefix("192.0.0.0/24"),
	netip.MustParsePrefix("198.18.0.0/15"),
	netip.MustParsePrefix("240.0.0.0/4"),
	netip.MustParsePrefix("fec0::/10"),
}

var nat64Prefix = netip.MustParsePrefix("64:ff9b::/96")

func blockedAddress(address netip.Addr) bool {
	address = address.Unmap()
	if address.Is6() && nat64Prefix.Contains(address) {
		bytes := address.As16()
		return blockedAddress(netip.AddrFrom4([4]byte{bytes[12], bytes[13], bytes[14], bytes[15]}))
	}
	if !address.IsValid() || address.IsPrivate() || address.IsLoopback() || address.IsLinkLocalUnicast() ||
		address.IsLinkLocalMulticast() || address.IsInterfaceLocalMulticast() || address.IsMulticast() || address.IsUnspecified() {
		return true
	}
	for _, prefix := range blockedPrefixes {
		if prefix.Contains(address) {
			return true
		}
	}
	return false
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
