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

// Client reads one repository from the GitHub REST API. The host is the fixed
// GitHub API unless BaseURL overrides it for tests.
type Client struct {
	BaseURL string
	// Token is optional; unauthenticated requests work at lower limits.
	Token string
	HTTP  *http.Client
	// PageSize is the stargazers page size. Zero means 100.
	PageSize int
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
		return ErrRateLimited
	case resp.StatusCode != http.StatusOK:
		return fmt.Errorf("github request: status %d", resp.StatusCode)
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

// Reconciler repairs missed webhooks and backfills existing stargazers.
type Reconciler struct {
	Store  *Store
	Client *Client
	Now    func() time.Time
}

// Job returns the daily scheduler job. It also runs at startup, which performs
// the one-time backfill on a fresh install. A rate limit is logged and left
// for the next run rather than reported as a failure.
func (r *Reconciler) Job() scheduler.Job {
	return scheduler.Job{Name: "github-stars-reconcile", Every: 24 * time.Hour, RunOnStart: true, Run: func(ctx context.Context) error {
		err := r.Run(ctx)
		if errors.Is(err, ErrRateLimited) {
			slog.Warn("github star reconcile skipped: rate limited or access denied")
			return nil
		}
		return err
	}}
}

// Run records today's snapshot and, when GitHub's star count differs from the
// live row (or no history exists yet), diffs the full stargazer list against
// the recorded history. With no events at all the inserted stars are marked as
// backfill, otherwise as reconcile.
func (r *Reconciler) Run(ctx context.Context) error {
	now := r.Now()
	counts, err := r.Client.Counts(ctx, r.Store.Repo())
	if err != nil {
		return err
	}
	events, err := r.Store.EventCount(ctx)
	if err != nil {
		return err
	}
	stored, known, err := r.Store.Stars(ctx)
	if err != nil {
		return err
	}
	if events == 0 || !known || stored != counts.Stars {
		if err := r.repairHistory(ctx, events == 0, now); err != nil {
			return err
		}
	}
	if err := r.Store.SaveState(ctx, counts, now); err != nil {
		return err
	}
	return r.Store.SaveSnapshot(ctx, now.In(r.Store.loc), counts)
}

func (r *Reconciler) repairHistory(ctx context.Context, backfill bool, now time.Time) error {
	remote, err := r.Client.Stargazers(ctx, r.Store.Repo())
	if err != nil {
		return err
	}
	local, err := r.Store.CurrentStargazers(ctx)
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
	return r.Store.InsertEvents(ctx, missing)
}
