package integrations

import (
	"bytes"
	"context"
	"encoding/json"
	"fmt"
	"io"
	"net"
	"net/http"
	"net/url"
	"time"

	"supermetry/internal/safehttp"
)

const (
	// SearchConsoleID is the provider slug for Google Search Console.
	SearchConsoleID = "search-console"

	googleScope      = "https://www.googleapis.com/auth/webmasters.readonly"
	googleHTTPTimout = 20 * time.Second
	googlePageRows   = 5000
	maxResponseBytes = 8 << 20
)

// GoogleOptions configures the Search Console provider. Endpoint overrides
// exist so tests can target a local server; AllowPrivate must stay false in
// production so every outbound call is SSRF-guarded.
type GoogleOptions struct {
	ClientID     string
	ClientSecret string
	AuthURL      string
	TokenURL     string
	RevokeURL    string
	APIBase      string
	AllowPrivate bool
	Now          func() time.Time
}

// Google implements Provider for Search Console.
type Google struct {
	opts   GoogleOptions
	client *http.Client
}

// NewGoogle builds the provider with a guarded HTTP client that never follows
// redirects and never uses proxies.
func NewGoogle(opts GoogleOptions) *Google {
	if opts.AuthURL == "" {
		opts.AuthURL = "https://accounts.google.com/o/oauth2/v2/auth"
	}
	if opts.TokenURL == "" {
		opts.TokenURL = "https://oauth2.googleapis.com/token"
	}
	if opts.RevokeURL == "" {
		opts.RevokeURL = "https://oauth2.googleapis.com/revoke"
	}
	if opts.APIBase == "" {
		opts.APIBase = "https://searchconsole.googleapis.com/webmasters/v3"
	}
	if opts.Now == nil {
		opts.Now = time.Now
	}
	dialer := &net.Dialer{Timeout: googleHTTPTimout}
	return &Google{opts: opts, client: &http.Client{
		Timeout: googleHTTPTimout,
		Transport: &http.Transport{
			Proxy:                 nil,
			DialContext:           safehttp.GuardedDial(net.DefaultResolver, dialer.DialContext, opts.AllowPrivate),
			TLSHandshakeTimeout:   googleHTTPTimout,
			ResponseHeaderTimeout: googleHTTPTimout,
		},
		CheckRedirect: func(*http.Request, []*http.Request) error { return http.ErrUseLastResponse },
	}}
}

func (g *Google) ID() string       { return SearchConsoleID }
func (g *Google) Name() string     { return "Google Search Console" }
func (g *Google) Configured() bool { return g.opts.ClientID != "" && g.opts.ClientSecret != "" }

func (g *Google) AuthURL(p AuthParams) string {
	values := url.Values{
		"client_id": {g.opts.ClientID}, "redirect_uri": {p.RedirectURI}, "response_type": {"code"},
		"scope": {googleScope}, "state": {p.State}, "access_type": {"offline"}, "prompt": {"consent"},
		"code_challenge": {p.CodeChallenge}, "code_challenge_method": {"S256"},
	}
	return g.opts.AuthURL + "?" + values.Encode()
}

type tokenResponse struct {
	AccessToken  string `json:"access_token"`
	RefreshToken string `json:"refresh_token"`
	ExpiresIn    int64  `json:"expires_in"`
	Error        string `json:"error"`
}

func (g *Google) token(ctx context.Context, form url.Values) (Tokens, error) {
	form.Set("client_id", g.opts.ClientID)
	form.Set("client_secret", g.opts.ClientSecret)
	status, body, err := g.do(ctx, http.MethodPost, g.opts.TokenURL, "application/x-www-form-urlencoded", "", []byte(form.Encode()))
	if err != nil {
		return Tokens{}, err
	}
	var parsed tokenResponse
	_ = json.Unmarshal(body, &parsed)
	if status == http.StatusBadRequest || status == http.StatusUnauthorized {
		if parsed.Error == "invalid_grant" || status == http.StatusUnauthorized {
			return Tokens{}, &ProviderError{Kind: KindAuth, Msg: "authorization was revoked or expired; reconnect"}
		}
		return Tokens{}, &ProviderError{Kind: KindTransient, Msg: "token request was rejected"}
	}
	if status != http.StatusOK || parsed.AccessToken == "" {
		return Tokens{}, &ProviderError{Kind: KindTransient, Msg: fmt.Sprintf("token endpoint returned status %d", status)}
	}
	return Tokens{
		AccessToken: parsed.AccessToken, RefreshToken: parsed.RefreshToken,
		Expiry: g.opts.Now().Add(time.Duration(parsed.ExpiresIn) * time.Second),
	}, nil
}

