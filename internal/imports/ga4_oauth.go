package imports

import (
	"bytes"
	"context"
	"crypto/rand"
	"crypto/sha256"
	"encoding/base64"
	"encoding/json"
	"errors"
	"fmt"
	"net/http"
	"net/url"
	"strings"
	"time"

	"github.com/jackc/pgx/v5"
)

var (
	// ErrGA4NotConfigured is returned while the OAuth client credentials are unset.
	ErrGA4NotConfigured = errors.New("Google Analytics import is not configured: set SUPERMETRY_GA4_OAUTH_CLIENT_ID and SUPERMETRY_GA4_OAUTH_CLIENT_SECRET")
	ErrGA4InvalidState  = errors.New("authorization state is invalid or expired")
	ErrGA4Denied        = errors.New("Google authorization was denied")
	ErrGA4NoRefresh     = errors.New("Google did not return a refresh token; remove the app from your Google account permissions and connect again")
	ErrGA4Connection    = errors.New("Google Analytics connection not found")
)

const ga4StateTTL = 10 * time.Minute

func ga4Random() (string, error) {
	raw := make([]byte, 32)
	if _, err := rand.Read(raw); err != nil {
		return "", fmt.Errorf("generate random token: %w", err)
	}
	return base64.RawURLEncoding.EncodeToString(raw), nil
}

func ga4StateHash(state string) []byte {
	sum := sha256.Sum256([]byte(state))
	return sum[:]
}

// StartGA4 records a one-time state bound to the user, organization and site,
// with a PKCE verifier, and returns the Google consent URL.
func (s *PullService) StartGA4(ctx context.Context, userID, orgID int64, siteID int32) (string, error) {
	if !s.ga4.Configured() {
		return "", ErrGA4NotConfigured
	}
	state, err := ga4Random()
	if err != nil {
		return "", err
	}
	verifier, err := ga4Random()
	if err != nil {
		return "", err
	}
	challenge := sha256.Sum256([]byte(verifier))
	now := s.now()
	if _, err := s.store.pool.Exec(ctx, `delete from import_ga4_states where expires_at<=$1`, now); err != nil {
		return "", fmt.Errorf("purge expired states: %w", err)
	}
	if _, err := s.store.pool.Exec(ctx, `
		insert into import_ga4_states(state_hash,user_id,org_id,site_id,code_verifier,expires_at)
		values ($1,$2,$3,$4,$5,$6)`, ga4StateHash(state), userID, orgID, siteID, s.box.Seal([]byte(verifier)), now.Add(ga4StateTTL)); err != nil {
		return "", fmt.Errorf("store authorization state: %w", err)
	}
	values := url.Values{
		"client_id": {s.ga4.ClientID}, "redirect_uri": {s.ga4.RedirectURI()}, "response_type": {"code"},
		"scope": {ga4Scope}, "state": {state}, "access_type": {"offline"}, "prompt": {"consent"},
		"code_challenge": {base64.RawURLEncoding.EncodeToString(challenge[:])}, "code_challenge_method": {"S256"},
	}
	return s.ga4.AuthURL + "?" + values.Encode(), nil
}

// CallbackGA4 completes the handshake: it consumes the state (exactly once,
// only for the same user and organization), exchanges the code and stores the
// refresh token sealed and scoped to the organization. It returns the site
// the flow started from, which the caller uses to redirect.
func (s *PullService) CallbackGA4(ctx context.Context, userID, orgID int64, code, state, providerError string) (int32, error) {
	if !s.ga4.Configured() {
		return 0, ErrGA4NotConfigured
	}
	if state == "" || len(state) > 128 {
		return 0, ErrGA4InvalidState
	}
	var (
		stateUser, stateOrg int64
		siteID              int32
		sealedVerifier      []byte
		expires             time.Time
	)
	err := s.store.pool.QueryRow(ctx, `
		delete from import_ga4_states where state_hash=$1
		returning user_id,org_id,site_id,code_verifier,expires_at`, ga4StateHash(state)).
		Scan(&stateUser, &stateOrg, &siteID, &sealedVerifier, &expires)
	if errors.Is(err, pgx.ErrNoRows) {
		return 0, ErrGA4InvalidState
	}
	if err != nil {
		return 0, fmt.Errorf("consume authorization state: %w", err)
	}
	if !s.now().Before(expires) || stateUser != userID || stateOrg != orgID {
		return 0, ErrGA4InvalidState
	}
	if providerError != "" {
		return siteID, ErrGA4Denied
	}
	if code == "" || len(code) > 2048 {
		return siteID, ErrGA4InvalidState
	}
	verifier, err := s.box.Open(sealedVerifier)
	if err != nil {
		return siteID, errors.New("decrypt code verifier")
	}
	var token tokenReply
	if err := s.tokenRequest(ctx, url.Values{
		"grant_type": {"authorization_code"}, "code": {code},
		"redirect_uri": {s.ga4.RedirectURI()}, "code_verifier": {string(verifier)},
	}, &token); err != nil {
		return siteID, err
	}
	if token.RefreshToken == "" {
		return siteID, ErrGA4NoRefresh
	}
	if _, err := s.store.pool.Exec(ctx, `
		insert into import_ga4_connections(org_id,refresh_token,connected_by) values ($1,$2,$3)`,
		orgID, s.box.Seal([]byte(token.RefreshToken)), userID); err != nil {
		return siteID, fmt.Errorf("save connection: %w", err)
	}
	return siteID, nil
}

