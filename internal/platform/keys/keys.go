// Package keys creates, verifies, lists, and revokes API keys.
package keys

import (
	"context"
	"crypto/rand"
	"crypto/sha256"
	"crypto/subtle"
	"encoding/base32"
	"errors"
	"fmt"
	"strings"
	"sync"
	"time"

	"github.com/jackc/pgx/v5"

	"supermetry/internal/platform/audit"
	"supermetry/internal/platform/authz"
	"supermetry/internal/platform/store"
)

const (
	keyPrefixLength  = 8
	lastUseInterval  = time.Minute
	lastUseQueueSize = 256
)

var (
	ErrForbidden = errors.New("forbidden")
	ErrNotFound  = errors.New("API key not found")
	ErrInvalid   = errors.New("invalid API key scope or site restriction")

	base32NoPadding = base32.StdEncoding.WithPadding(base32.NoPadding)
)

type KeyInfo struct {
	ID         int64
	OrgID      int64
	Name       string
	Prefix     string
	Scopes     []string
	SiteIDs    []int32
	CreatedBy  int64
	CreatedAt  time.Time
	LastUsedAt *time.Time
	ExpiresAt  *time.Time
	RevokedAt  *time.Time
}

type Keys struct {
	store  *store.Store
	used   chan int64
	cancel context.CancelFunc
	done   chan struct{}
	once   sync.Once
}

func New(data *store.Store) *Keys {
	return newWithFlushInterval(data, lastUseInterval)
}

func newWithFlushInterval(data *store.Store, interval time.Duration) *Keys {
	ctx, cancel := context.WithCancel(context.Background())
	keys := &Keys{
		store: data, used: make(chan int64, lastUseQueueSize),
		cancel: cancel, done: make(chan struct{}),
	}
	go keys.flushLastUsed(ctx, interval)
	return keys
}

// Close stops the last-used worker after flushing queued key IDs.
func (k *Keys) Close() {
	k.once.Do(func() {
		k.cancel()
		<-k.done
	})
}

func (k *Keys) Create(ctx context.Context, principal authz.Principal, name string, scopes []string, siteIDs []int32, expires *time.Time) (plain string, info KeyInfo, err error) {
	if !authz.Can(principal, authz.ActionKeysManage, 0) || principal.Role != authz.RoleKey && principal.UserID == 0 {
		return "", KeyInfo{}, ErrForbidden
	}
	if !authz.ValidScopes(scopes) {
		return "", KeyInfo{}, ErrInvalid
	}
	prefix, secret, err := newKeyParts()
	if err != nil {
		return "", KeyInfo{}, err
	}
	plain = "pnl_" + prefix + "_" + secret
	hash := sha256.Sum256([]byte(plain))

	var stored store.APIKey
	err = k.store.Tx(ctx, func(tx pgx.Tx) error {
		createdBy := principal.UserID
		if principal.Role != authz.RoleKey {
			current, currentErr := store.CurrentMemberForUpdate(ctx, tx, principal.OrgID, principal.UserID)
			if errors.Is(currentErr, pgx.ErrNoRows) {
				return ErrForbidden
			}
			if currentErr != nil {
				return currentErr
			}
			if !authz.Can(current, authz.ActionKeysManage, 0) || !canIssue(current, scopes, siteIDs) {
				return ErrForbidden
			}
		}
		if len(siteIDs) > 0 {
			valid, validateErr := store.InvitationSitesBelongToOrg(ctx, tx, principal.OrgID, siteIDs)
			if validateErr != nil {
				return validateErr
			}
			if !valid {
				return ErrInvalid
			}
		}
		if principal.Role == authz.RoleKey {
			caller, callerErr := store.APIKeyForDelegation(ctx, tx, principal.OrgID, principal.KeyID)
			if callerErr != nil {
				if errors.Is(callerErr, pgx.ErrNoRows) {
					return ErrForbidden
				}
				return callerErr
			}
			if !canDelegate(caller, scopes, siteIDs, expires) {
				return ErrForbidden
			}
			createdBy = caller.CreatedBy
		}
		var createErr error
		stored, createErr = store.CreateAPIKey(ctx, tx, store.APIKey{
			OrgID: principal.OrgID, Name: name, Prefix: prefix, Hash: hash[:],
			Scopes: cloneStrings(scopes), SiteIDs: cloneSites(siteIDs),
			CreatedBy: createdBy, ExpiresAt: cloneTime(expires),
		})
		if createErr != nil {
			return createErr
		}
		return audit.Write(ctx, tx, principal, "api_key.created", "api_key", stored.ID, map[string]any{
			"name": name, "scopes": scopes, "site_ids": siteIDs, "expires_at": expires,
		})
	})
	if err != nil {
		return "", KeyInfo{}, err
	}
	return plain, keyInfo(stored), nil
}