func (g *Google) Exchange(ctx context.Context, code, redirectURI, verifier string) (Tokens, error) {
	return g.token(ctx, url.Values{"grant_type": {"authorization_code"}, "code": {code}, "redirect_uri": {redirectURI}, "code_verifier": {verifier}})
}

func (g *Google) Refresh(ctx context.Context, refreshToken string) (Tokens, error) {
	return g.token(ctx, url.Values{"grant_type": {"refresh_token"}, "refresh_token": {refreshToken}})
}

func (g *Google) Revoke(ctx context.Context, token string) error {
	_, _, err := g.do(ctx, http.MethodPost, g.opts.RevokeURL, "application/x-www-form-urlencoded", "", []byte(url.Values{"token": {token}}.Encode()))
	return err
}

func (g *Google) Fetch(ctx context.Context, accessToken, property string, day time.Time, maxRows int) ([]Row, error) {
	endpoint := g.opts.APIBase + "/sites/" + url.PathEscape(property) + "/searchAnalytics/query"
	date := day.UTC().Format(time.DateOnly)
	var rows []Row
	for start := 0; start < maxRows; start += googlePageRows {
		request, err := json.Marshal(map[string]any{
			"startDate": date, "endDate": date, "dataState": "final",
			"dimensions": []string{"query", "page", "country", "device"},
			"rowLimit":   min(googlePageRows, maxRows-start), "startRow": start,
		})
		if err != nil {
			return nil, err
		}
		status, body, err := g.do(ctx, http.MethodPost, endpoint, "application/json", accessToken, request)
		if err != nil {
			return nil, err
		}
		switch {
		case status == http.StatusUnauthorized || status == http.StatusForbidden || status == http.StatusNotFound:
			return nil, &ProviderError{Kind: KindAuth, Msg: "access to the property was lost; reconnect"}
		case status == http.StatusTooManyRequests:
			return nil, &ProviderError{Kind: KindTransient, Msg: "provider quota exceeded"}
		case status != http.StatusOK:
			return nil, &ProviderError{Kind: KindTransient, Msg: fmt.Sprintf("provider returned status %d", status)}
		}
		var parsed struct {
			Rows []struct {
				Keys        []string `json:"keys"`
				Clicks      float64  `json:"clicks"`
				Impressions float64  `json:"impressions"`
				CTR         float64  `json:"ctr"`
				Position    float64  `json:"position"`
			} `json:"rows"`
		}
		if err := json.Unmarshal(body, &parsed); err != nil {
			return nil, &ProviderError{Kind: KindTransient, Msg: "provider returned an unreadable response"}
		}
		for _, item := range parsed.Rows {
			if len(item.Keys) != 4 {
				continue
			}
			rows = append(rows, Row{
				Day: day, Query: item.Keys[0], Page: item.Keys[1], Country: item.Keys[2], Device: item.Keys[3],
				Clicks: int(item.Clicks), Impressions: int(item.Impressions), CTR: item.CTR, Position: item.Position,
			})
		}
		if len(parsed.Rows) < googlePageRows {
			break
		}
	}
	return rows, nil
}

// do performs one guarded request. Error text never includes the URL, headers
// or body, because those can carry credentials.
func (g *Google) do(ctx context.Context, method, target, contentType, bearer string, body []byte) (int, []byte, error) {
	request, err := http.NewRequestWithContext(ctx, method, target, bytes.NewReader(body))
	if err != nil {
		return 0, nil, &ProviderError{Kind: KindTransient, Msg: "invalid provider request"}
	}
	request.Header.Set("Content-Type", contentType)
	if bearer != "" {
		request.Header.Set("Authorization", "Bearer "+bearer)
	}
	response, err := g.client.Do(request)
	if err != nil {
		return 0, nil, &ProviderError{Kind: KindTransient, Msg: "provider is unreachable"}
	}
	defer response.Body.Close()
	data, err := io.ReadAll(io.LimitReader(response.Body, maxResponseBytes))
	if err != nil {
		return 0, nil, &ProviderError{Kind: KindTransient, Msg: "provider response was interrupted"}
	}
	return response.StatusCode, data, nil
}
