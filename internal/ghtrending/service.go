package ghtrending

import (
	"context"
	"errors"
	"fmt"
	"log/slog"
	"sort"
	"strings"
	"time"

	"github.com/jackc/pgx/v5"
	"github.com/jackc/pgx/v5/pgxpool"

	"supermetry/internal/scheduler"
)

const (
	// Interval is how often the job runs.
	Interval = 10 * time.Minute
	// MaxListsPerRun caps the page requests of one run.
	MaxListsPerRun = 12
	// MissesToClose is how many consecutive readable fetches without the
	// repository close an episode.
	MissesToClose = 2

	noListPause   = 24 * time.Hour
	maxErrorLen   = 300
	historyLength = 50
)

// ErrNotFound reports an unknown repository.
var ErrNotFound = errors.New("ghtrending: repository not found")

// Sighting is one episode of a repository being on one trending list.
type Sighting struct {
	ID int64 `json:"id"`
	// ListLanguage is the lowercase language of the list; empty means all languages.
	ListLanguage string     `json:"list_language"`
	Since        string     `json:"since"`
	FirstRank    int        `json:"first_rank"`
	BestRank     int        `json:"best_rank"`
	LastRank     int        `json:"last_rank"`
	FirstSeenAt  time.Time  `json:"first_seen_at"`
	LastSeenAt   time.Time  `json:"last_seen_at"`
	LeftAt       *time.Time `json:"left_at"`
}

// TrendingNotifier is told when an episode opens and closes. Calls happen after
// the change is stored, at most once per transition; errors cannot be returned
// because the stored state already moved on. Sighting.ID identifies the episode.
type TrendingNotifier interface {
	Entered(ctx context.Context, repoID int64, s Sighting)
	Left(ctx context.Context, repoID int64, s Sighting)
}

// NopNotifier is the default notifier.
type NopNotifier struct{}

// Entered implements TrendingNotifier.
func (NopNotifier) Entered(context.Context, int64, Sighting) {}

// Left implements TrendingNotifier.
func (NopNotifier) Left(context.Context, int64, Sighting) {}

// Annotator writes the chart marker for a new episode and returns its id.
type Annotator interface {
	Annotate(ctx context.Context, repoID int64, at time.Time, label string) (int64, error)
}

// Service owns the trending tables and the job.
type Service struct {
	pool      *pgxpool.Pool
	Fetcher   ListFetcher
	Notifier  TrendingNotifier
	Annotator Annotator
	Now       func() time.Time
	// MaxLists overrides MaxListsPerRun (tests).
	MaxLists int
}

// NewService returns a service using the real fetcher, no notifications and
// the star chart's annotation table.
func NewService(pool *pgxpool.Pool, now func() time.Time) *Service {
	if now == nil {
		now = time.Now
	}
	return &Service{pool: pool, Fetcher: NewFetcher(), Notifier: NopNotifier{}, Annotator: pgAnnotator{pool: pool}, Now: now}
}

// Job returns the scheduler job. It is silent when no repository opted in.
func (s *Service) Job() scheduler.Job {
	return scheduler.Job{Name: "github-trending", Every: Interval, RunOnStart: true, Run: s.Run}
}

type watched struct {
	id       int64
	language string // display form as GitHub reports it
	key      string // lowercase
}

type listKey struct {
	language string
	since    Since
}

func (s *Service) maxLists() int {
	if s.MaxLists > 0 {
		return s.MaxLists
	}
	return MaxListsPerRun
}

