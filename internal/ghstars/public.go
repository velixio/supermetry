package ghstars

import (
	"context"
	"errors"
	"net/http"
	"strconv"
	"strings"

	"github.com/jackc/pgx/v5"
)

// Limiter decides whether a client may make another request.
type Limiter interface{ Allow(key string) bool }

// PublicCount serves GET /api/github/stars/{owner}/{name}: the live star count
// of a tracked, enabled repository whose organization turned its public counter
// on, as {"stars":N} and nothing else. The value is GitHub-verified: it is the
// count the reconcile and import jobs read from GitHub, stored separately from
// the live count that webhooks update, so an organization cannot publish a
// forged number under a repository name by signing its own webhook payloads. A
// repository that has never been read from GitHub is not served. When several
// organizations publish the same repository, the one verified most recently
// answers. The request itself never calls GitHub. Every miss (unknown, disabled, never synced,
// not public) answers the same 404 so the response does not reveal which
// repositories are tracked.
type PublicCount struct {
	Service *Service
	// Limiter is optional and keyed by ClientKey.
	Limiter   Limiter
	ClientKey func(*http.Request) string
}

const notFoundBody = "{\"error\":\"not found\"}\n"

func (h *PublicCount) ServeHTTP(w http.ResponseWriter, r *http.Request) {
	header := w.Header()
	header.Set("Access-Control-Allow-Origin", "*")
	header.Set("X-Content-Type-Options", "nosniff")
	if r.Method == http.MethodOptions {
		header.Set("Access-Control-Allow-Methods", "GET, HEAD, OPTIONS")
		header.Set("Access-Control-Max-Age", "86400")
		w.WriteHeader(http.StatusNoContent)
		return
	}
	header.Set("Content-Type", "application/json")
	if h.Limiter != nil {
		key := r.RemoteAddr
		if h.ClientKey != nil {
			key = h.ClientKey(r)
		}
		if !h.Limiter.Allow("ghstars:" + key) {
			header.Set("Retry-After", "60")
			header.Set("Cache-Control", "no-store")
			w.WriteHeader(http.StatusTooManyRequests)
			h.write(w, r, "{\"error\":\"rate limited\"}\n")
			return
		}
	}
	repo := strings.ToLower(r.PathValue("owner") + "/" + r.PathValue("name"))
	stars, ok, err := h.Service.publicStars(r.Context(), repo)
	switch {
	case err != nil:
		header.Set("Cache-Control", "no-store")
		w.WriteHeader(http.StatusInternalServerError)
		h.write(w, r, "{\"error\":\"unavailable\"}\n")
	case !ok:
		header.Set("Cache-Control", "public, max-age=60")
		w.WriteHeader(http.StatusNotFound)
		h.write(w, r, notFoundBody)
	default:
		header.Set("Cache-Control", "public, max-age=60")
		w.WriteHeader(http.StatusOK)
		h.write(w, r, "{\"stars\":"+strconv.Itoa(stars)+"}\n")
	}
}

func (h *PublicCount) write(w http.ResponseWriter, r *http.Request, body string) {
	if r.Method != http.MethodHead {
		_, _ = w.Write([]byte(body))
	}
}

// publicStars returns the GitHub-verified star count of the enabled, public
// repository claimant that was verified most recently.
func (s *Service) publicStars(ctx context.Context, repo string) (int, bool, error) {
	if !repoPattern.MatchString(repo) {
		return 0, false, nil
	}
	var stars int
	err := s.pool.QueryRow(ctx, `
		select r.verified_stars from github_star_repos r
		join orgs o on o.id = r.org_id and o.deleted_at is null
		where r.repo = $1 and r.enabled and r.public_counter and r.verified_at is not null
		order by r.verified_at desc, r.id limit 1`, repo).Scan(&stars)
	if errors.Is(err, pgx.ErrNoRows) {
		return 0, false, nil
	}
	return stars, err == nil, err
}