func canDelegate(caller store.APIKey, scopes []string, siteIDs []int32, expires *time.Time) bool {
	if !contains(caller.Scopes, "keys:manage") || contains(scopes, "keys:manage") {
		return false
	}
	for _, scope := range scopes {
		if !contains(caller.Scopes, scope) {
			return false
		}
	}
	if caller.SiteIDs != nil {
		if siteIDs == nil {
			return false
		}
		for _, siteID := range siteIDs {
			if !containsSite(caller.SiteIDs, siteID) {
				return false
			}
		}
	}
	if caller.ExpiresAt != nil && (expires == nil || expires.After(*caller.ExpiresAt)) {
		return false
	}
	return true
}

func contains(values []string, want string) bool {
	for _, value := range values {
		if value == want {
			return true
		}
	}
	return false
}

func containsSite(values []int32, want int32) bool {
	for _, value := range values {
		if value == want {
			return true
		}
	}
	return false
}

func (k *Keys) Verify(ctx context.Context, plain string) (authz.Principal, bool) {
	prefix, ok := parseKeyPrefix(plain)
	if !ok {
		return authz.Principal{}, false
	}
	candidates, err := store.APIKeysByPrefix(ctx, k.store, prefix)
	if err != nil {
		return authz.Principal{}, false
	}
	hash := sha256.Sum256([]byte(plain))
	now := time.Now()
	for _, candidate := range candidates {
		if subtle.ConstantTimeCompare(candidate.Hash, hash[:]) != 1 {
			continue
		}
		// Do not let historical, unrecognized persisted scopes become wildcard
		// authority through the analytics principal adapter.
		if !authz.ValidScopes(candidate.Scopes) {
			return authz.Principal{}, false
		}
		if candidate.RevokedAt != nil || candidate.ExpiresAt != nil && !candidate.ExpiresAt.After(now) {
			return authz.Principal{}, false
		}
		select {
		case k.used <- candidate.ID:
		default:
		}
		return authz.Principal{
			OrgID: candidate.OrgID, KeyID: candidate.ID, Role: authz.RoleKey,
			SiteIDs: cloneSites(candidate.SiteIDs), Scopes: cloneStrings(candidate.Scopes),
		}, true
	}
	return authz.Principal{}, false
}

func (k *Keys) Revoke(ctx context.Context, principal authz.Principal, id int64) error {
	if !authz.Can(principal, authz.ActionKeysManage, 0) {
		return ErrForbidden
	}
	return k.store.Tx(ctx, func(tx pgx.Tx) error {
		if principal.Role == authz.RoleKey {
			caller, err := store.APIKeyForDelegation(ctx, tx, principal.OrgID, principal.KeyID)
			if errors.Is(err, pgx.ErrNoRows) {
				return ErrForbidden
			}
			if err != nil {
				return err
			}
			if !contains(caller.Scopes, "keys:manage") {
				return ErrForbidden
			}
			if id != caller.ID {
				target, err := store.APIKeyForDelegation(ctx, tx, principal.OrgID, id)
				if errors.Is(err, pgx.ErrNoRows) {
					return ErrNotFound
				}
				if err != nil {
					return err
				}
				if !keyWithinAuthority(caller, target) {
					return ErrNotFound
				}
			}
		} else {
			current, err := store.CurrentMemberForUpdate(ctx, tx, principal.OrgID, principal.UserID)
			if errors.Is(err, pgx.ErrNoRows) {
				return ErrForbidden
			}
			if err != nil {
				return err
			}
			if !authz.Can(current, authz.ActionKeysManage, 0) {
				return ErrForbidden
			}
		}
		if err := store.RevokeAPIKey(ctx, tx, principal.OrgID, id); err != nil {
			if errors.Is(err, pgx.ErrNoRows) {
				return ErrNotFound
			}
			return err
		}
		return audit.Write(ctx, tx, principal, "api_key.revoked", "api_key", id, nil)
	})
}

func (k *Keys) List(ctx context.Context, principal authz.Principal) ([]KeyInfo, error) {
	if !authz.Can(principal, authz.ActionKeysManage, 0) {
		return nil, ErrForbidden
	}
	stored, err := store.ListAPIKeys(ctx, k.store, principal.OrgID)
	if err != nil {
		return nil, err
	}
	infos := make([]KeyInfo, 0, len(stored))
	for _, key := range stored {
		if principal.Role == authz.RoleKey && key.ID != principal.KeyID && !keyWithinAuthority(store.APIKey{Scopes: principal.Scopes, SiteIDs: principal.SiteIDs}, key) {
			continue
		}
		infos = append(infos, keyInfo(key))
	}
	return infos, nil
}