// Run fetches the lists the opted-in repositories need and updates episodes.
func (s *Service) Run(ctx context.Context) error {
	now := s.Now()
	repos, err := s.watchedRepos(ctx)
	if err != nil {
		return err
	}
	if len(repos) == 0 {
		return nil
	}
	wanted := map[listKey]bool{}
	for _, since := range Windows {
		wanted[listKey{"", since}] = true
		for _, r := range repos {
			if r.key != "" {
				wanted[listKey{r.key, since}] = true
			}
		}
	}
	state, err := s.listStates(ctx)
	if err != nil {
		return err
	}
	var due []listKey
	for k := range wanted {
		if st, ok := state[k]; ok && st.skipUntil != nil && now.Before(*st.skipUntil) {
			continue
		}
		due = append(due, k)
	}
	// Oldest attempt first (never attempted first of all) so a cap rotates fairly.
	attempted := func(k listKey) time.Time {
		if st, ok := state[k]; ok && st.lastAttempt != nil {
			return *st.lastAttempt
		}
		return time.Time{}
	}
	sort.Slice(due, func(i, j int) bool {
		if ai, aj := attempted(due[i]), attempted(due[j]); !ai.Equal(aj) {
			return ai.Before(aj)
		}
		if due[i].language != due[j].language {
			return due[i].language < due[j].language
		}
		return due[i].since < due[j].since
	})
	deferred := 0
	if len(due) > s.maxLists() {
		deferred = len(due) - s.maxLists()
		due = due[:s.maxLists()]
	}
	fetched := 0
	var firstErr error
	for _, k := range due {
		if ctx.Err() != nil {
			break
		}
		fetched++
		entries, err := s.Fetcher.Fetch(ctx, k.language, k.since)
		at := s.Now()
		if err != nil {
			if ctx.Err() != nil {
				break
			}
			if serr := s.recordFailure(ctx, k, err, at); serr != nil && firstErr == nil {
				firstErr = serr
			}
			continue
		}
		if err := s.recordSuccess(ctx, k, len(entries), at); err != nil && firstErr == nil {
			firstErr = err
			continue
		}
		if err := s.apply(ctx, k, entries, at); err != nil && firstErr == nil {
			firstErr = err
		}
	}
	if err := s.recordRun(ctx, now, len(due)+deferred, fetched, deferred); err != nil && firstErr == nil {
		firstErr = err
	}
	return firstErr
}

func (s *Service) watchedRepos(ctx context.Context) ([]watched, error) {
	rows, err := s.pool.Query(ctx, `select id, language from github_star_repos where enabled and trending_enabled order by id`)
	if err != nil {
		return nil, fmt.Errorf("list trending repos: %w", err)
	}
	defer rows.Close()
	var out []watched
	for rows.Next() {
		var w watched
		if err := rows.Scan(&w.id, &w.language); err != nil {
			return nil, err
		}
		w.language = strings.TrimSpace(w.language)
		w.key = strings.ToLower(w.language)
		out = append(out, w)
	}
	return out, rows.Err()
}

type listState struct {
	lastAttempt *time.Time
	skipUntil   *time.Time
}

func (s *Service) listStates(ctx context.Context) (map[listKey]listState, error) {
	rows, err := s.pool.Query(ctx, `select list_language, since, last_attempt_at, skip_until from github_trending_list_state`)
	if err != nil {
		return nil, fmt.Errorf("list trending state: %w", err)
	}
	defer rows.Close()
	out := map[listKey]listState{}
	for rows.Next() {
		var k listKey
		var st listState
		var since string
		if err := rows.Scan(&k.language, &since, &st.lastAttempt, &st.skipUntil); err != nil {
			return nil, err
		}
		k.since = Since(since)
		out[k] = st
	}
	return out, rows.Err()
}

func (s *Service) recordSuccess(ctx context.Context, k listKey, n int, at time.Time) error {
	_, err := s.pool.Exec(ctx, `
		insert into github_trending_list_state(list_language, since, last_ok_at, last_attempt_at, last_error, parse_ok, entries, skip_until)
		values ($1,$2,$3,$3,'',true,$4,null)
		on conflict (list_language, since) do update
		set last_ok_at=$3, last_attempt_at=$3, last_error='', parse_ok=true, entries=$4, skip_until=null`,
		k.language, string(k.since), at, n)
	if err != nil {
		return fmt.Errorf("save trending state: %w", err)
	}
	return nil
}

