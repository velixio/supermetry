package alerts

import (
	"bytes"
	"context"
	"encoding/json"
	"errors"
	"fmt"
	"io"
	"net"
	"net/http"
	"regexp"
	"time"
)

const (
	telegramMessageLimit = 4096
	telegramTimeout      = 5 * time.Second
	telegramAttempts     = 3
	telegramMaxRetryWait = 10 * time.Second
	telegramResponseSize = 16 << 10
)

var (
	telegramTokenPattern = regexp.MustCompile(`^[0-9]{5,20}:[A-Za-z0-9_-]{20,200}$`)
	telegramChatPattern  = regexp.MustCompile(`^(?:-?[0-9]{1,20}|@[A-Za-z0-9_]{5,32})$`)
)

// TelegramSender is the narrow provider boundary used by the alert dispatcher.
type TelegramSender interface {
	SendMessage(context.Context, string, string, string) error
}

type TelegramOptions struct {
	Transport http.RoundTripper
	Sleep     func(context.Context, time.Duration) error
}

type TelegramClient struct {
	client *http.Client
	sleep  func(context.Context, time.Duration) error
}

func NewTelegramClient(options TelegramOptions) *TelegramClient {
	transport := options.Transport
	if transport == nil {
		transport = &http.Transport{
			Proxy:                 nil,
			DialContext:           (&net.Dialer{Timeout: telegramTimeout}).DialContext,
			TLSHandshakeTimeout:   telegramTimeout,
			ResponseHeaderTimeout: telegramTimeout,
		}
	}
	sleep := options.Sleep
	if sleep == nil {
		sleep = sleepContext
	}
	return &TelegramClient{
		client: &http.Client{
			Timeout:       telegramTimeout,
			Transport:     transport,
			CheckRedirect: func(*http.Request, []*http.Request) error { return http.ErrUseLastResponse },
		},
		sleep: sleep,
	}
}

func ValidTelegramToken(token string) bool { return telegramTokenPattern.MatchString(token) }

func ValidTelegramChatID(chatID string) bool { return telegramChatPattern.MatchString(chatID) }

func (c *TelegramClient) SendMessage(ctx context.Context, token, chatID, text string) error {
	if !ValidTelegramToken(token) || !ValidTelegramChatID(chatID) {
		return errors.New("invalid Telegram delivery configuration")
	}
	body, err := json.Marshal(struct {
		ChatID string `json:"chat_id"`
		Text   string `json:"text"`
	}{ChatID: chatID, Text: boundTelegramText(text)})
	if err != nil {
		return errors.New("encode Telegram request")
	}
	endpoint := "https://api.telegram.org/bot" + token + "/sendMessage"
	for attempt := 0; attempt < telegramAttempts; attempt++ {
		response, err := c.send(ctx, endpoint, body)
		if err != nil {
			return err
		}
		if response.OK && response.StatusCode >= 200 && response.StatusCode < 300 {
			return nil
		}
		if response.StatusCode != http.StatusTooManyRequests || response.Parameters.RetryAfter <= 0 {
			return fmt.Errorf("Telegram delivery was rejected (status %d)", response.StatusCode)
		}
		wait := time.Duration(response.Parameters.RetryAfter) * time.Second
		if wait > telegramMaxRetryWait || attempt+1 == telegramAttempts {
			return errors.New("Telegram delivery was rate limited")
		}
		if err := c.sleep(ctx, wait); err != nil {
			return err
		}
	}
	return errors.New("Telegram delivery failed")
}

type telegramAPIResponse struct {
	OK         bool `json:"ok"`
	StatusCode int
	Parameters struct {
		RetryAfter int `json:"retry_after"`
	} `json:"parameters"`
}

func (c *TelegramClient) send(ctx context.Context, endpoint string, body []byte) (telegramAPIResponse, error) {
	request, err := http.NewRequestWithContext(ctx, http.MethodPost, endpoint, bytes.NewReader(body))
	if err != nil {
		return telegramAPIResponse{}, errors.New("build Telegram request")
	}
	request.Header.Set("Content-Type", "application/json")
	response, err := c.client.Do(request)
	if err != nil {
		if ctx.Err() != nil {
			return telegramAPIResponse{}, ctx.Err()
		}
		// net/http errors can contain the request URL, whose path contains the token.
		return telegramAPIResponse{}, errors.New("Telegram request failed")
	}
	defer response.Body.Close()
	responseBody, err := io.ReadAll(io.LimitReader(response.Body, telegramResponseSize))
	if err != nil {
		return telegramAPIResponse{}, errors.New("read Telegram response")
	}
	parsed := telegramAPIResponse{StatusCode: response.StatusCode}
	if err := json.Unmarshal(responseBody, &parsed); err != nil {
		return telegramAPIResponse{}, fmt.Errorf("Telegram delivery returned status %d", response.StatusCode)
	}
	parsed.StatusCode = response.StatusCode
	return parsed, nil
}

func boundTelegramText(text string) string {
	runes := []rune(text)
	if len(runes) <= telegramMessageLimit {
		return text
	}
	return string(runes[:telegramMessageLimit])
}
