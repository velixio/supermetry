package platformhttp

import (
	"context"
	"errors"
	"fmt"
	"net/http"
	"time"

	"github.com/jackc/pgx/v5"

	"supermetry/internal/platform/sitesvc"
	"supermetry/internal/platform/store"
)

var (
	// errEraseSlugMismatch means the slug in the request is not the organization's.
	errEraseSlugMismatch = errors.New("slug does not match the organization")
	// errEraseLastOrg means the organization is the only live one of the instance.
	errEraseLastOrg = errors.New("cannot erase the only organization of the instance")
)

// Statuses reported by an immediate organization erasure.
const (
	OrgEraseStatusErasing = "erasing"
	OrgEraseStatusErased  = "erased"
)

// adminOrgEraser is implemented by Services; custom MutationService
// implementations opt in by providing it.
type adminOrgEraser interface {
	AdminEraseOrg(ctx context.Context, actorUserID int64, orgID int64, slug string) (string, error)
}

// AdminEraseOrg deletes the organization when it is still live, then makes it
// and all its sites due for erasure at once, skipping the restore grace period.
// The site-erasure job does the work; it returns OrgEraseStatusErasing while
// that is pending and OrgEraseStatusErased once the job has finished. It
// returns store.ErrNotFound for an unknown organization.
func (s Services) AdminEraseOrg(ctx context.Context, actorUserID int64, orgID int64, slug string) (string, error) {
	if s.Store == nil {
		return "", errors.New("organization service is unavailable")
	}
	status := OrgEraseStatusErasing
	err := s.Store.Tx(ctx, func(tx pgx.Tx) error {
		if allowed, err := store.InstanceAdminActive(ctx, tx, actorUserID); err != nil {
			return err
		} else if !allowed {
			return sitesvc.ErrForbidden
		}
		var orgSlug string
		var deletedAt, erasedAt, requestedAt *time.Time
		err := tx.QueryRow(ctx, `select slug,deleted_at,erased_at,erase_requested_at from orgs where id=$1 for update`, orgID).
			Scan(&orgSlug, &deletedAt, &erasedAt, &requestedAt)
		if errors.Is(err, pgx.ErrNoRows) {
			return store.ErrNotFound
		}
		if err != nil {
			return fmt.Errorf("lock organization for erasure: %w", err)
		}
		if orgSlug != slug {
			return errEraseSlugMismatch
		}
		if erasedAt != nil {
			status = OrgEraseStatusErased
			return nil
		}
		if requestedAt != nil {
			return nil
		}
		now := time.Now()
		if deletedAt == nil {
			var others int
			if err := tx.QueryRow(ctx, `select count(*) from orgs where id<>$1 and deleted_at is null`, orgID).Scan(&others); err != nil {
				return fmt.Errorf("count other organizations: %w", err)
			}
			if others == 0 {
				return errEraseLastOrg
			}
			if _, _, err := deleteOrgTx(ctx, tx, orgID, now, "instance_admin", actorUserID); err != nil {
				return err
			}
		}
		if _, err := tx.Exec(ctx, `update orgs set erase_requested_at=$2 where id=$1`, orgID, now); err != nil {
			return fmt.Errorf("request organization erasure: %w", err)
		}
		tag, err := tx.Exec(ctx, `
			update sites
			set deleted_at=coalesce(deleted_at,$2),erasure_started_at=coalesce(erasure_started_at,$2)
			where org_id=$1 and erased_at is null`, orgID, now)
		if err != nil {
			return fmt.Errorf("queue organization sites for erasure: %w", err)
		}
		return store.WriteInstanceAudit(ctx, tx, store.InstanceAuditEntry{
			ActorUserID: actorUserID, Action: "instance.organization.erase_now", TargetType: "organization", TargetID: orgID,
			Metadata: map[string]any{"slug": orgSlug, "was_deleted": deletedAt != nil, "sites_queued": tag.RowsAffected()},
		})
	})
	if err != nil {
		return "", err
	}
	if s.Sites != nil {
		if err := s.Sites.Invalidate(); err != nil {
			return "", fmt.Errorf("refresh site registry: %w", err)
		}
	}
	return status, nil
}

func (h *handler) adminEraseOrganization(w http.ResponseWriter, r *http.Request) {
	principal, ok := h.adminPrincipal(w, r)
	if !ok {
		return
	}
	orgID, ok := adminID(r, "org")
	if !ok {
		writeProblem(w, http.StatusBadRequest, "Bad request", "organization is invalid")
		return
	}
	var request struct {
		Slug string `json:"slug"`
	}
	if !decodeJSON(w, r, &request) {
		return
	}
	if request.Slug == "" {
		writeProblem(w, http.StatusBadRequest, "Bad request", "slug is required and must equal the organization's slug")
		return
	}
	eraser, ok := h.deps.Mutations.(adminOrgEraser)
	if !ok {
		unavailable(w, "organization")
		return
	}
	status, err := eraser.AdminEraseOrg(r.Context(), principal.UserID, orgID, request.Slug)
	switch {
	case errors.Is(err, store.ErrNotFound):
		writeProblem(w, http.StatusNotFound, "Not found", "resource not found")
	case errors.Is(err, errEraseSlugMismatch):
		writeProblem(w, http.StatusBadRequest, "Bad request", err.Error())
	case errors.Is(err, errEraseLastOrg):
		writeProblem(w, http.StatusConflict, "Conflict", err.Error())
	case err != nil:
		serviceError(w, err)
	case status == OrgEraseStatusErased:
		writeJSON(w, http.StatusOK, map[string]string{"status": status})
	default:
		writeJSON(w, http.StatusAccepted, map[string]string{"status": status})
	}
}