// recordFailure notes a failed fetch. It never touches episodes: an unreadable
// or failed page says nothing about whether a repository is listed.
func (s *Service) recordFailure(ctx context.Context, k listKey, cause error, at time.Time) error {
	msg := cause.Error()
	if len(msg) > maxErrorLen {
		msg = msg[:maxErrorLen]
	}
	parseOK := !errors.Is(cause, ErrUnreadable) && !errors.Is(cause, ErrBodyTooLarge)
	var skip *time.Time
	if errors.Is(cause, ErrNoList) {
		t := at.Add(noListPause)
		skip = &t
	}
	slog.Warn("trending list not read", "language", k.language, "since", k.since, "error", msg)
	_, err := s.pool.Exec(ctx, `
		insert into github_trending_list_state(list_language, since, last_attempt_at, last_error, parse_ok, skip_until)
		values ($1,$2,$3,$4,$5,$6)
		on conflict (list_language, since) do update
		set last_attempt_at=$3, last_error=$4, parse_ok = case when $5 then github_trending_list_state.parse_ok else false end, skip_until=$6`,
		k.language, string(k.since), at, msg, parseOK, skip)
	if err != nil {
		return fmt.Errorf("save trending state: %w", err)
	}
	return nil
}

func (s *Service) recordRun(ctx context.Context, at time.Time, needed, fetched, deferred int) error {
	_, err := s.pool.Exec(ctx, `
		insert into github_trending_run(id, last_run_at, lists_needed, lists_fetched, lists_deferred) values (1,$1,$2,$3,$4)
		on conflict (id) do update set last_run_at=$1, lists_needed=$2, lists_fetched=$3, lists_deferred=$4`,
		at, needed, fetched, deferred)
	if err != nil {
		return fmt.Errorf("save trending run: %w", err)
	}
	return nil
}

type transition struct {
	repoID   int64
	sighting Sighting
	label    string
}

// apply moves every opted-in repository that uses the list through its state
// machine, given a readable list. One fetched list serves all of them.
func (s *Service) apply(ctx context.Context, k listKey, entries []Entry, at time.Time) error {
	rank := make(map[string]int, len(entries))
	for _, e := range entries {
		rank[e.Repo] = e.Rank
	}
	var entered, left []transition
	err := pgx.BeginFunc(ctx, s.pool, func(tx pgx.Tx) error {
		entered, left = nil, nil
		rows, err := tx.Query(ctx, `select id, repo, language from github_star_repos
			where enabled and trending_enabled and ($1 = '' or lower(language) = $1) order by id`, k.language)
		if err != nil {
			return err
		}
		type repo struct {
			id       int64
			name     string
			language string
		}
		var repos []repo
		for rows.Next() {
			var r repo
			if err := rows.Scan(&r.id, &r.name, &r.language); err != nil {
				rows.Close()
				return err
			}
			repos = append(repos, r)
		}
		rows.Close()
		if err := rows.Err(); err != nil {
			return err
		}
		for _, r := range repos {
			var cur Sighting
			var misses int
			err := tx.QueryRow(ctx, `select id, first_rank, best_rank, last_rank, first_seen_at, last_seen_at, misses
				from github_trending_sightings where repo_id=$1 and list_language=$2 and since=$3 and left_at is null for update`,
				r.id, k.language, string(k.since)).Scan(&cur.ID, &cur.FirstRank, &cur.BestRank, &cur.LastRank, &cur.FirstSeenAt, &cur.LastSeenAt, &misses)
			open := true
			if errors.Is(err, pgx.ErrNoRows) {
				open = false
			} else if err != nil {
				return err
			}
			cur.ListLanguage, cur.Since = k.language, string(k.since)
			pos, present := rank[r.name]
			switch {
			case present && open:
				cur.BestRank = min(cur.BestRank, pos)
				cur.LastRank, cur.LastSeenAt = pos, at
				if _, err := tx.Exec(ctx, `update github_trending_sightings set best_rank=$2, last_rank=$3, last_seen_at=$4, misses=0 where id=$1`,
					cur.ID, cur.BestRank, pos, at); err != nil {
					return err
				}
			case present:
				cur.FirstRank, cur.BestRank, cur.LastRank, cur.FirstSeenAt, cur.LastSeenAt = pos, pos, pos, at, at
				if err := tx.QueryRow(ctx, `insert into github_trending_sightings(repo_id, list_language, since, first_rank, best_rank, last_rank, first_seen_at, last_seen_at)
					values ($1,$2,$3,$4,$4,$4,$5,$5) returning id`, r.id, k.language, string(k.since), pos, at).Scan(&cur.ID); err != nil {
					return err
				}
				entered = append(entered, transition{repoID: r.id, sighting: cur})
			case open:
				misses++
				if misses < MissesToClose {
					if _, err := tx.Exec(ctx, `update github_trending_sightings set misses=$2 where id=$1`, cur.ID, misses); err != nil {
						return err
					}
					continue
				}
				cur.LeftAt = &at
				if _, err := tx.Exec(ctx, `update github_trending_sightings set misses=$2, left_at=$3 where id=$1`, cur.ID, misses, at); err != nil {
					return err
				}
				left = append(left, transition{repoID: r.id, sighting: cur})
			}
		}
		return nil
	})
	if err != nil {
		return fmt.Errorf("apply trending list: %w", err)
	}
	for _, t := range entered {
		s.Notifier.Entered(ctx, t.repoID, t.sighting)
	}
	for _, t := range left {
		s.Notifier.Left(ctx, t.repoID, t.sighting)
	}
	s.annotateOpen(ctx, k)
	return nil
}

