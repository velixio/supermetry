package ghstars

import (
	"context"
	"encoding/json"
	"errors"
	"fmt"
	"io"
	"log/slog"
	"net/http"
	"net/url"
	"strconv"
	"strings"
	"time"

	"supermetry/internal/scheduler"
)

const (
	defaultAPIBase  = "https://api.github.com"
	defaultPageSize = 100
	// maxPages bounds a full stargazer listing (100,000 stars at the default
	// page size). A listing cut short is an error, never applied, so a partial
	// list can't record unstars.
	maxPages       = 1000
	requestTimeout = 20 * time.Second
	maxResponse    = 4 << 20
)

// ErrRateLimited reports a 403 or 429 from GitHub. The next scheduled run retries.
var ErrRateLimited = errors.New("ghstars: github rate limit or access denied")

var (
	// ErrRepoNotFound reports a 404 from GitHub: no such repository, or the token cannot see it.
	ErrRepoNotFound = errors.New("ghstars: repository not found on github")
	// ErrBadToken reports a 401 from GitHub.
	ErrBadToken = errors.New("ghstars: github rejected the token")
	// ErrTokenRequired reports a 401 for a request that carried no token: GitHub
	// lists stargazers with their star dates to authenticated callers only.
	ErrTokenRequired = errors.New("ghstars: GitHub needs a token to list stargazers with their star dates; add a token in the repository settings (read-only; a classic token needs no scopes for a public repository)")
	// ErrPaginationLimited reports GitHub's 422 "pagination is limited" for a
	// stargazer listing too deep for the REST API; the GraphQL import takes over.
	ErrPaginationLimited = errors.New("ghstars: github limits REST pagination for this repository")
	// ErrTokenIgnored reports a run that succeeded only after retrying without
	// the stored token, which GitHub rejected. The data is saved; the admin
	// should replace or clear the token.
	ErrTokenIgnored = errors.New("ghstars: github rejected the stored token (401); synced without it, replace or clear the token in the repository settings")
)

// Client reads one repository from the GitHub REST API. The host is the fixed
// GitHub API unless BaseURL overrides it for tests.
type Client struct {
	BaseURL string
	// Token is optional; unauthenticated requests work at lower limits.
	Token string
	HTTP  *http.Client
	// PageSize is the REST stargazers page size. Zero means 100.
	PageSize int
	// GraphQLPageSize is the GraphQL stargazers page size. Zero means 100.
	GraphQLPageSize int

	retryDelay     time.Duration // test hook; zero means the default backoff
	graphqlTimeout time.Duration // test hook; zero means 30 seconds
}

// Stargazer is one current star with the time it was given.
type Stargazer struct {
	UserID    int64
	Login     string
	StarredAt time.Time
}

// NewClient returns a client with a bounded timeout that never follows redirects.
func NewClient(token string) *Client {
	return &Client{Token: token, HTTP: &http.Client{
		Timeout:       requestTimeout,
		CheckRedirect: func(*http.Request, []*http.Request) error { return http.ErrUseLastResponse },
	}}
}

// githubMessage is ": <message>" from GitHub's JSON error body, or "" when there
// is none. Only a short prefix of the body is read, and it is never logged raw.
func githubMessage(resp *http.Response) string {
	var body struct {
		Message string `json:"message"`
	}
	if err := json.NewDecoder(io.LimitReader(resp.Body, 2048)).Decode(&body); err != nil || body.Message == "" {
		return ""
	}
	message := body.Message
	if len(message) > 200 {
		message = message[:200]
	}
	return ": " + message
}

// refused wraps a GitHub refusal with the request that was refused and, when
// GitHub sends it, the token permissions that request needs, so the saved error
// tells an admin which part of the setup is wrong. errors.Is still matches base.
func refused(base error, path string, resp *http.Response) error {
	detail := " (GET " + path
	if needed := resp.Header.Get("X-Accepted-GitHub-Permissions"); needed != "" {
		detail += "; needs token permissions: " + needed
	}
	return fmt.Errorf("%w%s)", base, detail)
}

func (c *Client) get(ctx context.Context, path string, query url.Values, accept string, out any) error {
	base := c.BaseURL
	if base == "" {
		base = defaultAPIBase
	}
	target := base + path
	if len(query) > 0 {
		target += "?" + query.Encode()
	}
	req, err := http.NewRequestWithContext(ctx, http.MethodGet, target, nil)
	if err != nil {
		return err
	}
	req.Header.Set("Accept", accept)
	req.Header.Set("X-GitHub-Api-Version", "2022-11-28")
	req.Header.Set("User-Agent", "supermetry")
	if c.Token != "" {
		req.Header.Set("Authorization", "Bearer "+c.Token)
	}
	client := c.HTTP
	if client == nil {
		client = http.DefaultClient
	}
	resp, err := client.Do(req)
	if err != nil {
		return fmt.Errorf("github request: %w", err)
	}
	defer resp.Body.Close()
	switch {
	case resp.StatusCode == http.StatusForbidden || resp.StatusCode == http.StatusTooManyRequests:
		return refused(ErrRateLimited, path, resp)
	case resp.StatusCode == http.StatusNotFound:
		return refused(ErrRepoNotFound, path, resp)
	case resp.StatusCode == http.StatusUnauthorized && c.Token == "":
		return refused(ErrTokenRequired, path, resp)
	case resp.StatusCode == http.StatusUnauthorized:
		return refused(ErrBadToken, path, resp)
	case resp.StatusCode == http.StatusUnprocessableEntity:
		message := githubMessage(resp)
		if strings.Contains(message, "pagination is limited") {
			return fmt.Errorf("%w (GET %s)%s", ErrPaginationLimited, path, message)
		}
		return fmt.Errorf("github request: status %d (GET %s)%s", resp.StatusCode, path, message)
	case resp.StatusCode != http.StatusOK:
		return fmt.Errorf("github request: status %d (GET %s)%s", resp.StatusCode, path, githubMessage(resp))
	}
	if err := json.NewDecoder(io.LimitReader(resp.Body, maxResponse)).Decode(out); err != nil {
		return fmt.Errorf("decode github response: %w", err)
	}
	return nil
}

