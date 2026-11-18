package appissues

import (
	"context"
	"errors"
	"fmt"
	"strings"
	"time"
	"unicode/utf8"

	"github.com/jackc/pgx/v5"
	"github.com/jackc/pgx/v5/pgconn"

	"supermetry/internal/appquery/search"
)

// ErrViewExists reports a second view with the same name for the same owner.
var ErrViewExists = errors.New("a view with this name already exists")

// View is a saved search. Private views are visible to their owner only,
// shared views to everyone who can open the project.
type View struct {
	ID        int64     `json:"id"`
	Name      string    `json:"name"`
	Query     string    `json:"query"`
	Shared    bool      `json:"shared"`
	Mine      bool      `json:"mine"`
	OwnerName string    `json:"owner_name"`
	CreatedAt time.Time `json:"created_at"`
}

// ViewChange is a partial update of a view.
type ViewChange struct {
	Name   *string `json:"name"`
	Query  *string `json:"query"`
	Shared *bool   `json:"shared"`
}

const (
	maxViewName = 80
	viewSelect  = `select v.id, v.name, v.query, v.shared, v.owner_user_id is not distinct from $2, coalesce(u.name, ''), v.created_at
from app_saved_views v left join users u on u.id = v.owner_user_id`
)

func checkView(name, query string) (string, string, error) {
	name = strings.TrimSpace(name)
	if n := utf8.RuneCountInString(name); n < 1 || n > maxViewName || strings.ContainsRune(name, 0) {
		return "", "", invalid("a view name must be 1 to %d characters", maxViewName)
	}
	query = strings.TrimSpace(query)
	if _, err := search.Parse(query); err != nil {
		return "", "", invalid("query: %s", err.Error())
	}
	return name, query, nil
}

func scanView(row pgx.Row) (View, error) {
	var v View
	err := row.Scan(&v.ID, &v.Name, &v.Query, &v.Shared, &v.Mine, &v.OwnerName, &v.CreatedAt)
	return v, err
}

// Views lists the views the user can see: their own and the shared ones.
func (s *Service) Views(ctx context.Context, siteID int32, userID int64) ([]View, error) {
	rows, err := s.pool.Query(ctx, viewSelect+`
where v.site_id = $1 and (v.shared or v.owner_user_id = $2) order by lower(v.name), v.id`, siteID, userID)
	if err != nil {
		return nil, fmt.Errorf("list views: %w", err)
	}
	defer rows.Close()
	out := []View{}
	for rows.Next() {
		v, err := scanView(rows)
		if err != nil {
			return nil, err
		}
		out = append(out, v)
	}
	return out, rows.Err()
}

func isUnique(err error) bool {
	var pg *pgconn.PgError
	return errors.As(err, &pg) && pg.Code == "23505"
}

// CreateView saves a search for the actor.
func (s *Service) CreateView(ctx context.Context, siteID int32, actor Actor, name, query string, shared bool) (View, error) {
	name, query, err := checkView(name, query)
	if err != nil {
		return View{}, err
	}
	var id int64
	err = s.pool.QueryRow(ctx, `insert into app_saved_views (site_id, owner_user_id, name, query, shared, created_at)
values ($1, $2, $3, $4, $5, $6) returning id`, siteID, actor.id(), name, query, shared, s.now()).Scan(&id)
	if isUnique(err) {
		return View{}, ErrViewExists
	}
	if err != nil {
		return View{}, fmt.Errorf("create view: %w", err)
	}
	return scanView(s.pool.QueryRow(ctx, viewSelect+` where v.id = $1 and v.site_id = $3`, id, actor.UserID, siteID))
}

// loadOwned finds a view the actor may change: their own, or (admins only) a
// shared one. Another member's private view does not exist for the actor.
func (s *Service) loadOwned(ctx context.Context, tx pgx.Tx, siteID int32, id int64, actor Actor) (View, error) {
	var owner *int64
	v := View{ID: id}
	err := tx.QueryRow(ctx, `select name, query, shared, owner_user_id from app_saved_views where id = $1 and site_id = $2 for update`, id, siteID).
		Scan(&v.Name, &v.Query, &v.Shared, &owner)
	if errors.Is(err, pgx.ErrNoRows) {
		return v, ErrNotFound
	}
	if err != nil {
		return v, fmt.Errorf("read view: %w", err)
	}
	mine := owner != nil && *owner == actor.UserID
	switch {
	case mine, v.Shared && actor.Admin:
		return v, nil
	case v.Shared:
		return v, ErrForbidden
	default:
		return v, ErrNotFound
	}
}

// UpdateView renames a view, changes its search or shares or unshares it.
func (s *Service) UpdateView(ctx context.Context, siteID int32, id int64, actor Actor, ch ViewChange) (View, error) {
	err := pgx.BeginFunc(ctx, s.pool, func(tx pgx.Tx) error {
		v, err := s.loadOwned(ctx, tx, siteID, id, actor)
		if err != nil {
			return err
		}
		if ch.Name != nil {
			v.Name = *ch.Name
		}
		if ch.Query != nil {
			v.Query = *ch.Query
		}
		if ch.Shared != nil {
			v.Shared = *ch.Shared
		}
		if v.Name, v.Query, err = checkView(v.Name, v.Query); err != nil {
			return err
		}
		_, err = tx.Exec(ctx, `update app_saved_views set name = $2, query = $3, shared = $4 where id = $1`, id, v.Name, v.Query, v.Shared)
		if isUnique(err) {
			return ErrViewExists
		}
		return err
	})
	if err != nil {
		return View{}, err
	}
	return scanView(s.pool.QueryRow(ctx, viewSelect+` where v.id = $1 and v.site_id = $3`, id, actor.UserID, siteID))
}

// DeleteView removes a view.
func (s *Service) DeleteView(ctx context.Context, siteID int32, id int64, actor Actor) error {
	return pgx.BeginFunc(ctx, s.pool, func(tx pgx.Tx) error {
		if _, err := s.loadOwned(ctx, tx, siteID, id, actor); err != nil {
			return err
		}
		_, err := tx.Exec(ctx, `delete from app_saved_views where id = $1`, id)
		return err
	})
}
