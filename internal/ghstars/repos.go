package ghstars

import (
	"context"
	"crypto/rand"
	"encoding/hex"
	"encoding/json"
	"errors"
	"fmt"
	"net/http"
	"regexp"
	"strings"
	"sync"
	"sync/atomic"
	"time"

	"github.com/jackc/pgx/v5"
	"github.com/jackc/pgx/v5/pgconn"
	"github.com/jackc/pgx/v5/pgxpool"

	platformcrypto "supermetry/internal/platform/crypto"
)

// DefaultRepoLimit is how many repositories an organization may track when
// neither the instance configuration nor the organization sets a limit.
const DefaultRepoLimit = 5

// limitUnlimited is how orgs.github_star_repo_limit stores "no cap".
const limitUnlimited = -1

const (
	publicIDLength   = 16
	publicIDAlphabet = "abcdefghijklmnopqrstuvwxyz0123456789"
	maxErrorLength   = 300
	manualSyncGap    = time.Minute
	syncTimeout      = 10 * time.Minute
)

var (
	// ErrExists reports a repository that is already tracked.
	ErrExists = errors.New("ghstars: repository already tracked")
	// ErrLimit reports that the organization already tracks as many repositories as it may.
	ErrLimit = errors.New("ghstars: repository limit reached")
	// ErrBusy reports a sync that is already running for the repository.
	ErrBusy = errors.New("ghstars: sync already running")
	// ErrTooSoon reports a manual sync requested right after the previous one.
	ErrTooSoon = errors.New("ghstars: sync was started moments ago")

	repoPattern = regexp.MustCompile(`^[a-z0-9_.-]+/[a-z0-9_.-]+$`)
)

// Repo is one tracked repository as the organization API shows it. The webhook secret
// and the token are never part of it.
type Repo struct {
	ID    int64  `json:"id"`
	OrgID int64  `json:"org_id"`
	Repo  string `json:"repo"`
	// PublicCounter makes the star count readable without signing in at
	// /api/github/stars/{owner}/{name}.
	PublicCounter  bool   `json:"public_counter"`
	Enabled        bool   `json:"enabled"`
	TimeZone       string `json:"time_zone"`
	HasToken       bool   `json:"has_token"`
	WebhookURL     string `json:"webhook_url"`
	PublicCountURL string `json:"public_count_url"`
	// HistoryComplete is false while a large repository's history import is unfinished.
	HistoryComplete bool       `json:"history_complete"`
	CreatedAt       time.Time  `json:"created_at"`
	LastWebhookAt   *time.Time `json:"last_webhook_at"`
	LastReconcileAt *time.Time `json:"last_reconcile_at"`
	LastError       *string    `json:"last_error"`
}

// repoRow is a Repo plus the sealed values and public id the service needs.
type repoRow struct {
	Repo
	publicID  string
	secretEnc []byte
	tokenEnc  []byte
	// backfillCursor and backfillComplete track the resumable import of large repositories.
	backfillCursor   *string
	backfillComplete bool
}

// CreateInput describes a repository to track. Token is optional.
type CreateInput struct {
	Repo          string
	Token         string
	TimeZone      string
	PublicCounter bool
}

// UpdateInput changes a repository. Nil fields stay as they are; ClearToken
// removes the stored token and wins over Token.
type UpdateInput struct {
	Enabled       *bool
	TimeZone      *string
	Token         *string
	PublicCounter *bool
	ClearToken    bool
}

// Service manages the tracked repositories and everything that needs their
// secrets: the webhook, the daily reconcile and manual syncs.
type Service struct {
	pool *pgxpool.Pool
	box  *platformcrypto.SecretBox
	// BaseURL is the instance's public URL, used to build webhook URLs. Empty
	// yields root-relative paths.
	BaseURL string
	// APIBase overrides the GitHub API host; empty means api.github.com.
	APIBase string
	HTTP    *http.Client
	Now     func() time.Time
	// AlertChannels and AlertSender deliver star alerts through the
	// organization's notification channels. Either nil turns alerts off.
	AlertChannels AlertChannels
	AlertSender   AlertSender

	// RepoLimit is the default number of repositories one organization may
	// track; an organization's own setting overrides it. Zero means
	// DefaultRepoLimit.
	RepoLimit int
	// Concurrency caps how many repositories the scheduled jobs sync at once
	// across all organizations. Zero means 2.
	Concurrency int
	// Stagger is the pause between starting two scheduled syncs. Zero means one
	// second; a negative value means none.
	Stagger time.Duration

	// LargeThreshold is the star count above which history is imported through
	// GraphQL instead of the REST listing. Zero means 30,000.
	LargeThreshold int
	// ChunkBudget is how long one import run works before it stops and saves its
	// cursor. Zero means four minutes.
	ChunkBudget time.Duration
	// ChunkPages, when positive, also caps the pages one import run reads.
	ChunkPages int
	// GraphQLPageSize is the GraphQL stargazers page size. Zero means 100.
	GraphQLPageSize int
	pageHook        func() // test hook: runs after each saved import page

	mu       sync.Mutex
	running  map[int64]bool
	lastSync map[int64]time.Time
	// lastTestAlert rate limits test alerts per repository.
	lastTestAlert map[int64]time.Time
	alertsSent    atomic.Int64
	alertsFailed  atomic.Int64
	wg            sync.WaitGroup
}