type tokenReply struct {
	AccessToken  string `json:"access_token"`
	RefreshToken string `json:"refresh_token"`
	ExpiresIn    int64  `json:"expires_in"`
	Error        string `json:"error"`
}

// tokenRequest posts to Google's token endpoint. Response bodies are never
// copied into errors, so no token can reach a log line.
func (s *PullService) tokenRequest(ctx context.Context, form url.Values, into *tokenReply) error {
	form.Set("client_id", s.ga4.ClientID)
	form.Set("client_secret", s.ga4.ClientSecret)
	encoded := []byte(form.Encode())
	status, raw, err := s.client.do(ctx, s.ga4.ClientSecret, func() (*http.Request, error) {
		request, err := http.NewRequest(http.MethodPost, s.ga4.TokenURL, bytes.NewReader(encoded))
		if err != nil {
			return nil, err
		}
		request.Header.Set("Content-Type", "application/x-www-form-urlencoded")
		return request, nil
	})
	if err != nil {
		return err
	}
	_ = json.Unmarshal(raw, into)
	switch {
	case status == http.StatusOK && into.AccessToken != "":
		return nil
	case status == http.StatusBadRequest || status == http.StatusUnauthorized:
		if into.Error == "invalid_grant" || status == http.StatusUnauthorized {
			return fmt.Errorf("%w: Google authorization was revoked or expired; reconnect", ErrRemoteAuth)
		}
		return errors.New("Google rejected the token request")
	}
	return fmt.Errorf("Google token endpoint answered HTTP %d", status)
}

// ga4Refresh exchanges a refresh token for an access token.
func (s *PullService) ga4Refresh(ctx context.Context, refreshToken string) (string, time.Duration, error) {
	var token tokenReply
	if err := s.tokenRequest(ctx, url.Values{"grant_type": {"refresh_token"}, "refresh_token": {refreshToken}}, &token); err != nil {
		return "", 0, err
	}
	lifetime := time.Duration(token.ExpiresIn) * time.Second
	if lifetime <= 0 {
		lifetime = time.Hour
	}
	return token.AccessToken, lifetime, nil
}

// ga4RefreshToken loads and decrypts a connection's refresh token, requiring
// that it belongs to the organization.
func (s *PullService) ga4RefreshToken(ctx context.Context, orgID, connectionID int64) (string, error) {
	var sealed []byte
	err := s.store.pool.QueryRow(ctx, `select refresh_token from import_ga4_connections where id=$1 and org_id=$2`,
		connectionID, orgID).Scan(&sealed)
	if errors.Is(err, pgx.ErrNoRows) {
		return "", ErrGA4Connection
	}
	if err != nil {
		return "", err
	}
	plain, err := s.box.Open(sealed)
	if err != nil {
		return "", errors.New("the stored Google credentials can no longer be decrypted; reconnect")
	}
	return string(plain), nil
}

// GA4Connection is the API view of a stored connection; it carries no secret.
type GA4Connection struct {
	ID        int64     `json:"id"`
	CreatedAt time.Time `json:"created_at"`
}

