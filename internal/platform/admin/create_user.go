package admin

import (
	"context"
	"crypto/rand"
	"encoding/base64"
	"errors"
	"fmt"
	"log/slog"
	"net/mail"
	"strings"
	"time"

	"github.com/jackc/pgx/v5"
	"github.com/jackc/pgx/v5/pgconn"

	"supermetry/internal/platform/store"
	"supermetry/internal/platform/users"
)

// ErrEmailTaken is returned when the email (compared case-insensitively) already
// belongs to an account, including the hidden admin API key system user.
var ErrEmailTaken = errors.New("email is already registered")

const (
	maxEmailLength   = 254
	maxNameLength    = 200
	maxOrgNameLength = 100
)

// CreateUserInput describes a user an instance admin creates.
type CreateUserInput struct {
	Email, Name, OrgName string
	SendReset            bool
}

// ResetStarter emails a password-set link to the address, or returns the link
// when no mail transport is configured.
type ResetStarter func(ctx context.Context, email string) (string, error)

// SetResetStarter wires the password-reset sender used by CreateUser. Call it
// during startup, before serving requests.
func (s *Service) SetResetStarter(start ResetStarter) { s.resetStarter = start }

// CreateUser creates a user with a random, unusable password, optionally with
// an organization they own, and audits it in the same transaction. With
// SendReset it then emails a password-set link; creation does not depend on that
// succeeding (the password stays unusable until a reset is completed).
func (s *Service) CreateUser(ctx context.Context, actorUserID int64, in CreateUserInput) (User, error) {
	email := users.NormalizeEmail(in.Email)
	address, err := mail.ParseAddress(email)
	if err != nil || address.Address != email || len(email) > maxEmailLength || !strings.Contains(email, "@") {
		return User{}, ErrInvalid
	}
	name := strings.TrimSpace(in.Name)
	if name == "" {
		name = strings.TrimSpace(strings.SplitN(email, "@", 2)[0])
	}
	orgName := strings.TrimSpace(in.OrgName)
	if name == "" || len(name) > maxNameLength || len(orgName) > maxOrgNameLength {
		return User{}, ErrInvalid
	}
	password, err := randomPassword()
	if err != nil {
		return User{}, err
	}
	var created User
	err = s.store.Tx(ctx, func(tx pgx.Tx) error {
		if err := requireAdmin(ctx, tx, actorUserID); err != nil {
			return err
		}
		userID, err := users.New(s.store).CreateWithDB(ctx, tx, email, name, password, time.Now().UTC())
		if err != nil {
			var pgErr *pgconn.PgError
			if errors.As(err, &pgErr) && pgErr.Code == "23505" && strings.HasPrefix(pgErr.ConstraintName, "users_email") {
				return ErrEmailTaken
			}
			return err
		}
		var orgID int64
		if orgName != "" {
			if orgID, err = store.CreateOrg(ctx, tx, orgName, userID); err != nil {
				return err
			}
		}
		if err := store.WriteInstanceAudit(ctx, tx, store.InstanceAuditEntry{
			ActorUserID: actorUserID, Action: "instance.user.created", TargetType: "user", TargetID: userID,
			Metadata: map[string]any{"org_created": orgID != 0, "send_reset": in.SendReset},
		}); err != nil {
			return err
		}
		items, err := s.listUsers(ctx, tx, "", userID, 1)
		if err != nil {
			return err
		}
		if len(items) != 1 {
			return ErrNotFound
		}
		created = items[0]
		return nil
	})
	if err != nil {
		return User{}, err
	}
	if in.SendReset && s.resetStarter != nil {
		// The account exists either way; an unsent link must not turn a
		// successful creation into an error the caller would retry into a 409.
		if _, err := s.resetStarter(ctx, email); err != nil {
			slog.Warn("admin-created user: password reset not sent", "user_id", created.ID, "error", err)
		}
	}
	return created, nil
}

// randomPassword returns 32 bytes from crypto/rand, base64url encoded. It is
// hashed immediately and never returned or logged.
func randomPassword() (string, error) {
	buf := make([]byte, 32)
	if _, err := rand.Read(buf); err != nil {
		return "", fmt.Errorf("generate password: %w", err)
	}
	return base64.RawURLEncoding.EncodeToString(buf), nil
}
