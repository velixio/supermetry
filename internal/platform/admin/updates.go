package admin

import (
	"context"
	"errors"
	"regexp"
	"strings"
	"time"

	"github.com/jackc/pgx/v5"
	"github.com/jackc/pgx/v5/pgconn"
	"golang.org/x/mod/semver"

	"supermetry/internal/platform/store"
	"supermetry/internal/updatehelper"
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
	ErrorCode      string     `json:"error_code,omitempty"`
	CreatedAt      time.Time  `json:"created_at"`
	UpdatedAt      time.Time  `json:"updated_at"`
	FinishedAt     *time.Time `json:"finished_at"`
}

func (s *Service) RequestUpdate(ctx context.Context, actorID int64, request UpdateRequest) (UpdateJob, bool, error) {
	request.IdempotencyKey = strings.TrimSpace(request.IdempotencyKey)
	from := normalizeStableVersion(request.FromVersion)
	target := normalizeStableVersion(request.TargetVersion)
	if len(request.IdempotencyKey) < 8 || len(request.IdempotencyKey) > 128 || from == "" || target == "" || semver.Compare(target, from) <= 0 || request.DeploymentMode != "binary" && request.DeploymentMode != "docker" {
		return UpdateJob{}, false, ErrInvalid
	}
	request.FromVersion, request.TargetVersion = from, target

	var job UpdateJob
	created := false
	err := s.store.Tx(ctx, func(tx pgx.Tx) error {
		if err := requireAdmin(ctx, tx, actorID); err != nil {
			return err
		}
		err := tx.QueryRow(ctx, `
			insert into instance_update_jobs(
				requested_by,idempotency_key,from_version,target_version,deployment_mode,status
			) values ($1,$2,$3,$4,$5,'requested')
			on conflict(idempotency_key) do nothing
			returning id,from_version,target_version,deployment_mode,status,coalesce(error_code,''),created_at,updated_at,finished_at`,
			actorID, request.IdempotencyKey, from, target, request.DeploymentMode).Scan(
			&job.ID, &job.FromVersion, &job.TargetVersion, &job.DeploymentMode,
			&job.Status, &job.ErrorCode, &job.CreatedAt, &job.UpdatedAt, &job.FinishedAt,
		)
		if errors.Is(err, pgx.ErrNoRows) {
			var requestedBy int64
			if err := tx.QueryRow(ctx, `
				select id,requested_by,from_version,target_version,deployment_mode,status,coalesce(error_code,''),created_at,updated_at,finished_at
				from instance_update_jobs where idempotency_key=$1`, request.IdempotencyKey).Scan(
				&job.ID, &requestedBy, &job.FromVersion, &job.TargetVersion,
				&job.DeploymentMode, &job.Status, &job.ErrorCode, &job.CreatedAt, &job.UpdatedAt, &job.FinishedAt,
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
		created = true
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
	return job, created, err
}

func (s *Service) LatestUpdateJob(ctx context.Context) (*UpdateJob, error) {
	var job UpdateJob
	err := s.store.Pool.QueryRow(ctx, `
		select id,from_version,target_version,deployment_mode,status,coalesce(error_code,''),created_at,updated_at,finished_at
		from instance_update_jobs order by id desc limit 1`).Scan(
		&job.ID, &job.FromVersion, &job.TargetVersion, &job.DeploymentMode,
		&job.Status, &job.ErrorCode, &job.CreatedAt, &job.UpdatedAt, &job.FinishedAt,
	)
	if errors.Is(err, pgx.ErrNoRows) {
		return nil, nil
	}
	return &job, err
}

// RecentUpdateJobs lists the newest update jobs first, for the update history.
func (s *Service) RecentUpdateJobs(ctx context.Context, limit int) ([]UpdateJob, error) {
	rows, err := s.store.Pool.Query(ctx, `
		select id,from_version,target_version,deployment_mode,status,coalesce(error_code,''),created_at,updated_at,finished_at
		from instance_update_jobs order by id desc limit $1`, limit)
	if err != nil {
		return nil, err
	}
	defer rows.Close()
	jobs := []UpdateJob{}
	for rows.Next() {
		var job UpdateJob
		if err := rows.Scan(&job.ID, &job.FromVersion, &job.TargetVersion, &job.DeploymentMode,
			&job.Status, &job.ErrorCode, &job.CreatedAt, &job.UpdatedAt, &job.FinishedAt); err != nil {
			return nil, err
		}
		jobs = append(jobs, job)
	}
	return jobs, rows.Err()
}

func (s *Service) FailUndispatchedUpdate(ctx context.Context, actorID, jobID int64, code string) error {
	if !errorCodePattern.MatchString(code) {
		return ErrInvalid
	}
	return s.store.Tx(ctx, func(tx pgx.Tx) error {
		if err := requireAdmin(ctx, tx, actorID); err != nil {
			return err
		}
		tag, err := tx.Exec(ctx, `
			update instance_update_jobs
			set status='failed',error_code=$3,error_message='The updater helper was unavailable before dispatch.',updated_at=now(),finished_at=now()
			where id=$1 and requested_by=$2 and status='requested'`, jobID, actorID, code)
		if err != nil {
			return err
		}
		if tag.RowsAffected() != 1 {
			return ErrInvalid
		}
		return store.WriteInstanceAudit(ctx, tx, store.InstanceAuditEntry{
			ActorUserID: actorID, Action: "instance.update.failed", TargetType: "update_job", TargetID: jobID,
			Metadata: map[string]any{"failure_class": code},
		})
	})
}

func (s *Service) SyncUpdateJob(ctx context.Context, actorID, jobID int64, state updatehelper.State, code string) error {
	if !state.Valid() || code != "" && !errorCodePattern.MatchString(code) {
		return ErrInvalid
	}
	return s.store.Tx(ctx, func(tx pgx.Tx) error {
		if err := requireAdmin(ctx, tx, actorID); err != nil {
			return err
		}
		var current string
		if err := tx.QueryRow(ctx, `select status from instance_update_jobs where id=$1 for update`, jobID).Scan(&current); err != nil {
			if errors.Is(err, pgx.ErrNoRows) {
				return ErrNotFound
			}
			return err
		}
		if current == string(state) {
			return nil
		}
		currentRank, currentOK := updateStateRank[current]
		nextRank, nextOK := updateStateRank[string(state)]
		if !currentOK || !nextOK || terminalUpdateState[current] || !terminalUpdateState[string(state)] && nextRank < currentRank {
			return ErrInvalid
		}
		terminal := terminalUpdateState[string(state)]
		_, err := tx.Exec(ctx, `
			update instance_update_jobs set status=$2,error_code=nullif($3,''),
				started_at=case when $2 <> 'requested' then coalesce(started_at,now()) else started_at end,
				updated_at=now(),finished_at=case when $4 then now() else null end
			where id=$1`, jobID, string(state), code, terminal)
		if err != nil {
			return err
		}
		if !terminal {
			return nil
		}
		return store.WriteInstanceAudit(ctx, tx, store.InstanceAuditEntry{
			ActorUserID: actorID, Action: "instance.update." + string(state), TargetType: "update_job", TargetID: jobID,
			Metadata: map[string]any{"failure_class": code},
		})
	})
}

var updateStateRank = map[string]int{
	"requested": 0, "preflight": 1, "downloading": 2, "verifying": 3, "stopping": 4,
	"backing_up": 5, "activating": 6, "restarting": 7, "verifying_health": 8,
	"complete": 9, "failed": 9, "recovery_required": 9,
}

var terminalUpdateState = map[string]bool{"complete": true, "failed": true, "recovery_required": true}

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

var errorCodePattern = regexp.MustCompile(`^[a-z][a-z0-9_]{0,63}$`)