func (k *Keys) flushLastUsed(ctx context.Context, interval time.Duration) {
	defer close(k.done)
	ticker := time.NewTicker(interval)
	defer ticker.Stop()
	pending := make(map[int64]struct{})
	flush := func(flushCtx context.Context) {
		ids := make([]int64, 0, len(pending))
		for id := range pending {
			ids = append(ids, id)
		}
		if err := store.TouchAPIKeys(flushCtx, k.store, ids); err == nil {
			clear(pending)
		}
	}
	for {
		select {
		case id := <-k.used:
			pending[id] = struct{}{}
		case <-ticker.C:
			flush(ctx)
		case <-ctx.Done():
			for {
				select {
				case id := <-k.used:
					pending[id] = struct{}{}
				default:
					flushCtx, cancel := context.WithTimeout(context.Background(), 5*time.Second)
					flush(flushCtx)
					cancel()
					return
				}
			}
		}
	}
}

func newKeyParts() (string, string, error) {
	prefixBytes := make([]byte, 5)
	secretBytes := make([]byte, 32)
	if _, err := rand.Read(prefixBytes); err != nil {
		return "", "", fmt.Errorf("generate API key prefix: %w", err)
	}
	if _, err := rand.Read(secretBytes); err != nil {
		return "", "", fmt.Errorf("generate API key secret: %w", err)
	}
	prefix := strings.ToLower(base32NoPadding.EncodeToString(prefixBytes))
	secret := base32NoPadding.EncodeToString(secretBytes)
	return prefix, secret, nil
}

func parseKeyPrefix(plain string) (string, bool) {
	if len(plain) < len("pnl_")+keyPrefixLength+1+40 || !strings.HasPrefix(plain, "pnl_") {
		return "", false
	}
	if plain[len("pnl_")+keyPrefixLength] != '_' {
		return "", false
	}
	prefix := plain[len("pnl_") : len("pnl_")+keyPrefixLength]
	for _, char := range prefix {
		if char < 'a' || char > 'z' {
			if char < '2' || char > '7' {
				return "", false
			}
		}
	}
	for _, char := range plain[len("pnl_")+keyPrefixLength+1:] {
		if char < 'A' || char > 'Z' {
			if char < 'a' || char > 'z' {
				if char < '0' || char > '9' {
					return "", false
				}
			}
		}
	}
	return prefix, true
}

func keyInfo(key store.APIKey) KeyInfo {
	return KeyInfo{
		ID: key.ID, OrgID: key.OrgID, Name: key.Name, Prefix: key.Prefix,
		Scopes: cloneStrings(key.Scopes), SiteIDs: cloneSites(key.SiteIDs),
		CreatedBy: key.CreatedBy, CreatedAt: key.CreatedAt,
		LastUsedAt: cloneTime(key.LastUsedAt), ExpiresAt: cloneTime(key.ExpiresAt),
		RevokedAt: cloneTime(key.RevokedAt),
	}
}

func cloneStrings(values []string) []string {
	if values == nil {
		return nil
	}
	return append([]string(nil), values...)
}

func cloneSites(values []int32) []int32 {
	if values == nil {
		return nil
	}
	out := make([]int32, len(values)) // keeps an empty restriction empty (no sites), unlike append on nil
	copy(out, values)
	return out
}

func cloneTime(value *time.Time) *time.Time {
	if value == nil {
		return nil
	}
	cloned := *value
	return &cloned
}

func canIssue(issuer authz.Principal, scopes []string, siteIDs []int32) bool {
	allowed := authz.APIScopes(issuer)
	for _, scope := range scopes {
		if !contains(allowed, scope) {
			return false
		}
	}
	if issuer.SiteIDs != nil && issuer.Role != authz.RoleOwner && issuer.Role != authz.RoleAdmin {
		if siteIDs == nil {
			return false
		}
		for _, siteID := range siteIDs {
			if !containsSite(issuer.SiteIDs, siteID) {
				return false
			}
		}
	}
	return true
}

func keyWithinAuthority(caller, target store.APIKey) bool {
	for _, scope := range target.Scopes {
		if !contains(caller.Scopes, scope) {
			return false
		}
	}
	if caller.SiteIDs != nil {
		if target.SiteIDs == nil {
			return false
		}
		for _, id := range target.SiteIDs {
			if !containsSite(caller.SiteIDs, id) {
				return false
			}
		}
	}
	return true
}