// NewService returns a service sealing secrets with box.
func NewService(pool *pgxpool.Pool, box *platformcrypto.SecretBox, baseURL string, now func() time.Time) *Service {
	if now == nil {
		now = time.Now
	}
	return &Service{
		pool: pool, box: box, BaseURL: strings.TrimRight(baseURL, "/"), Now: now,
		running: make(map[int64]bool), lastSync: make(map[int64]time.Time), lastTestAlert: make(map[int64]time.Time),
	}
}

// Wait blocks until background syncs finish (used by tests and shutdown).
func (s *Service) Wait() { s.wg.Wait() }

func (s *Service) client(token string) *Client {
	c := NewClient(token)
	if s.APIBase != "" {
		c.BaseURL = s.APIBase
	}
	if s.HTTP != nil {
		c.HTTP = s.HTTP
	}
	c.GraphQLPageSize = s.GraphQLPageSize
	return c
}

func newPublicID() (string, error) {
	raw := make([]byte, publicIDLength)
	if _, err := rand.Read(raw); err != nil {
		return "", err
	}
	out := make([]byte, publicIDLength)
	for i, b := range raw {
		out[i] = publicIDAlphabet[int(b)%len(publicIDAlphabet)]
	}
	return string(out), nil
}

func newSecret() (string, error) {
	raw := make([]byte, 32)
	if _, err := rand.Read(raw); err != nil {
		return "", err
	}
	return hex.EncodeToString(raw), nil
}

func (s *Service) webhookURL(publicID string) string {
	return s.BaseURL + "/api/github/webhook/" + publicID
}

const repoColumns = `id, org_id, public_id, repo, public_counter, enabled, timezone, webhook_secret_enc, token_enc, created_at, last_webhook_at, last_reconcile_at, last_error, backfill_cursor, backfill_complete`

func (s *Service) scan(row pgx.Row) (repoRow, error) {
	var r repoRow
	err := row.Scan(&r.ID, &r.OrgID, &r.publicID, &r.Repo.Repo, &r.PublicCounter, &r.Enabled, &r.TimeZone, &r.secretEnc, &r.tokenEnc, &r.CreatedAt, &r.LastWebhookAt, &r.LastReconcileAt, &r.LastError, &r.backfillCursor, &r.backfillComplete)
	if errors.Is(err, pgx.ErrNoRows) {
		return repoRow{}, ErrNotFound
	}
	if err != nil {
		return repoRow{}, err
	}
	r.HasToken = len(r.tokenEnc) > 0
	r.HistoryComplete = r.backfillComplete
	r.WebhookURL = s.webhookURL(r.publicID)
	r.PublicCountURL = s.BaseURL + "/api/github/stars/" + r.Repo.Repo
	return r, nil
}

// load reads a repository by id without checking its organization; it is for
// the scheduler and the webhook, which are not acting for an organization.
func (s *Service) load(ctx context.Context, id int64) (repoRow, error) {
	return s.scan(s.pool.QueryRow(ctx, `select `+repoColumns+` from github_star_repos where id=$1`, id))
}

// loadIn reads a repository of one organization; another organization's
// repository is ErrNotFound.
func (s *Service) loadIn(ctx context.Context, orgID, id int64) (repoRow, error) {
	return s.scan(s.pool.QueryRow(ctx, `select `+repoColumns+` from github_star_repos where id=$1 and org_id=$2`, id, orgID))
}

// Get returns one repository of the organization.
func (s *Service) Get(ctx context.Context, orgID, id int64) (Repo, error) {
	r, err := s.loadIn(ctx, orgID, id)
	return r.Repo, err
}

