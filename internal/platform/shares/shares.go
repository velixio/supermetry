// Package shares manages read-only shared dashboard links.
package shares

import (
	"context"
	"crypto/hmac"
	"crypto/sha256"
	"encoding/base64"
	"errors"
	"strconv"
	"strings"
	"time"

	"github.com/jackc/pgx/v5"

	"supermetry/internal/platform/audit"
	"supermetry/internal/platform/authz"
	platformcrypto "supermetry/internal/platform/crypto"
	"supermetry/internal/platform/store"
)

const shareSessionLifetime = 12 * time.Hour

var (
	ErrForbidden        = errors.New("forbidden")
	ErrNotFound         = errors.New("shared link not found")
	ErrPasswordRequired = errors.New("share password required")
	ErrInvalid          = errors.New("invalid shared link or password")
)

type ShareInfo struct {
	ID                int64
	SiteID            int32
	CreatedBy         int64
	CreatedAt         time.Time
	RevokedAt         *time.Time
	PasswordProtected bool
}

type Shares struct {
	store *store.Store
	now   func() time.Time
}

func New(data *store.Store) *Shares {
	return &Shares{store: data, now: time.Now}
}

func (s *Shares) Create(ctx context.Context, principal authz.Principal, siteID int32, password string) (slug string, info ShareInfo, err error) {
	if !authz.Can(principal, authz.ActionSharesManage, siteID) || principal.UserID == 0 {
		return "", ShareInfo{}, ErrForbidden
	}

	slug, slugHash := platformcrypto.NewToken("shr_")
	var passwordHash *string
	if password != "" {
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
			CreatedBy: principal.UserID,
		})
		if createErr != nil {
			if errors.Is(createErr, pgx.ErrNoRows) {
				return ErrNotFound
			}
			return createErr
		}
		return audit.Write(ctx, tx, principal, "share.created", "shared_link", link.ID, map[string]any{
			"site_id": siteID, "password_protected": passwordHash != nil,
		})
	})
	if err != nil {
		return "", ShareInfo{}, err
	}
	return slug, shareInfo(link), nil
}

func (s *Shares) Open(ctx context.Context, slug, password string) (cookieValue string, err error) {
	link, err := s.lookup(ctx, slug)
	if err != nil || link.RevokedAt != nil {
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
	return signCookie(slug, s.now().Add(shareSessionLifetime), *link.PasswordHash), nil
}

func (s *Shares) Principal(ctx context.Context, slug, cookieValue string) (authz.Principal, bool) {
	link, err := s.lookup(ctx, slug)
	if err != nil || link.RevokedAt != nil {
		return authz.Principal{}, false
	}
	if link.PasswordHash != nil && !validCookie(slug, cookieValue, *link.PasswordHash, s.now()) {
		return authz.Principal{}, false
	}
	return authz.Principal{
		OrgID: link.OrgID, ShareID: link.ID, Role: authz.RoleShare,
		SiteIDs: []int32{link.SiteID},
	}, true
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

func signCookie(slug string, expires time.Time, passwordHash string) string {
	expiry := strconv.FormatInt(expires.Unix(), 10)
	mac := cookieMAC(slug, expiry, passwordHash)
	return expiry + "." + base64.RawURLEncoding.EncodeToString(mac)
}

func validCookie(slug, value, passwordHash string, now time.Time) bool {
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
	return hmac.Equal(got, cookieMAC(slug, expiry, passwordHash))
}

func cookieMAC(slug, expiry, passwordHash string) []byte {
	mac := hmac.New(sha256.New, []byte(passwordHash))
	_, _ = mac.Write([]byte(slug))
	_, _ = mac.Write([]byte{0})
	_, _ = mac.Write([]byte(expiry))
	return mac.Sum(nil)
}

func shareInfo(link store.SharedLink) ShareInfo {
	return ShareInfo{
		ID: link.ID, SiteID: link.SiteID, CreatedBy: link.CreatedBy,
		CreatedAt: link.CreatedAt, RevokedAt: link.RevokedAt,
		PasswordProtected: link.PasswordHash != nil,
	}
}
