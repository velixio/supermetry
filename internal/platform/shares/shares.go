// Package shares manages read-only shared dashboard links.
package shares

import (
	"context"
	"crypto/hmac"
	"crypto/sha256"
	"encoding/base64"
	"errors"
	"sort"
	"strconv"
	"strings"
	"sync"
	"time"

	"github.com/jackc/pgx/v5"

	"supermetry/internal/platform/audit"
	"supermetry/internal/platform/authz"
	platformcrypto "supermetry/internal/platform/crypto"
	"supermetry/internal/platform/store"
)

const (
	shareSessionLifetime = 12 * time.Hour
	attemptBucketCap     = 4096
)

var (
	ErrForbidden        = errors.New("forbidden")
	ErrNotFound         = errors.New("shared link not found")
	ErrPasswordRequired = errors.New("share password required")
	ErrInvalid          = errors.New("invalid shared link or password")
	ErrRateLimited      = errors.New("too many share password attempts")
)

type clientKeyContextKey struct{}

// WithClientKey attaches an ephemeral, caller-derived client key. It is used
// only by the per-process in-memory password-attempt limiter.
func WithClientKey(ctx context.Context, key string) context.Context {
	return context.WithValue(ctx, clientKeyContextKey{}, strings.TrimSpace(key))
}

type attemptBucket struct {
	tokens float64
	last   time.Time
}

type attemptLimiter struct {
	mu      sync.Mutex
	targets map[string]attemptBucket
	clients map[string]attemptBucket
	attempt uint64
}

type ShareInfo struct {
	ID                int64      `json:"id"`
	SiteID            int32      `json:"site_id"`
	CreatedBy         int64      `json:"created_by"`
	CreatedAt         time.Time  `json:"created_at"`
	ExpiresAt         *time.Time `json:"expires_at"`
	RevokedAt         *time.Time `json:"revoked_at,omitempty"`
	PasswordProtected bool       `json:"password_protected"`
}

type Shares struct {
	store     *store.Store
	now       func() time.Time
	limiter   attemptLimiter
	secretMu  sync.RWMutex
	cookieKey []byte
}

func New(data *store.Store, secrets ...[]byte) *Shares {
	service := &Shares{store: data, now: time.Now, limiter: attemptLimiter{
		targets: make(map[string]attemptBucket), clients: make(map[string]attemptBucket),
	}}
	if len(secrets) > 0 {
		service.SetCookieSecret(secrets[0])
	}
	return service
}

// SetCookieSecret derives a purpose-specific signing key from
// SUPERMETRY_SECRET_KEY. Changing that server secret invalidates existing
// share-unlock cookies by design.
func (s *Shares) SetCookieSecret(secret []byte) {
	mac := hmac.New(sha256.New, secret)
	_, _ = mac.Write([]byte("supermetry/share-unlock-cookie/v1"))
	s.secretMu.Lock()
	s.cookieKey = mac.Sum(nil)
	s.secretMu.Unlock()
}

func (s *Shares) Create(ctx context.Context, principal authz.Principal, siteID int32, password string) (slug string, info ShareInfo, err error) {
	return s.CreateWithExpiry(ctx, principal, siteID, password, nil)
}

func (s *Shares) CreateWithExpiry(ctx context.Context, principal authz.Principal, siteID int32, password string, expiresInDays *int) (slug string, info ShareInfo, err error) {
	if !authz.Can(principal, authz.ActionSharesManage, siteID) || principal.UserID == 0 {
		return "", ShareInfo{}, ErrForbidden
	}
	expiresAt, err := expiryFromDays(s.now(), expiresInDays)
	if err != nil {
		return "", ShareInfo{}, err
	}

	slug, slugHash := platformcrypto.NewToken("shr_")
	var passwordHash *string
	if password != "" {
		if err := platformcrypto.ValidateNewPassword(password); err != nil {
			return "", ShareInfo{}, err
		}
		hash, hashErr := platformcrypto.HashPassword(password)
		if hashErr != nil {
			return "", ShareInfo{}, hashErr
		}
		passwordHash = &hash
	}

	var link store.SharedLink
	err = s.store.Tx(ctx, func(tx pgx.Tx) error {
		current, err := store.CurrentMemberForUpdate(ctx, tx, principal.OrgID, principal.UserID)
		if errors.Is(err, pgx.ErrNoRows) {
			return ErrForbidden
		}
		if err != nil {
			return err
		}
		if !authz.Can(current, authz.ActionSharesManage, siteID) {
			return ErrForbidden
		}
		var createErr error
		link, createErr = store.CreateSharedLink(ctx, tx, principal.OrgID, store.SharedLink{
			SiteID: siteID, SlugHash: slugHash, PasswordHash: passwordHash,
			CreatedBy: principal.UserID, ExpiresAt: expiresAt,
		})
		if createErr != nil {
			if errors.Is(createErr, pgx.ErrNoRows) {
				return ErrNotFound
			}
			return createErr
		}
		return audit.Write(ctx, tx, principal, "share.created", "shared_link", link.ID, map[string]any{
			"site_id": siteID, "password_protected": passwordHash != nil, "expires_at": expiresAt,
		})
	})
	if err != nil {
		return "", ShareInfo{}, err
	}
	return slug, shareInfo(link), nil
}

