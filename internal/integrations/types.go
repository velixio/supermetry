// Package integrations connects a site to third-party data providers. A
// Provider implements the OAuth handshake and the daily fetch; the Service
// stores sealed credentials, runs the scheduled import and owns disconnects.
package integrations

import (
	"context"
	"errors"
	"time"
)

// Limits shared by the service, the API and the migration constraints.
const (
	MaxBackfillDays     = 485 // Search Console keeps about 16 months
	DefaultBackfillDays = 28
	MaxDaysPerRun       = 7    // bounded fetch window per connection and run
	RefetchDays         = 3    // recent days are re-imported until final
	MaxRowsPerDay       = 5000 // top rows by clicks kept per day
	MaxRowsPerSite      = 3_000_000
	RetentionDays       = 490
	MaxPropertyBytes    = 512
	MaxQueryBytes       = 1024
	MaxPageBytes        = 2048
)

var (
	ErrForbidden      = errors.New("forbidden")
	ErrInvalid        = errors.New("invalid integration request")
	ErrNotFound       = errors.New("integration not found")
	ErrUnavailable    = errors.New("integration provider is not configured")
	ErrInvalidState   = errors.New("authorization state is invalid or expired")
	ErrRateLimited    = errors.New("integration request rate limit exceeded")
	ErrNoRefreshToken = errors.New("provider did not return a refresh token")
)

// ErrorKind classifies provider failures for the retry policy.
type ErrorKind int

const (
	// KindTransient failures (network, 5xx, quota) are retried with backoff.
	KindTransient ErrorKind = iota
	// KindAuth failures (revoked or expired grant) need the user to reconnect.
	KindAuth
)

// ProviderError carries a safe, token-free message.
type ProviderError struct {
	Kind ErrorKind
	Msg  string
}

func (e *ProviderError) Error() string { return e.Msg }

// Tokens are the OAuth credentials of one connection.
type Tokens struct {
	AccessToken  string    `json:"access_token"`
	RefreshToken string    `json:"refresh_token"`
	Expiry       time.Time `json:"expiry"`
}

// AuthParams are the inputs to the provider authorization URL.
type AuthParams struct {
	State         string
	RedirectURI   string
	CodeChallenge string // S256 PKCE challenge
}

// Row is one daily Search Console record.
type Row struct {
	Day         time.Time
	Query       string
	Page        string
	Country     string
	Device      string
	Clicks      int
	Impressions int
	CTR         float64
	Position    float64
}

// Provider is the contract every integration implements.
type Provider interface {
	// ID is the stable provider slug used in URLs and storage.
	ID() string
	// Name is the display name.
	Name() string
	// Configured reports whether client credentials are set; unconfigured
	// providers are hidden from the catalog.
	Configured() bool
	AuthURL(AuthParams) string
	Exchange(ctx context.Context, code, redirectURI, codeVerifier string) (Tokens, error)
	Refresh(ctx context.Context, refreshToken string) (Tokens, error)
	// Fetch returns up to maxRows rows (highest clicks first) for one day.
	Fetch(ctx context.Context, accessToken, property string, day time.Time, maxRows int) ([]Row, error)
	// Revoke asks the provider to invalidate a token; failures are ignored.
	Revoke(ctx context.Context, token string) error
}

// Connection is the API view of a connection; it never carries credentials.
type Connection struct {
	ID            int64      `json:"id"`
	Provider      string     `json:"provider"`
	Property      string     `json:"property"`
	Status        string     `json:"status"`
	SyncedThrough *string    `json:"synced_through,omitempty"`
	LastSyncAt    *time.Time `json:"last_sync_at,omitempty"`
	LastError     string     `json:"last_error,omitempty"`
	CreatedAt     time.Time  `json:"created_at"`
}

// CatalogEntry is one provider in the site's integration catalog.
type CatalogEntry struct {
	Provider   string      `json:"provider"`
	Name       string      `json:"name"`
	Available  bool        `json:"available"`
	Connection *Connection `json:"connection,omitempty"`
}

// StartInput begins an OAuth connection.
type StartInput struct {
	Provider     string
	Property     string
	BackfillDays int
}

// TopRow is an aggregated query or page.
type TopRow struct {
	Key         string  `json:"key"`
	Clicks      int64   `json:"clicks"`
	Impressions int64   `json:"impressions"`
	CTR         float64 `json:"ctr"`
	Position    float64 `json:"position"`
}

// TopResult is the read endpoint payload.
type TopResult struct {
	Dimension string   `json:"dimension"`
	From      string   `json:"from"`
	To        string   `json:"to"`
	Totals    TopRow   `json:"totals"`
	Rows      []TopRow `json:"rows"`
}