// List returns the organization's tracked repositories, oldest first.
func (s *Service) List(ctx context.Context, orgID int64) ([]Repo, error) {
	rows, err := s.pool.Query(ctx, `select `+repoColumns+` from github_star_repos where org_id=$1 order by id`, orgID)
	if err != nil {
		return nil, fmt.Errorf("list star repos: %w", err)
	}
	defer rows.Close()
	repos := []Repo{}
	for rows.Next() {
		r, err := s.scan(rows)
		if err != nil {
			return nil, err
		}
		repos = append(repos, r.Repo)
	}
	return repos, rows.Err()
}

func validZone(zone string) (*time.Location, error) {
	loc, err := time.LoadLocation(zone)
	if err != nil || zone == "" || zone == "Local" {
		return nil, fmt.Errorf("%w: time zone must be an IANA name such as UTC or Asia/Kathmandu", ErrInvalid)
	}
	return loc, nil
}

func (s *Service) seal(plain string) []byte { return s.box.Seal([]byte(plain)) }

func (s *Service) open(sealed []byte) (string, error) {
	plain, err := s.box.Open(sealed)
	return string(plain), err
}

func (s *Service) audit(ctx context.Context, tx pgx.Tx, orgID, actor int64, action string, id int64, repo string) error {
	meta, _ := json.Marshal(map[string]any{"repo": repo})
	_, err := tx.Exec(ctx, `
		insert into audit_log(org_id,actor_user_id,action,target_type,target_id,metadata)
		values ($5,nullif($1::bigint,0),$2,'github_star_repo',$3,$4)`, actor, action, id, meta, orgID)
	if err != nil {
		return fmt.Errorf("audit %s: %w", action, err)
	}
	return nil
}

// Create starts tracking a repository and returns it with the new webhook
// secret, which is not retrievable afterwards. It checks that the repository
// exists with one GitHub call; a GitHub outage or rate limit does not block
// the save.
func (s *Service) Create(ctx context.Context, orgID int64, in CreateInput, actor int64) (Repo, string, error) {
	repo := strings.ToLower(strings.TrimSpace(in.Repo))
	if !repoPattern.MatchString(repo) {
		return Repo{}, "", fmt.Errorf("%w: repository must be owner/name", ErrInvalid)
	}
	zone := strings.TrimSpace(in.TimeZone)
	if zone == "" {
		zone = "UTC"
	}
	if _, err := validZone(zone); err != nil {
		return Repo{}, "", err
	}
	token := strings.TrimSpace(in.Token)
	checkCtx, cancel := context.WithTimeout(ctx, requestTimeout)
	_, err := s.client(token).Counts(checkCtx, repo)
	cancel()
	switch {
	case errors.Is(err, ErrRepoNotFound):
		return Repo{}, "", fmt.Errorf("%w: GitHub does not know this repository (or the token cannot see it)", ErrInvalid)
	case errors.Is(err, ErrBadToken):
		return Repo{}, "", fmt.Errorf("%w: GitHub rejected the token", ErrInvalid)
	case errors.Is(err, ErrTokenRequired):
		return Repo{}, "", fmt.Errorf("%w: GitHub requires a token for this repository", ErrInvalid)
	}
	publicID, err := newPublicID()
	if err != nil {
		return Repo{}, "", err
	}
	secret, err := newSecret()
	if err != nil {
		return Repo{}, "", err
	}
	var tokenEnc []byte
	if token != "" {
		tokenEnc = s.seal(token)
	}
	var out repoRow
	err = pgx.BeginFunc(ctx, s.pool, func(tx pgx.Tx) error {
		// The organization row is locked so two creations cannot both slip under the cap.
		var orgLimit *int
		if err := tx.QueryRow(ctx, `select github_star_repo_limit from orgs where id=$1 and deleted_at is null for update`, orgID).Scan(&orgLimit); err != nil {
			if errors.Is(err, pgx.ErrNoRows) {
				return ErrNotFound
			}
			return err
		}
		if max, capped := s.effectiveLimit(orgLimit); capped {
			var count int
			if err := tx.QueryRow(ctx, `select count(*) from github_star_repos where org_id=$1`, orgID).Scan(&count); err != nil {
				return err
			}
			if count >= max {
				return fmt.Errorf("%w: this organization may track %d %s", ErrLimit, max, plural(max))
			}
		}
		row, err := s.scan(tx.QueryRow(ctx, `
			insert into github_star_repos(org_id,public_id,repo,timezone,webhook_secret_enc,token_enc,public_counter)
			values ($1,$2,$3,$4,$5,$6,$7) returning `+repoColumns, orgID, publicID, repo, zone, s.seal(secret), tokenEnc, in.PublicCounter))
		if err != nil {
			return err
		}
		out = row
		return s.audit(ctx, tx, orgID, actor, "github_stars.repo_added", row.ID, repo)
	})
	var pgErr *pgconn.PgError
	if errors.As(err, &pgErr) && pgErr.Code == "23505" {
		return Repo{}, "", ErrExists
	}
	if err != nil {
		return Repo{}, "", err
	}
	return out.Repo, secret, nil
}