// annotateOpen writes the chart marker of every open episode of the list that
// has none yet, once per episode. It also repairs an annotation that failed
// when the episode opened.
func (s *Service) annotateOpen(ctx context.Context, k listKey) {
	rows, err := s.pool.Query(ctx, `select t.id, t.repo_id, t.first_seen_at, t.first_rank, r.language
		from github_trending_sightings t join github_star_repos r on r.id=t.repo_id
		where t.list_language=$1 and t.since=$2 and t.left_at is null and t.annotation_id is null`, k.language, string(k.since))
	if err != nil {
		slog.Warn("trending annotations", "error", err)
		return
	}
	type pending struct {
		id, repoID int64
		at         time.Time
		rank       int
		language   string
	}
	var todo []pending
	for rows.Next() {
		var p pending
		if err := rows.Scan(&p.id, &p.repoID, &p.at, &p.rank, &p.language); err == nil {
			todo = append(todo, p)
		}
	}
	rows.Close()
	for _, p := range todo {
		list := "all languages"
		if k.language != "" {
			list = p.language
			if list == "" {
				list = k.language
			}
		}
		label := fmt.Sprintf("On trending: %s, %s, #%d", list, k.since, p.rank)
		id, err := s.Annotator.Annotate(ctx, p.repoID, p.at, label)
		if err != nil {
			slog.Warn("trending annotation failed", "episode", p.id, "error", err)
			continue
		}
		if _, err := s.pool.Exec(ctx, `update github_trending_sightings set annotation_id=$2 where id=$1 and annotation_id is null`, p.id, id); err != nil {
			slog.Warn("trending annotation link failed", "episode", p.id, "error", err)
		}
	}
}

// SetEnabled opts a repository in or out. Opting out closes its open episodes
// without notifying: the watcher stops looking, so it cannot say they ended.
func (s *Service) SetEnabled(ctx context.Context, orgID, repoID int64, enabled bool) error {
	return pgx.BeginFunc(ctx, s.pool, func(tx pgx.Tx) error {
		tag, err := tx.Exec(ctx, `update github_star_repos set trending_enabled=$3 where id=$1 and org_id=$2`, repoID, orgID, enabled)
		if err != nil {
			return err
		}
		if tag.RowsAffected() == 0 {
			return ErrNotFound
		}
		if !enabled {
			_, err = tx.Exec(ctx, `update github_trending_sightings set left_at=$2 where repo_id=$1 and left_at is null`, repoID, s.Now())
		}
		return err
	})
}
