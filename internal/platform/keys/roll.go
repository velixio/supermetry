package keys

import (
	"context"
	"crypto/sha256"
	"errors"
	"strconv"
	"time"

	"github.com/jackc/pgx/v5"

	"supermetry/internal/platform/audit"
	"supermetry/internal/platform/authz"
	"supermetry/internal/platform/store"
)

// ErrInvalidGrace means a roll asked for a grace window that is not offered.
var ErrInvalidGrace = errors.New("grace_hours must be 0, 1, 24 or 168")

// GraceChoices are the overlap windows a roll may choose, in hours.
var GraceChoices = []int{0, 1, 24, 168}

// DefaultGraceHours is the overlap when a roll does not choose one.
const DefaultGraceHours = 24

// Roll replaces a key with a successor that has the same name, scopes, site
// restriction and expiry policy, and lets the old key keep working for the
// grace window. The successor's secret is returned once. The caller needs the
// same authority as for creating that key: a person must be allowed to issue
// the scopes and sites, a key must hold every one of them and may not roll
// itself or another keys:manage key (keys:manage cannot be delegated).
func (k *Keys) Roll(ctx context.Context, principal authz.Principal, id int64, graceHours int) (plain string, info KeyInfo, err error) {
	if !contains(intsToStrings(GraceChoices), itoa(graceHours)) {
		return "", KeyInfo{}, ErrInvalidGrace
	}
	if !authz.Can(principal, authz.ActionKeysManage, 0) || principal.Role != authz.RoleKey && principal.UserID == 0 {
		return "", KeyInfo{}, ErrForbidden
	}
	var oldKey store.APIKey
	err = k.store.Tx(ctx, func(tx pgx.Tx) error {
		var lockErr error
		oldKey, lockErr = store.APIKeyForRoll(ctx, tx, principal.OrgID, id)
		if errors.Is(lockErr, pgx.ErrNoRows) {
			return ErrNotFound
		}
		if lockErr != nil {
			return lockErr
		}
		now := time.Now()
		createdBy := principal.UserID
		if principal.Role == authz.RoleKey {
			caller, callerErr := store.APIKeyForDelegation(ctx, tx, principal.OrgID, principal.KeyID)
			if errors.Is(callerErr, pgx.ErrNoRows) {
				return ErrForbidden
			}
			if callerErr != nil {
				return callerErr
			}
			if !keyWithinAuthority(caller, oldKey) {
				return ErrNotFound
			}
			createdBy = caller.CreatedBy
		} else {
			current, currentErr := store.CurrentMemberForUpdate(ctx, tx, principal.OrgID, principal.UserID)
			if errors.Is(currentErr, pgx.ErrNoRows) {
				return ErrForbidden
			}
			if currentErr != nil {
				return currentErr
			}
			if !authz.Can(current, authz.ActionKeysManage, 0) || !canIssue(current, oldKey.Scopes, oldKey.SiteIDs) {
				return ErrForbidden
			}
		}
		var successorExpiry *time.Time
		if oldKey.ExpiresAt != nil {
			lifetime := oldKey.ExpiresAt.Sub(oldKey.CreatedAt)
			if lifetime < 24*time.Hour {
				lifetime = 24 * time.Hour
			}
			expiry := now.Add(lifetime)
			successorExpiry = &expiry
		}
		if principal.Role == authz.RoleKey {
			caller, _ := store.APIKeyForDelegation(ctx, tx, principal.OrgID, principal.KeyID)
			if !contains(caller.Scopes, "keys:manage") || contains(oldKey.Scopes, "keys:manage") {
				return ErrForbidden
			}
			if caller.ExpiresAt != nil && (successorExpiry == nil || successorExpiry.After(*caller.ExpiresAt)) {
				clamped := *caller.ExpiresAt
				successorExpiry = &clamped
			}
		}
		var prefix string
		plain, prefix, err = newPlainKey(oldKey.Scopes)
		if err != nil {
			return err
		}
		hash := sha256Sum(plain)
		stored, createErr := store.CreateAPIKey(ctx, tx, store.APIKey{
			OrgID: principal.OrgID, Name: oldKey.Name, Prefix: prefix, Hash: hash[:],
			Scopes: cloneStrings(oldKey.Scopes), SiteIDs: cloneSites(oldKey.SiteIDs),
			CreatedBy: createdBy, ExpiresAt: successorExpiry, RolledFromID: &oldKey.ID,
		})
		if createErr != nil {
			return createErr
		}
		info = keyInfo(stored)
		graceEnd := now.Add(time.Duration(graceHours) * time.Hour)
		if oldKey.ExpiresAt == nil || graceEnd.Before(*oldKey.ExpiresAt) {
			if err := store.SetAPIKeyExpiry(ctx, tx, principal.OrgID, oldKey.ID, graceEnd); err != nil {
				return err
			}
		} else {
			graceEnd = *oldKey.ExpiresAt
		}
		return audit.Write(ctx, tx, principal, "api_key.rolled", "api_key", oldKey.ID, map[string]any{
			"name": oldKey.Name, "successor_id": stored.ID, "grace_hours": graceHours, "old_key_expires_at": graceEnd,
		})
	})
	if err != nil {
		return "", KeyInfo{}, err
	}
	return plain, info, nil
}

func sha256Sum(plain string) [32]byte { return sha256.Sum256([]byte(plain)) }
func itoa(n int) string               { return strconv.Itoa(n) }
func intsToStrings(values []int) []string {
	out := make([]string, len(values))
	for i, v := range values {
		out[i] = strconv.Itoa(v)
	}
	return out
}