func (s *Shares) Open(ctx context.Context, slug, password string) (cookieValue string, err error) {
	now := s.now()
	client, _ := ctx.Value(clientKeyContextKey{}).(string)
	target := base64.RawURLEncoding.EncodeToString(platformcrypto.HashToken(slug))
	if !s.limiter.allow(target, client, now) {
		return "", ErrRateLimited
	}
	link, err := s.lookup(ctx, slug)
	if err != nil || unavailable(link, now) {
		return "", ErrInvalid
	}
	if link.PasswordHash == nil {
		return "", nil
	}
	if password == "" {
		return "", ErrPasswordRequired
	}
	valid, err := platformcrypto.VerifyPassword(*link.PasswordHash, password)
	if err != nil || !valid {
		return "", ErrInvalid
	}
	key := s.cookieSecret()
	if len(key) == 0 {
		return "", errors.New("share cookie signing is unavailable")
	}
	return signCookie(slug, now.Add(shareSessionLifetime), key), nil
}

func (s *Shares) Principal(ctx context.Context, slug, cookieValue string) (authz.Principal, bool) {
	principal, err := s.Resolve(ctx, slug, cookieValue)
	return principal, err == nil
}

func (s *Shares) Resolve(ctx context.Context, slug, cookieValue string) (authz.Principal, error) {
	now := s.now()
	link, err := s.lookup(ctx, slug)
	if err != nil || unavailable(link, now) {
		return authz.Principal{}, ErrNotFound
	}
	if link.PasswordHash != nil && !validCookie(slug, cookieValue, s.cookieSecret(), now) {
		return authz.Principal{}, ErrPasswordRequired
	}
	return authz.Principal{
		OrgID: link.OrgID, ShareID: link.ID, Role: authz.RoleShare,
		SiteIDs: []int32{link.SiteID},
	}, nil
}

func (s *Shares) ChangeExpiry(ctx context.Context, principal authz.Principal, id int64, expiresInDays *int) error {
	if principal.UserID == 0 {
		return ErrForbidden
	}
	expiresAt, err := expiryFromDays(s.now(), expiresInDays)
	if err != nil {
		return err
	}
	return s.store.Tx(ctx, func(tx pgx.Tx) error {
		current, err := store.CurrentMemberForUpdate(ctx, tx, principal.OrgID, principal.UserID)
		if errors.Is(err, pgx.ErrNoRows) {
			return ErrForbidden
		}
		if err != nil {
			return err
		}
		link, err := store.SharedLinkByIDForUpdate(ctx, tx, principal.OrgID, id)
		if err != nil {
			if errors.Is(err, pgx.ErrNoRows) {
				return ErrNotFound
			}
			return err
		}
		if !authz.Can(principal, authz.ActionSharesManage, link.SiteID) || !authz.Can(current, authz.ActionSharesManage, link.SiteID) {
			return ErrForbidden
		}
		if err := store.ChangeSharedLinkExpiry(ctx, tx, principal.OrgID, id, expiresAt); err != nil {
			if errors.Is(err, pgx.ErrNoRows) {
				return ErrNotFound
			}
			return err
		}
		return audit.Write(ctx, tx, principal, "share.expiry_changed", "shared_link", id, map[string]any{
			"expires_at": expiresAt,
		})
	})
}

func (s *Shares) Revoke(ctx context.Context, principal authz.Principal, id int64) error {
	if principal.UserID == 0 {
		return ErrForbidden
	}
	return s.store.Tx(ctx, func(tx pgx.Tx) error {
		current, err := store.CurrentMemberForUpdate(ctx, tx, principal.OrgID, principal.UserID)
		if errors.Is(err, pgx.ErrNoRows) {
			return ErrForbidden
		}
		if err != nil {
			return err
		}
		link, err := store.SharedLinkByIDForUpdate(ctx, tx, principal.OrgID, id)
		if err != nil {
			if errors.Is(err, pgx.ErrNoRows) {
				return ErrNotFound
			}
			return err
		}
		if !authz.Can(principal, authz.ActionSharesManage, link.SiteID) || !authz.Can(current, authz.ActionSharesManage, link.SiteID) {
			return ErrForbidden
		}
		if err := store.RevokeSharedLink(ctx, tx, principal.OrgID, id); err != nil {
			if errors.Is(err, pgx.ErrNoRows) {
				return ErrNotFound
			}
			return err
		}
		return audit.Write(ctx, tx, principal, "share.revoked", "shared_link", id, nil)
	})
}

