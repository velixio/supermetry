package appissues

import (
	"context"
	"errors"
	"fmt"
	"strings"
	"time"
	"unicode/utf8"

	"github.com/jackc/pgx/v5"
)

// MaxCommentLen is the longest comment body, in characters.
const MaxCommentLen = 10000

// Comment is a user comment on an issue.
type Comment struct {
	ID           int64      `json:"id"`
	IssueID      int64      `json:"issue_id"`
	AuthorUserID *int64     `json:"author_user_id"`
	AuthorName   string     `json:"author_name"`
	Body         string     `json:"body"`
	CreatedAt    time.Time  `json:"created_at"`
	EditedAt     *time.Time `json:"edited_at"`
}

func checkBody(body string) (string, error) {
	body = strings.TrimSpace(body)
	if n := utf8.RuneCountInString(body); n < 1 || n > MaxCommentLen {
		return "", invalid("a comment must be 1 to %d characters", MaxCommentLen)
	}
	return body, nil
}

const commentSelect = `select c.id, c.issue_id, c.author_user_id, coalesce(u.name, ''), c.body, c.created_at, c.edited_at
from app_issue_comments c left join users u on u.id = c.author_user_id`

func scanComment(row pgx.Row) (Comment, error) {
	var c Comment
	err := row.Scan(&c.ID, &c.IssueID, &c.AuthorUserID, &c.AuthorName, &c.Body, &c.CreatedAt, &c.EditedAt)
	return c, err
}

// Comment adds a comment and its "commented" activity row.
func (s *Service) Comment(ctx context.Context, siteID int32, issueID int64, actor Actor, body string) (Comment, error) {
	body, err := checkBody(body)
	if err != nil {
		return Comment{}, err
	}
	if actor.System || actor.UserID == 0 {
		return Comment{}, ErrForbidden
	}
	var out Comment
	err = pgx.BeginFunc(ctx, s.pool, func(tx pgx.Tx) error {
		var ok bool
		if err := tx.QueryRow(ctx, `select exists (select 1 from app_issues where id = $1 and site_id = $2)`, issueID, siteID).Scan(&ok); err != nil {
			return fmt.Errorf("read issue: %w", err)
		}
		if !ok {
			return ErrNotFound
		}
		now := s.now()
		var id int64
		if err := tx.QueryRow(ctx, `insert into app_issue_comments (site_id, issue_id, author_user_id, body, created_at) values ($1, $2, $3, $4, $5) returning id`,
			siteID, issueID, actor.UserID, body, now).Scan(&id); err != nil {
			return fmt.Errorf("insert comment: %w", err)
		}
		if err := record(ctx, tx, siteID, issueID, now, actor.id(), KindCommented, map[string]any{"comment_id": id}); err != nil {
			return err
		}
		out, err = scanComment(tx.QueryRow(ctx, commentSelect+` where c.id = $1`, id))
		return err
	})
	return out, err
}

// Comments lists the issue's comments, oldest first, without deleted ones.
func (s *Service) Comments(ctx context.Context, siteID int32, issueID int64) ([]Comment, error) {
	if err := s.issueExists(ctx, siteID, issueID); err != nil {
		return nil, err
	}
	rows, err := s.pool.Query(ctx, commentSelect+` where c.issue_id = $1 and c.site_id = $2 and c.deleted_at is null order by c.created_at, c.id limit 500`, issueID, siteID)
	if err != nil {
		return nil, fmt.Errorf("read comments: %w", err)
	}
	defer rows.Close()
	out := []Comment{}
	for rows.Next() {
		c, err := scanComment(rows)
		if err != nil {
			return nil, err
		}
		out = append(out, c)
	}
	return out, rows.Err()
}

// lockComment returns the author of a live comment of the site and checks the
// actor may change it.
func lockComment(ctx context.Context, tx pgx.Tx, siteID int32, commentID int64, actor Actor) error {
	var author *int64
	err := tx.QueryRow(ctx, `select author_user_id from app_issue_comments where id = $1 and site_id = $2 and deleted_at is null for update`, commentID, siteID).Scan(&author)
	if errors.Is(err, pgx.ErrNoRows) {
		return ErrNotFound
	}
	if err != nil {
		return fmt.Errorf("lock comment: %w", err)
	}
	if !actor.Admin && (author == nil || actor.System || *author != actor.UserID) {
		return ErrForbidden
	}
	return nil
}

// EditComment replaces a comment's body; the author or an admin only.
func (s *Service) EditComment(ctx context.Context, siteID int32, commentID int64, actor Actor, body string) (Comment, error) {
	body, err := checkBody(body)
	if err != nil {
		return Comment{}, err
	}
	var out Comment
	err = pgx.BeginFunc(ctx, s.pool, func(tx pgx.Tx) error {
		if err := lockComment(ctx, tx, siteID, commentID, actor); err != nil {
			return err
		}
		if _, err := tx.Exec(ctx, `update app_issue_comments set body = $2, edited_at = $3 where id = $1`, commentID, body, s.now()); err != nil {
			return fmt.Errorf("edit comment: %w", err)
		}
		out, err = scanComment(tx.QueryRow(ctx, commentSelect+` where c.id = $1`, commentID))
		return err
	})
	return out, err
}

// DeleteComment removes a comment from view (the row is kept, marked
// deleted); the author or an admin only.
func (s *Service) DeleteComment(ctx context.Context, siteID int32, commentID int64, actor Actor) error {
	return pgx.BeginFunc(ctx, s.pool, func(tx pgx.Tx) error {
		if err := lockComment(ctx, tx, siteID, commentID, actor); err != nil {
			return err
		}
		if _, err := tx.Exec(ctx, `update app_issue_comments set deleted_at = $2 where id = $1`, commentID, s.now()); err != nil {
			return fmt.Errorf("delete comment: %w", err)
		}
		return nil
	})
}