// Counts fetches the repository counters.
func (c *Client) Counts(ctx context.Context, repo string) (Counts, error) {
	var body struct {
		Stars      int `json:"stargazers_count"`
		Forks      int `json:"forks_count"`
		Watchers   int `json:"subscribers_count"`
		OpenIssues int `json:"open_issues_count"`
	}
	if err := c.get(ctx, "/repos/"+repo, nil, "application/vnd.github+json", &body); err != nil {
		return Counts{}, err
	}
	return Counts{Stars: body.Stars, Forks: body.Forks, Watchers: body.Watchers, OpenIssues: body.OpenIssues}, nil
}

// Stargazers pages through every current stargazer.
func (c *Client) Stargazers(ctx context.Context, repo string) ([]Stargazer, error) {
	size := c.PageSize
	if size <= 0 {
		size = defaultPageSize
	}
	var all []Stargazer
	for page := 1; page <= maxPages; page++ {
		var body []struct {
			StarredAt time.Time `json:"starred_at"`
			User      struct {
				ID    int64  `json:"id"`
				Login string `json:"login"`
			} `json:"user"`
		}
		query := url.Values{"per_page": {strconv.Itoa(size)}, "page": {strconv.Itoa(page)}}
		if err := c.get(ctx, "/repos/"+repo+"/stargazers", query, "application/vnd.github.star+json", &body); err != nil {
			return nil, err
		}
		for _, item := range body {
			all = append(all, Stargazer{UserID: item.User.ID, Login: item.User.Login, StarredAt: item.StarredAt})
		}
		if len(body) < size {
			return all, nil
		}
	}
	return nil, fmt.Errorf("github stargazers: more than %d pages", maxPages)
}

// ReconcileJob returns the daily scheduler job. It also runs at startup, which
// performs the one-time backfill for newly added repositories. Repositories are
// handled one by one and a failure is recorded on that repository only.
func (s *Service) ReconcileJob() scheduler.Job {
	return scheduler.Job{Name: "github-stars-reconcile", Every: 24 * time.Hour, RunOnStart: true, Run: s.ReconcileAll}
}

// ReconcileAll reconciles every enabled repository.
func (s *Service) ReconcileAll(ctx context.Context) error {
	rows, err := s.pool.Query(ctx, `select id from github_star_repos where enabled order by id`)
	if err != nil {
		return fmt.Errorf("list star repos: %w", err)
	}
	var ids []int64
	for rows.Next() {
		var id int64
		if err := rows.Scan(&id); err != nil {
			rows.Close()
			return err
		}
		ids = append(ids, id)
	}
	rows.Close()
	if err := rows.Err(); err != nil {
		return err
	}
	for _, id := range ids {
		if ctx.Err() != nil {
			return ctx.Err()
		}
		if err := s.ReconcileRepo(ctx, id); err != nil && !errors.Is(err, ErrBusy) {
			slog.Warn("github star reconcile failed", "repo_id", id, "error", err)
		}
	}
	return nil
}

// acquire takes the per-repository lock shared by the daily job, the import
// job and manual syncs, so they never overlap.
func (s *Service) acquire(id int64) bool {
	s.mu.Lock()
	defer s.mu.Unlock()
	if s.running[id] {
		return false
	}
	s.running[id] = true
	return true
}

func (s *Service) release(id int64) {
	s.mu.Lock()
	delete(s.running, id)
	s.mu.Unlock()
}

// StartSync reconciles one repository in the background (for a large
// repository that is one import chunk). At most one run happens per repository
// and manual syncs are at least a minute apart.
func (s *Service) StartSync(ctx context.Context, id int64) error {
	if _, err := s.load(ctx, id); err != nil {
		return err
	}
	now := s.Now()
	s.mu.Lock()
	if s.running[id] {
		s.mu.Unlock()
		return ErrBusy
	}
	if last, ok := s.lastSync[id]; ok && now.Sub(last) < manualSyncGap {
		s.mu.Unlock()
		return ErrTooSoon
	}
	s.running[id] = true
	s.lastSync[id] = now
	s.mu.Unlock()
	s.wg.Add(1)
	go func() {
		defer s.wg.Done()
		defer s.release(id)
		timeout := syncTimeout
		if padded := s.chunkBudget() + syncSlack; padded > timeout {
			timeout = padded
		}
		runCtx, cancel := context.WithTimeout(context.Background(), timeout)
		defer cancel()
		if err := s.reconcileLocked(runCtx, id); err != nil {
			slog.Warn("github star sync failed", "repo_id", id, "error", err)
		}
	}()
	return nil
}