// ListGA4Connections lists the organization's connections.
func (s *PullService) ListGA4Connections(ctx context.Context, orgID int64) ([]GA4Connection, error) {
	if !s.ga4.Configured() {
		return nil, ErrGA4NotConfigured
	}
	rows, err := s.store.pool.Query(ctx, `select id,created_at from import_ga4_connections where org_id=$1 order by id`, orgID)
	if err != nil {
		return nil, err
	}
	defer rows.Close()
	result := make([]GA4Connection, 0)
	for rows.Next() {
		var connection GA4Connection
		if err := rows.Scan(&connection.ID, &connection.CreatedAt); err != nil {
			return nil, err
		}
		result = append(result, connection)
	}
	return result, rows.Err()
}

// DeleteGA4Connection removes a connection and asks Google to revoke the
// token; revocation failures are ignored.
func (s *PullService) DeleteGA4Connection(ctx context.Context, orgID, connectionID int64) error {
	if !s.ga4.Configured() {
		return ErrGA4NotConfigured
	}
	refresh, err := s.ga4RefreshToken(ctx, orgID, connectionID)
	if err != nil {
		return err
	}
	if _, err := s.store.pool.Exec(ctx, `delete from import_ga4_connections where id=$1 and org_id=$2`, connectionID, orgID); err != nil {
		return err
	}
	encoded := []byte(url.Values{"token": {refresh}}.Encode())
	_, _, _ = s.client.do(ctx, refresh, func() (*http.Request, error) {
		request, err := http.NewRequest(http.MethodPost, s.ga4.RevokeURL, bytes.NewReader(encoded))
		if err != nil {
			return nil, err
		}
		request.Header.Set("Content-Type", "application/x-www-form-urlencoded")
		return request, nil
	})
	return nil
}

// GA4Property is one Analytics property the connected account can read.
type GA4Property struct {
	Property    string `json:"property"`
	DisplayName string `json:"display_name"`
	Account     string `json:"account"`
}

const maxGA4Properties = 1000

// ListGA4Properties lists the properties visible to a connection through the
// Admin API account summaries.
func (s *PullService) ListGA4Properties(ctx context.Context, orgID, connectionID int64) ([]GA4Property, error) {
	if !s.ga4.Configured() {
		return nil, ErrGA4NotConfigured
	}
	refresh, err := s.ga4RefreshToken(ctx, orgID, connectionID)
	if err != nil {
		return nil, err
	}
	access, _, err := s.ga4Refresh(ctx, refresh)
	if err != nil {
		return nil, &RemoteError{Err: err}
	}
	properties := make([]GA4Property, 0)
	pageToken := ""
	for {
		query := url.Values{"pageSize": {"200"}}
		if pageToken != "" {
			query.Set("pageToken", pageToken)
		}
		endpoint := strings.TrimRight(s.ga4.AdminBase, "/") + "/v1beta/accountSummaries?" + query.Encode()
		status, raw, err := s.client.do(ctx, access, func() (*http.Request, error) {
			request, err := http.NewRequest(http.MethodGet, endpoint, nil)
			if err != nil {
				return nil, err
			}
			request.Header.Set("Authorization", "Bearer "+access)
			return request, nil
		})
		if err != nil {
			return nil, &RemoteError{Err: err}
		}
		if status == http.StatusUnauthorized || status == http.StatusForbidden {
			return nil, &RemoteError{Err: fmt.Errorf("%w: the Google account cannot list Analytics properties; reconnect", ErrRemoteAuth)}
		}
		if status != http.StatusOK {
			return nil, &RemoteError{Err: fmt.Errorf("Google Analytics Admin API answered HTTP %d", status)}
		}
		var page struct {
			AccountSummaries []struct {
				Account           string `json:"account"`
				DisplayName       string `json:"displayName"`
				PropertySummaries []struct {
					Property    string `json:"property"`
					DisplayName string `json:"displayName"`
				} `json:"propertySummaries"`
			} `json:"accountSummaries"`
			NextPageToken string `json:"nextPageToken"`
		}
		if err := json.Unmarshal(raw, &page); err != nil {
			return nil, errors.New("Google Analytics Admin API returned an unexpected response")
		}
		for _, account := range page.AccountSummaries {
			for _, property := range account.PropertySummaries {
				if ga4PropertyPattern.MatchString(property.Property) && len(properties) < maxGA4Properties {
					properties = append(properties, GA4Property{Property: property.Property, DisplayName: property.DisplayName, Account: account.DisplayName})
				}
			}
		}
		if page.NextPageToken == "" || len(properties) >= maxGA4Properties {
			return properties, nil
		}
		pageToken = page.NextPageToken
	}
}