func (s *Shares) lookup(ctx context.Context, slug string) (store.SharedLink, error) {
	if slug == "" {
		return store.SharedLink{}, ErrInvalid
	}
	link, err := store.SharedLinkBySlugHash(ctx, s.store, platformcrypto.HashToken(slug))
	if err != nil {
		return store.SharedLink{}, ErrInvalid
	}
	return link, nil
}

func (s *Shares) cookieSecret() []byte {
	s.secretMu.RLock()
	defer s.secretMu.RUnlock()
	return append([]byte(nil), s.cookieKey...)
}

func (l *attemptLimiter) allow(target, client string, now time.Time) bool {
	l.mu.Lock()
	defer l.mu.Unlock()
	l.attempt++
	if l.attempt%256 == 0 {
		pruneAttempts(l.targets, now)
		pruneAttempts(l.clients, now)
	}
	if client != "" && !takeAttempt(l.clients, client, 5, now) {
		return false
	}
	return takeAttempt(l.targets, target, 20, now)
}

func pruneAttempts(buckets map[string]attemptBucket, now time.Time) {
	for key, bucket := range buckets {
		if now.Sub(bucket.last) >= time.Minute {
			delete(buckets, key)
		}
	}
	if len(buckets) <= attemptBucketCap {
		return
	}
	type entry struct {
		key  string
		last time.Time
	}
	entries := make([]entry, 0, len(buckets))
	for key, bucket := range buckets {
		entries = append(entries, entry{key: key, last: bucket.last})
	}
	sort.Slice(entries, func(i, j int) bool {
		if entries[i].last.Equal(entries[j].last) {
			return entries[i].key < entries[j].key
		}
		return entries[i].last.Before(entries[j].last)
	})
	for _, entry := range entries[:len(entries)-attemptBucketCap] {
		delete(buckets, entry.key)
	}
}

func takeAttempt(buckets map[string]attemptBucket, key string, capacity float64, now time.Time) bool {
	bucket := buckets[key]
	if bucket.last.IsZero() {
		bucket.tokens = capacity
		bucket.last = now
	} else if now.After(bucket.last) {
		bucket.tokens = min(capacity, bucket.tokens+now.Sub(bucket.last).Minutes()*capacity)
		bucket.last = now
	}
	if bucket.tokens < 1 {
		buckets[key] = bucket
		return false
	}
	bucket.tokens--
	buckets[key] = bucket
	return true
}

func signCookie(slug string, expires time.Time, key []byte) string {
	expiry := strconv.FormatInt(expires.Unix(), 10)
	mac := cookieMAC(slug, expiry, key)
	return expiry + "." + base64.RawURLEncoding.EncodeToString(mac)
}

func validCookie(slug, value string, key []byte, now time.Time) bool {
	if len(key) == 0 {
		return false
	}
	expiry, encodedMAC, ok := strings.Cut(value, ".")
	if !ok || expiry == "" || encodedMAC == "" {
		return false
	}
	expiresUnix, err := strconv.ParseInt(expiry, 10, 64)
	if err != nil || !time.Unix(expiresUnix, 0).After(now) {
		return false
	}
	got, err := base64.RawURLEncoding.Strict().DecodeString(encodedMAC)
	if err != nil {
		return false
	}
	return hmac.Equal(got, cookieMAC(slug, expiry, key))
}

func cookieMAC(slug, expiry string, key []byte) []byte {
	mac := hmac.New(sha256.New, key)
	_, _ = mac.Write([]byte(slug))
	_, _ = mac.Write([]byte{0})
	_, _ = mac.Write([]byte(expiry))
	return mac.Sum(nil)
}

func expiryFromDays(now time.Time, expiresInDays *int) (*time.Time, error) {
	if expiresInDays == nil {
		return nil, nil
	}
	switch *expiresInDays {
	case 7, 30, 90, 365:
		expiresAt := now.Add(time.Duration(*expiresInDays) * 24 * time.Hour)
		return &expiresAt, nil
	default:
		return nil, ErrInvalid
	}
}

func unavailable(link store.SharedLink, now time.Time) bool {
	return link.RevokedAt != nil || link.ExpiresAt != nil && !link.ExpiresAt.After(now)
}

func shareInfo(link store.SharedLink) ShareInfo {
	return ShareInfo{
		ID: link.ID, SiteID: link.SiteID, CreatedBy: link.CreatedBy,
		CreatedAt: link.CreatedAt, ExpiresAt: link.ExpiresAt, RevokedAt: link.RevokedAt,
		PasswordProtected: link.PasswordHash != nil,
	}
}