// ReconcileRepo records today's snapshot and brings the history up to date:
// repositories up to LargeThreshold stars get a full diff against the REST
// stargazer list, larger ones (or ones GitHub refuses to page) a resumable
// GraphQL import and then an incremental read of the newest pages. The outcome,
// including a rate limit, is saved on the repository as last_error /
// last_reconcile_at. It returns ErrBusy when another run is in progress.
func (s *Service) ReconcileRepo(ctx context.Context, id int64) error {
	if !s.acquire(id) {
		return ErrBusy
	}
	defer s.release(id)
	return s.reconcileLocked(ctx, id)
}

func (s *Service) reconcileLocked(ctx context.Context, id int64) error {
	r, err := s.load(ctx, id)
	if err != nil {
		return err
	}
	runErr := s.reconcile(ctx, r)
	status := (*string)(nil)
	if runErr != nil {
		msg := truncateError(runErr)
		status = &msg
	}
	if _, err := s.pool.Exec(ctx, `update github_star_repos set last_reconcile_at=$2, last_error=$3 where id=$1`, id, s.Now(), status); err != nil {
		return errors.Join(runErr, err)
	}
	return runErr
}

func (s *Service) reconcile(ctx context.Context, r repoRow) error {
	token := ""
	if len(r.tokenEnc) > 0 {
		var err error
		if token, err = s.open(r.tokenEnc); err != nil {
			return errors.New("stored token cannot be decrypted")
		}
	}
	err := s.reconcileWith(ctx, r, token)
	if token != "" && errors.Is(err, ErrBadToken) {
		// A stale token is worse than none for a public repository, so retry once
		// without it. Private repositories fail again here with not found.
		retryErr := s.reconcileWith(ctx, r, "")
		if errors.Is(retryErr, ErrTokenRequired) {
			return err // the stargazer list needs a working token, so the old one is the problem
		}
		if retryErr != nil {
			return retryErr
		}
		return ErrTokenIgnored
	}
	return err
}

func (s *Service) reconcileWith(ctx context.Context, r repoRow, token string) error {
	client, store, now := s.client(token), s.storeOf(r), s.Now()
	counts, err := client.Counts(ctx, store.Repo())
	if err != nil {
		return err
	}
	if counts.Stars > s.largeThreshold() {
		return s.reconcileLarge(ctx, r, store, client, counts)
	}
	events, err := store.EventCount(ctx)
	if err != nil {
		return err
	}
	stored, known, err := store.Stars(ctx)
	if err != nil {
		return err
	}
	recorded, err := store.CurrentStargazers(ctx)
	if err != nil {
		return err
	}
	// The live count alone is not proof the history is complete: a webhook that
	// arrives before the first sync sets it to GitHub's total while only that
	// one star is on record. Compare the stargazers actually recorded too.
	if events == 0 || !known || stored != counts.Stars || len(recorded) != counts.Stars {
		if err := repairHistory(ctx, store, client, events == 0, now); err != nil {
			if errors.Is(err, ErrPaginationLimited) {
				return s.reconcileLarge(ctx, r, store, client, counts)
			}
			// The counts are still worth keeping: today's snapshot does not need
			// the stargazer list. The live row is left alone so the next run
			// notices the history is still incomplete.
			if snapErr := store.SaveSnapshot(ctx, now.In(store.loc), counts); snapErr != nil {
				return errors.Join(err, snapErr)
			}
			return err
		}
	}
	if err := store.SaveState(ctx, counts, now); err != nil {
		return err
	}
	if !r.backfillComplete {
		if err := store.MarkBackfillComplete(ctx); err != nil {
			return err
		}
	}
	return store.SaveSnapshot(ctx, now.In(store.loc), counts)
}

func repairHistory(ctx context.Context, store *Store, client *Client, backfill bool, now time.Time) error {
	remote, err := client.Stargazers(ctx, store.Repo())
	if err != nil {
		return err
	}
	local, err := store.CurrentStargazers(ctx)
	if err != nil {
		return err
	}
	source := SourceReconcile
	if backfill {
		source = SourceBackfill
	}
	var missing []Event
	present := make(map[int64]bool, len(remote))
	for _, g := range remote {
		present[g.UserID] = true
		if _, ok := local[g.UserID]; !ok {
			missing = append(missing, Event{UserID: g.UserID, Login: g.Login, Action: ActionStarred, OccurredAt: g.StarredAt, Source: source})
		}
	}
	for id, login := range local {
		if !present[id] {
			missing = append(missing, Event{UserID: id, Login: login, Action: ActionUnstarred, OccurredAt: now, Source: SourceReconcile})
		}
	}
	return store.InsertEvents(ctx, missing)
}
