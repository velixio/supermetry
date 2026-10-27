package platformhttp

import (
	"context"
	"errors"
	"fmt"
	"net/http"
	"strings"
	"time"

	"github.com/jackc/pgx/v5"

	platformauth "supermetry/internal/platform/auth"
	"supermetry/internal/platform/sitesvc"
	"supermetry/internal/platform/store"
)

// userDeleteRefused is a 409 with a human-readable reason.
type userDeleteRefused struct{ detail string }

func (e userDeleteRefused) Error() string { return e.detail }

// OwnedOrg identifies an organization in a last_owner refusal.
type OwnedOrg struct {
	ID   int64  `json:"id"`
	Slug string `json:"slug"`
}

// lastOwnerError means the user is the only owner of live organizations and
// erase_owned_orgs was false.
type lastOwnerError struct{ orgs []OwnedOrg }

func (e lastOwnerError) Error() string { return "user is the only owner of live organizations" }

var errEmailMismatch = errors.New("email does not match the user")

// adminUserDeleter is implemented by Services; custom MutationService
// implementations opt in by providing it.
type adminUserDeleter interface {
	AdminDeleteUser(ctx context.Context, actorUserID, userID int64, email string, eraseOwnedOrgs bool) (int, error)
}

// AdminDeleteUser deletes a user account on behalf of an instance admin
// without a password. The email must equal the user's email (compared
// case-insensitively). It refuses system users, instance admins, the caller
// and the last enabled admin. Organizations the user solely owns are erased
// at once (returned count, handled by the erasure job) or, when
// eraseOwnedOrgs is false, make a live one a lastOwnerError. All checks run
// before any change, in one transaction. It returns store.ErrNotFound for an
// unknown or deleted user.
func (s Services) AdminDeleteUser(ctx context.Context, actorUserID, userID int64, email string, eraseOwnedOrgs bool) (int, error) {
	if s.Store == nil {
		return 0, errors.New("user service is unavailable")
	}
	tombstone, err := platformauth.TombstonePasswordHash()
	if err != nil {
		return 0, err
	}
	erasing := 0
	err = s.Store.Tx(ctx, func(tx pgx.Tx) error {
		erasing = 0
		if allowed, err := store.InstanceAdminActive(ctx, tx, actorUserID); err != nil {
			return err
		} else if !allowed {
			return sitesvc.ErrForbidden
		}
		var userEmail string
		var isAdmin, isSystem bool
		err := tx.QueryRow(ctx, `select email,is_admin,is_system from users where id=$1 and deleted_at is null for update`, userID).Scan(&userEmail, &isAdmin, &isSystem)
		if errors.Is(err, pgx.ErrNoRows) {
			return store.ErrNotFound
		}
		if err != nil {
			return fmt.Errorf("lock user for deletion: %w", err)
		}
		if !strings.EqualFold(strings.TrimSpace(email), userEmail) {
			return errEmailMismatch
		}
		switch {
		case userID == actorUserID:
			return userDeleteRefused{"you cannot delete your own account here"}
		case isSystem:
			return userDeleteRefused{"system users cannot be deleted"}
		case isAdmin:
			var others int
			if err := tx.QueryRow(ctx, `select count(*) from users where is_admin and disabled_at is null and deleted_at is null and not is_system and id<>$1`, userID).Scan(&others); err != nil {
				return fmt.Errorf("count other administrators: %w", err)
			}
			if others == 0 {
				return userDeleteRefused{"this is the last enabled instance administrator"}
			}
			return userDeleteRefused{"instance administrators must be demoted before their account can be deleted"}
		}
		rows, err := tx.Query(ctx, `
			select o.id,o.slug,o.deleted_at is null
			from memberships m join orgs o on o.id=m.org_id
			where m.user_id=$1 and m.role='owner' and o.erased_at is null
			  and not exists(select 1 from memberships x where x.org_id=o.id and x.role='owner' and x.user_id<>$1)
			order by o.id`, userID)
		if err != nil {
			return fmt.Errorf("find sole-owned organizations: %w", err)
		}
		type owned struct {
			OwnedOrg
			live bool
		}
		var sole []owned
		for rows.Next() {
			var o owned
			if err := rows.Scan(&o.ID, &o.Slug, &o.live); err != nil {
				rows.Close()
				return err
			}
			sole = append(sole, o)
		}
		rows.Close()
		if err := rows.Err(); err != nil {
			return err
		}
		if !eraseOwnedOrgs {
			var live []OwnedOrg
			for _, o := range sole {
				if o.live {
					live = append(live, o.OwnedOrg)
				}
			}
			if len(live) > 0 {
				return lastOwnerError{live}
			}
		} else {
			for _, o := range sole {
				status, err := eraseOrgNowTx(ctx, tx, actorUserID, o.ID, nil)
				if err != nil {
					return err
				}
				if status == OrgEraseStatusErasing {
					erasing++
				}
			}
		}
		actor := actorUserID
		if err := platformauth.TombstoneUserTx(ctx, tx, userID, &actor, time.Now(), tombstone); err != nil {
			return err
		}
		return store.WriteInstanceAudit(ctx, tx, store.InstanceAuditEntry{
			ActorUserID: actorUserID, Action: "instance.user.deleted", TargetType: "user", TargetID: userID,
			Metadata: map[string]any{"erased_owned_orgs": eraseOwnedOrgs, "orgs_erased": erasing},
		})
	})
	if err != nil {
		return 0, err
	}
	if erasing > 0 && s.Sites != nil {
		if err := s.Sites.Invalidate(); err != nil {
			return erasing, fmt.Errorf("refresh site registry: %w", err)
		}
	}
	return erasing, nil
}

func (h *handler) adminDeleteUser(w http.ResponseWriter, r *http.Request) {
	principal, ok := h.adminPrincipal(w, r)
	if !ok {
		return
	}
	userID, ok := adminID(r, "user")
	if !ok {
		writeProblem(w, http.StatusBadRequest, "Bad request", "user is invalid")
		return
	}
	var request struct {
		Email          string `json:"email"`
		EraseOwnedOrgs *bool  `json:"erase_owned_orgs"`
	}
	if !decodeJSON(w, r, &request) {
		return
	}
	if strings.TrimSpace(request.Email) == "" {
		writeProblem(w, http.StatusBadRequest, "Bad request", "email is required and must equal the user's email")
		return
	}
	deleter, ok := h.deps.Mutations.(adminUserDeleter)
	if !ok {
		unavailable(w, "user")
		return
	}
	erase := request.EraseOwnedOrgs == nil || *request.EraseOwnedOrgs
	count, err := deleter.AdminDeleteUser(r.Context(), principal.UserID, userID, request.Email, erase)
	var refused userDeleteRefused
	var lastOwner lastOwnerError
	switch {
	case errors.Is(err, store.ErrNotFound):
		writeProblem(w, http.StatusNotFound, "Not found", "resource not found")
	case errors.Is(err, errEmailMismatch):
		writeProblem(w, http.StatusBadRequest, "Bad request", err.Error())
	case errors.As(err, &refused):
		writeProblem(w, http.StatusConflict, "Conflict", refused.detail)
	case errors.As(err, &lastOwner):
		writeJSON(w, http.StatusConflict, map[string]any{"error": "last_owner", "orgs": lastOwner.orgs})
	case errors.Is(err, errEraseLastOrg):
		writeProblem(w, http.StatusConflict, "Conflict", "an organization the user solely owns is the only live organization of the instance; it cannot be erased")
	case err != nil:
		serviceError(w, err)
	default:
		writeJSON(w, http.StatusOK, map[string]any{"status": "deleted", "orgs_erasing": count})
	}
}