// Update changes the enabled flag, the time zone or the token.
func (s *Service) Update(ctx context.Context, orgID, id int64, in UpdateInput, actor int64) (Repo, error) {
	if in.TimeZone != nil {
		if _, err := validZone(strings.TrimSpace(*in.TimeZone)); err != nil {
			return Repo{}, err
		}
	}
	var out repoRow
	err := pgx.BeginFunc(ctx, s.pool, func(tx pgx.Tx) error {
		cur, err := s.scan(tx.QueryRow(ctx, `select `+repoColumns+` from github_star_repos where id=$1 and org_id=$2 for update`, id, orgID))
		if err != nil {
			return err
		}
		enabled, zone, tokenEnc, public := cur.Enabled, cur.TimeZone, cur.tokenEnc, cur.PublicCounter
		if in.Enabled != nil {
			enabled = *in.Enabled
		}
		if in.PublicCounter != nil {
			public = *in.PublicCounter
		}
		if in.TimeZone != nil {
			zone = strings.TrimSpace(*in.TimeZone)
		}
		switch {
		case in.ClearToken:
			tokenEnc = nil
		case in.Token != nil && strings.TrimSpace(*in.Token) != "":
			tokenEnc = s.seal(strings.TrimSpace(*in.Token))
		}
		out, err = s.scan(tx.QueryRow(ctx, `
			update github_star_repos set enabled=$2, timezone=$3, token_enc=$4, public_counter=$5 where id=$1 returning `+repoColumns,
			id, enabled, zone, tokenEnc, public))
		if err != nil {
			return err
		}
		return s.audit(ctx, tx, orgID, actor, "github_stars.repo_updated", id, out.Repo.Repo)
	})
	return out.Repo, err
}

// RotateSecret replaces the webhook secret and returns the new one, once.
func (s *Service) RotateSecret(ctx context.Context, orgID, id int64, actor int64) (Repo, string, error) {
	secret, err := newSecret()
	if err != nil {
		return Repo{}, "", err
	}
	var out repoRow
	err = pgx.BeginFunc(ctx, s.pool, func(tx pgx.Tx) error {
		var err error
		out, err = s.scan(tx.QueryRow(ctx, `update github_star_repos set webhook_secret_enc=$2 where id=$1 and org_id=$3 returning `+repoColumns, id, s.seal(secret), orgID))
		if err != nil {
			return err
		}
		return s.audit(ctx, tx, orgID, actor, "github_stars.secret_rotated", id, out.Repo.Repo)
	})
	if err != nil {
		return Repo{}, "", err
	}
	return out.Repo, secret, nil
}

// Delete removes a repository. With deleteData the row goes and its events,
// state, snapshots, annotations and erasure tombstones follow through the
// foreign keys in the same statement; without it the repository is only
// disabled and its history is kept. It reports whether the configuration was
// removed.
func (s *Service) Delete(ctx context.Context, orgID, id int64, deleteData bool, actor int64) (bool, error) {
	removed := false
	err := pgx.BeginFunc(ctx, s.pool, func(tx pgx.Tx) error {
		cur, err := s.scan(tx.QueryRow(ctx, `select `+repoColumns+` from github_star_repos where id=$1 and org_id=$2 for update`, id, orgID))
		if err != nil {
			return err
		}
		if !deleteData {
			if _, err := tx.Exec(ctx, `update github_star_repos set enabled=false where id=$1`, id); err != nil {
				return err
			}
			return s.audit(ctx, tx, orgID, actor, "github_stars.repo_disabled", id, cur.Repo.Repo)
		}
		if _, err := tx.Exec(ctx, `delete from github_star_repos where id=$1`, id); err != nil {
			return err
		}
		removed = true
		return s.audit(ctx, tx, orgID, actor, "github_stars.repo_removed", id, cur.Repo.Repo)
	})
	return removed, err
}

