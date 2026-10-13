package admin

import (
	"context"
	"errors"
	"strings"
	"time"

	"github.com/jackc/pgx/v5"
	"github.com/jackc/pgx/v5/pgconn"
	"golang.org/x/mod/semver"

	"supermetry/internal/platform/store"
)

var ErrUpdateActive = errors.New("an update is already active")

type UpdateRequest struct {
	IdempotencyKey string
	FromVersion    string
	TargetVersion  string
	DeploymentMode string
}

type UpdateJob struct {
	ID             int64      `json:"id"`
	FromVersion    string     `json:"from_version"`
	TargetVersion  string     `json:"target_version"`
	DeploymentMode string     `json:"deployment_mode"`
	Status         string     `json:"status"`
	CreatedAt      time.Time  `json:"created_at"`
	UpdatedAt      time.Time  `json:"updated_at"`
	FinishedAt     *time.Time `json:"finished_at"`
}

func (s *Service) RequestUpdate(ctx context.Context, actorID int64, request UpdateRequest) (UpdateJob, error) {
	request.IdempotencyKey = strings.TrimSpace(request.IdempotencyKey)
	from := normalizeStableVersion(request.FromVersion)
	target := normalizeStableVersion(request.TargetVersion)
	if len(request.IdempotencyKey) < 8 || len(request.IdempotencyKey) > 128 || from == "" || target == "" || semver.Compare(target, from) <= 0 || request.DeploymentMode != "binary" && request.DeploymentMode != "docker" {
		return UpdateJob{}, ErrInvalid
	}
	request.FromVersion, request.TargetVersion = from, target

	var job UpdateJob
	err := s.store.Tx(ctx, func(tx pgx.Tx) error {
		if err := requireAdmin(ctx, tx, actorID); err != nil {
			return err
		}
		err := tx.QueryRow(ctx, `
			insert into instance_update_jobs(
				requested_by,idempotency_key,from_version,target_version,deployment_mode,status
			) values ($1,$2,$3,$4,$5,'requested')
			on conflict(idempotency_key) do nothing
			returning id,from_version,target_version,deployment_mode,status,created_at,updated_at,finished_at`,
			actorID, request.IdempotencyKey, from, target, request.DeploymentMode).Scan(
			&job.ID, &job.FromVersion, &job.TargetVersion, &job.DeploymentMode,
			&job.Status, &job.CreatedAt, &job.UpdatedAt, &job.FinishedAt,
		)
		if errors.Is(err, pgx.ErrNoRows) {
			var requestedBy int64
			if err := tx.QueryRow(ctx, `
				select id,requested_by,from_version,target_version,deployment_mode,status,created_at,updated_at,finished_at
				from instance_update_jobs where idempotency_key=$1`, request.IdempotencyKey).Scan(
				&job.ID, &requestedBy, &job.FromVersion, &job.TargetVersion,
				&job.DeploymentMode, &job.Status, &job.CreatedAt, &job.UpdatedAt, &job.FinishedAt,
			); err != nil {
				return err
			}
			if requestedBy != actorID || job.FromVersion != from || job.TargetVersion != target || job.DeploymentMode != request.DeploymentMode {
				return ErrInvalid
			}
			return nil
		}
		if err != nil {
			var pgErr *pgconn.PgError
			if errors.As(err, &pgErr) && pgErr.Code == "23505" {
				return ErrUpdateActive
			}
			return err
		}
		return store.WriteInstanceAudit(ctx, tx, store.InstanceAuditEntry{
			ActorUserID: actorID,
			Action:      "instance.update.requested",
			TargetType:  "update_job",
			TargetID:    job.ID,
			Metadata: map[string]any{
				"from_version": from, "target_version": target, "deployment_mode": request.DeploymentMode,
			},
		})
	})
	return job, err
}

func normalizeStableVersion(version string) string {
	version = strings.TrimSpace(version)
	if !strings.HasPrefix(version, "v") {
		version = "v" + version
	}
	if !semver.IsValid(version) || semver.Prerelease(version) != "" {
		return ""
	}
	return semver.Canonical(version)
}
