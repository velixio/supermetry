package appquery

import (
	"context"
	"fmt"
	"time"

	"github.com/jackc/pgx/v5"

	"supermetry/internal/apprelease"
)

// NextRelease is the ResolvedInRelease value that means "the next release".
const NextRelease = "next"

const maxReleaseLen = 64

// StatusInput is a requested status change.
type StatusInput struct {
	Status            string  // open, resolved or ignored
	ResolvedInRelease *string // resolved only: a release, or "next"
}

// StatusResult is the issue's state after the change.
type StatusResult struct {
	ID                int64   `json:"id"`
	Status            string  `json:"status"`
	Previous          string  `json:"-"`
	ResolvedInRelease *string `json:"resolved_in_release"`
	ResolvedNext      bool    `json:"resolved_next"`
}

// SetStatus changes an issue's status inside tx, so the caller can write its
// audit row in the same transaction.
//
// "next" stores the newest release seen so far (apprelease ordering over the
// site's events inside retention, falling back to the issue's last release)
// and sets resolved_next: the writer then keeps the issue resolved for that
// release or an older one and regresses it only for a strictly later release. When no release
// has been seen, nothing is stored and any event regresses the issue. Open and
// ignored clear the fix release.
func (q *Querier) SetStatus(ctx context.Context, tx pgx.Tx, scope Scope, issueID int64, in StatusInput) (StatusResult, error) {
	res := StatusResult{ID: issueID, Status: in.Status}
	switch in.Status {
	case "open", "resolved", "ignored":
	default:
		return res, invalid("status must be open, resolved or ignored")
	}
	if in.ResolvedInRelease != nil {
		if in.Status != "resolved" {
			return res, invalid("resolved_in_release is only allowed with status resolved")
		}
		if v := *in.ResolvedInRelease; v == "" || len(v) > maxReleaseLen {
			return res, invalid("resolved_in_release must be 1 to %d characters", maxReleaseLen)
		}
	}
	var lastRelease string
	err := tx.QueryRow(ctx, `select status, last_release from app_issues where id = $1 and site_id = $2 for update`, issueID, scope.SiteID).Scan(&res.Previous, &lastRelease)
	if err != nil {
		if isNoRows(err) {
			return res, ErrNotFound
		}
		return res, fmt.Errorf("lock issue: %w", err)
	}
	if in.Status == "resolved" && in.ResolvedInRelease != nil {
		if *in.ResolvedInRelease == NextRelease {
			newest, err := q.newestRelease(ctx, tx, scope)
			if err != nil {
				return res, err
			}
			if newest == "" {
				newest = lastRelease
			}
			if newest != "" {
				res.ResolvedInRelease, res.ResolvedNext = &newest, true
			}
		} else {
			v := *in.ResolvedInRelease
			res.ResolvedInRelease = &v
		}
	}
	if _, err := tx.Exec(ctx, `update app_issues set status = $2, resolved_in_release = $3, resolved_next = $4 where id = $1`,
		issueID, in.Status, res.ResolvedInRelease, res.ResolvedNext); err != nil {
		return res, fmt.Errorf("update issue status: %w", err)
	}
	return res, nil
}

func (q *Querier) newestRelease(ctx context.Context, tx pgx.Tx, scope Scope) (string, error) {
	var cutoff time.Time
	if scope.RetentionDays > 0 {
		cutoff = q.now().AddDate(0, 0, -scope.RetentionDays)
	}
	rows, err := tx.Query(ctx, `select release, min(ts) from app_events
where site_id = $1 and release <> '' and ($2::timestamptz is null or ts >= $2) group by release`, scope.SiteID, nullTime(cutoff))
	if err != nil {
		return "", fmt.Errorf("read releases: %w", err)
	}
	defer rows.Close()
	firstSeen := map[string]time.Time{}
	var names []string
	for rows.Next() {
		var name string
		var ts time.Time
		if err := rows.Scan(&name, &ts); err != nil {
			return "", err
		}
		firstSeen[name] = ts
		names = append(names, name)
	}
	if err := rows.Err(); err != nil {
		return "", err
	}
	return apprelease.Newest(names, firstSeen), nil
}