// StoreFor returns the data store of one repository, bucketed in its time zone.
func (s *Service) StoreFor(ctx context.Context, orgID, id int64) (*Store, Repo, error) {
	r, err := s.loadIn(ctx, orgID, id)
	if err != nil {
		return nil, Repo{}, err
	}
	return s.storeOf(r), r.Repo, nil
}

func (s *Service) storeOf(r repoRow) *Store {
	loc, err := time.LoadLocation(r.TimeZone)
	if err != nil {
		loc = time.UTC
	}
	return NewStore(s.pool, r.ID, r.Repo.Repo, loc)
}

func truncateError(err error) string {
	msg := err.Error()
	if len(msg) > maxErrorLength {
		msg = msg[:maxErrorLength]
	}
	return msg
}

func (s *Service) byPublicID(ctx context.Context, publicID string) (repoRow, error) {
	if len(publicID) != publicIDLength {
		return repoRow{}, ErrNotFound
	}
	return s.scan(s.pool.QueryRow(ctx, `select `+repoColumns+` from github_star_repos where public_id=$1
		and exists (select 1 from orgs o where o.id = org_id and o.deleted_at is null)`, publicID))
}

func plural(n int) string {
	if n == 1 {
		return "repository"
	}
	return "repositories"
}

// effectiveLimit resolves an organization's setting (nil means "use the
// default") to a number; capped is false when there is no cap.
func (s *Service) effectiveLimit(orgLimit *int) (max int, capped bool) {
	switch {
	case orgLimit == nil:
		if s.RepoLimit > 0 {
			return s.RepoLimit, true
		}
		return DefaultRepoLimit, true
	case *orgLimit == limitUnlimited:
		return 0, false
	default:
		return *orgLimit, true
	}
}

// OrgLimit describes how many repositories an organization may track.
type OrgLimit struct {
	// Limit is the cap; nil means unlimited.
	Limit *int `json:"limit"`
	// Source is "org" when the organization has its own setting and "default"
	// when the instance default applies.
	Source string `json:"source"`
	// Tracked is how many repositories the organization tracks now.
	Tracked int `json:"tracked"`
}

// GetOrgLimit returns the effective repository limit of an organization.
func (s *Service) GetOrgLimit(ctx context.Context, orgID int64) (OrgLimit, error) {
	var raw *int
	var tracked int
	err := s.pool.QueryRow(ctx, `
		select o.github_star_repo_limit, (select count(*) from github_star_repos where org_id=o.id)
		from orgs o where o.id=$1`, orgID).Scan(&raw, &tracked)
	if errors.Is(err, pgx.ErrNoRows) {
		return OrgLimit{}, ErrNotFound
	}
	if err != nil {
		return OrgLimit{}, err
	}
	out := OrgLimit{Source: "default", Tracked: tracked}
	if raw != nil {
		out.Source = "org"
	}
	if max, capped := s.effectiveLimit(raw); capped {
		out.Limit = &max
	}
	return out, nil
}

// SetOrgLimit sets an organization's own limit: a number (0 or more), nil for
// unlimited. Lowering it below the current count deletes nothing; it only
// refuses new repositories. The change is audited against the organization.
func (s *Service) SetOrgLimit(ctx context.Context, orgID int64, limit *int, actor int64) error {
	value := limitUnlimited
	if limit != nil {
		if *limit < 0 {
			return fmt.Errorf("%w: limit must be 0 or more", ErrInvalid)
		}
		value = *limit
	}
	return s.writeOrgLimit(ctx, orgID, &value, actor)
}

// ResetOrgLimit removes an organization's own limit so the instance default applies.
func (s *Service) ResetOrgLimit(ctx context.Context, orgID int64, actor int64) error {
	return s.writeOrgLimit(ctx, orgID, nil, actor)
}

func (s *Service) writeOrgLimit(ctx context.Context, orgID int64, value *int, actor int64) error {
	return pgx.BeginFunc(ctx, s.pool, func(tx pgx.Tx) error {
		tag, err := tx.Exec(ctx, `update orgs set github_star_repo_limit=$2 where id=$1`, orgID, value)
		if err != nil {
			return err
		}
		if tag.RowsAffected() == 0 {
			return ErrNotFound
		}
		meta, _ := json.Marshal(map[string]any{"limit": value})
		_, err = tx.Exec(ctx, `
			insert into audit_log(org_id,actor_user_id,action,target_type,target_id,metadata)
			values ($1,nullif($2::bigint,0),'github_stars.limit_changed','organization',$1,$3)`, orgID, actor, meta)
		return err
	})
}
