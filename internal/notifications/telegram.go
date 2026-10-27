package notifications

import (
	"bytes"
	"context"
	"encoding/json"
	"errors"
	"fmt"
	"io"
	"net"
	"net/http"
	"net/url"
	"regexp"
	"strings"
	"time"
)

const (
	telegramBaseURL       = "https://api.telegram.org"
	telegramTimeout       = 10 * time.Second
	telegramMessageLimit  = 4000
	telegramResponseLimit = 16 << 10
)

var (
	tokenPattern  = regexp.MustCompile(`^[0-9]{5,20}:[A-Za-z0-9_-]{20,200}$`)
	chatIDPattern = regexp.MustCompile(`^(?:-?[0-9]{1,20}|@[A-Za-z0-9_]{5,32})$`)
	// tokenInText finds a bot token or a bot<token> URL segment in any text.
	tokenInText = regexp.MustCompile(`(?:bot)?[0-9]{5,20}:[A-Za-z0-9_-]{20,200}`)
)

// ValidToken reports whether s looks like a Telegram bot token.
func ValidToken(s string) bool { return tokenPattern.MatchString(s) }

// ValidChatID reports whether s is a numeric chat id or an @channelname.
func ValidChatID(s string) bool { return chatIDPattern.MatchString(s) }

// Sender delivers one plain-text message.
type Sender interface {
	SendMessage(ctx context.Context, token, chatID, text string) error
}

// TelegramOptions configures a TelegramClient. The zero value is the
// production configuration; the fields exist for tests.
type TelegramOptions struct {
	// BaseURL must be https. Default https://api.telegram.org.
	BaseURL string
	// Transport replaces the default transport (tests pass a TLS test client).
	Transport http.RoundTripper
	// Timeout bounds one request. Default 10 s.
	Timeout time.Duration
}

// TelegramClient calls the Telegram Bot API sendMessage method only.
type TelegramClient struct {
	base   string
	client *http.Client
}

// NewTelegramClient builds a client with redirects refused and no proxy.
func NewTelegramClient(options TelegramOptions) (*TelegramClient, error) {
	base := strings.TrimRight(options.BaseURL, "/")
	if base == "" {
		base = telegramBaseURL
	}
	parsed, err := url.Parse(base)
	if err != nil || parsed.Scheme != "https" || parsed.Host == "" {
		return nil, errors.New("telegram base URL must be https")
	}
	timeout := options.Timeout
	if timeout <= 0 {
		timeout = telegramTimeout
	}
	transport := options.Transport
	if transport == nil {
		transport = &http.Transport{
			Proxy:                 nil,
			DialContext:           (&net.Dialer{Timeout: timeout}).DialContext,
			TLSHandshakeTimeout:   timeout,
			ResponseHeaderTimeout: timeout,
		}
	}
	return &TelegramClient{
		base: base,
		client: &http.Client{
			Timeout:       timeout,
			Transport:     transport,
			CheckRedirect: func(*http.Request, []*http.Request) error { return http.ErrUseLastResponse },
		},
	}, nil
}

// SendMessage posts text (plain, no parse mode, capped at 4000 characters) to
// chatID. Returned errors never contain the token.
func (c *TelegramClient) SendMessage(ctx context.Context, token, chatID, text string) error {
	if !ValidToken(token) || !ValidChatID(chatID) {
		return errors.New("invalid Telegram configuration")
	}
	body, err := json.Marshal(struct {
		ChatID string `json:"chat_id"`
		Text   string `json:"text"`
	}{ChatID: chatID, Text: capText(text)})
	if err != nil {
		return errors.New("encode Telegram request")
	}
	request, err := http.NewRequestWithContext(ctx, http.MethodPost, c.base+"/bot"+token+"/sendMessage", bytes.NewReader(body))
	if err != nil {
		return errors.New("build Telegram request")
	}
	request.Header.Set("Content-Type", "application/json")
	response, err := c.client.Do(request)
	if err != nil {
		if ctx.Err() != nil {
			return ctx.Err()
		}
		// net/http errors embed the request URL, whose path holds the token.
		return errors.New("Telegram request failed")
	}
	defer response.Body.Close()
	if response.StatusCode >= 300 && response.StatusCode < 400 {
		return fmt.Errorf("Telegram answered with a redirect (status %d), refused", response.StatusCode)
	}
	raw, err := io.ReadAll(io.LimitReader(response.Body, telegramResponseLimit+1))
	if err != nil {
		return errors.New("read Telegram response")
	}
	if len(raw) > telegramResponseLimit {
		return errors.New("Telegram response too large")
	}
	var parsed struct {
		OK          bool   `json:"ok"`
		Description string `json:"description"`
	}
	_ = json.Unmarshal(raw, &parsed)
	if response.StatusCode >= 200 && response.StatusCode < 300 && parsed.OK {
		return nil
	}
	if parsed.Description != "" {
		return fmt.Errorf("Telegram rejected the message (status %d): %s", response.StatusCode, Scrub(parsed.Description, token))
	}
	return fmt.Errorf("Telegram rejected the message (status %d)", response.StatusCode)
}

func capText(text string) string {
	runes := []rune(text)
	if len(runes) <= telegramMessageLimit {
		return text
	}
	return string(runes[:telegramMessageLimit-1]) + "…"
}

// Scrub removes token (and anything shaped like a bot token) from s and caps
// its length so it is safe to store or return.
func Scrub(s, token string) string {
	if token != "" {
		s = strings.ReplaceAll(s, token, "[token]")
	}
	s = tokenInText.ReplaceAllString(s, "[token]")
	if r := []rune(s); len(r) > 300 {
		s = string(r[:300])
	}
	return s
}
