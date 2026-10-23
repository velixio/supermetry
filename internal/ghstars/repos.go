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
	"time"

	"github.com/jackc/pgx/v5"
	"github.com/jackc/pgx/v5/pgconn"
	"github.com/jackc/pgx/v5/pgxpool"

	platformcrypto "supermetry/internal/platform/crypto"
)

// MaxRepos caps how many repositories one instance tracks.
const MaxRepos = 50

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
	// ErrLimit reports that MaxRepos repositories are already tracked.
	ErrLimit = errors.New("ghstars: repository limit reached")
	// ErrBusy reports a sync that is already running for the repository.
	ErrBusy = errors.New("ghstars: sync already running")
	// ErrTooSoon reports a manual sync requested right after the previous one.
	ErrTooSoon = errors.New("ghstars: sync was started moments ago")

	repoPattern = regexp.MustCompile(`^[a-z0-9_.-]+/[a-z0-9_.-]+$`)
)

// Repo is one tracked repository as the admin API shows it. The webhook secret
// and the token are never part of it.
type Repo struct {
	ID              int64      `json:"id"`
	Repo            string     `json:"repo"`
	Enabled         bool       `json:"enabled"`
	TimeZone        string     `json:"time_zone"`
	HasToken        bool       `json:"has_token"`
	WebhookURL      string     `json:"webhook_url"`
	PublicCountURL  string     `json:"public_count_url"`
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
}

// CreateInput describes a repository to track. Token is optional.
type CreateInput struct {
	Repo     string
	Token    string
	TimeZone string
}

// UpdateInput changes a repository. Nil fields stay as they are; ClearToken
// removes the stored token and wins over Token.
type UpdateInput struct {
	Enabled    *bool
	TimeZone   *string
	Token      *string
	ClearToken bool
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

	mu       sync.Mutex
	running  map[int64]bool
	lastSync map[int64]time.Time
	wg       sync.WaitGroup
}

// NewService returns a service sealing secrets with box.
func NewService(pool *pgxpool.Pool, box *platformcrypto.SecretBox, baseURL string, now func() time.Time) *Service {
	if now == nil {
		now = time.Now
	}
	return &Service{
		pool: pool, box: box, BaseURL: strings.TrimRight(baseURL, "/"), Now: now,
		running: make(map[int64]bool), lastSync: make(map[int64]time.Time),
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

const repoColumns = `id, public_id, repo, enabled, timezone, webhook_secret_enc, token_enc, created_at, last_webhook_at, last_reconcile_at, last_error`

func (s *Service) scan(row pgx.Row) (repoRow, error) {
	var r repoRow
	err := row.Scan(&r.ID, &r.publicID, &r.Repo.Repo, &r.Enabled, &r.TimeZone, &r.secretEnc, &r.tokenEnc, &r.CreatedAt, &r.LastWebhookAt, &r.LastReconcileAt, &r.LastError)
	if errors.Is(err, pgx.ErrNoRows) {
		return repoRow{}, ErrNotFound
	}
	if err != nil {
		return repoRow{}, err
	}
	r.HasToken = len(r.tokenEnc) > 0
	r.WebhookURL = s.webhookURL(r.publicID)
	r.PublicCountURL = s.BaseURL + "/api/github/stars/" + r.Repo.Repo
	return r, nil
}

func (s *Service) load(ctx context.Context, id int64) (repoRow, error) {
	return s.scan(s.pool.QueryRow(ctx, `select `+repoColumns+` from github_star_repos where id=$1`, id))
}

// Get returns one repository.
func (s *Service) Get(ctx context.Context, id int64) (Repo, error) {
	r, err := s.load(ctx, id)
	return r.Repo, err
}

// List returns every tracked repository, oldest first.
func (s *Service) List(ctx context.Context) ([]Repo, error) {
	rows, err := s.pool.Query(ctx, `select `+repoColumns+` from github_star_repos order by id`)
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

func (s *Service) audit(ctx context.Context, tx pgx.Tx, actor int64, action string, id int64, repo string) error {
	meta, _ := json.Marshal(map[string]any{"repo": repo})
	_, err := tx.Exec(ctx, `
		insert into audit_log(org_id,actor_user_id,action,target_type,target_id,metadata)
		values (null,nullif($1::bigint,0),$2,'github_star_repo',$3,$4)`, actor, action, id, meta)
	if err != nil {
		return fmt.Errorf("audit %s: %w", action, err)
	}
	return nil
}

// Create starts tracking a repository and returns it with the new webhook
// secret, which is not retrievable afterwards. It checks that the repository
// exists with one GitHub call; a GitHub outage or rate limit does not block
// the save.
func (s *Service) Create(ctx context.Context, in CreateInput, actor int64) (Repo, string, error) {
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
		var count int
		if err := tx.QueryRow(ctx, `select count(*) from github_star_repos`).Scan(&count); err != nil {
			return err
		}
		if count >= MaxRepos {
			return ErrLimit
		}
		row, err := s.scan(tx.QueryRow(ctx, `
			insert into github_star_repos(public_id,repo,timezone,webhook_secret_enc,token_enc)
			values ($1,$2,$3,$4,$5) returning `+repoColumns, publicID, repo, zone, s.seal(secret), tokenEnc))
		if err != nil {
			return err
		}
		out = row
		return s.audit(ctx, tx, actor, "instance.github_stars.repo_added", row.ID, repo)
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
func (s *Service) Update(ctx context.Context, id int64, in UpdateInput, actor int64) (Repo, error) {
	if in.TimeZone != nil {
		if _, err := validZone(strings.TrimSpace(*in.TimeZone)); err != nil {
			return Repo{}, err
		}
	}
	var out repoRow
	err := pgx.BeginFunc(ctx, s.pool, func(tx pgx.Tx) error {
		cur, err := s.scan(tx.QueryRow(ctx, `select `+repoColumns+` from github_star_repos where id=$1 for update`, id))
		if err != nil {
			return err
		}
		enabled, zone, tokenEnc := cur.Enabled, cur.TimeZone, cur.tokenEnc
		if in.Enabled != nil {
			enabled = *in.Enabled
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
			update github_star_repos set enabled=$2, timezone=$3, token_enc=$4 where id=$1 returning `+repoColumns,
			id, enabled, zone, tokenEnc))
		if err != nil {
			return err
		}
		return s.audit(ctx, tx, actor, "instance.github_stars.repo_updated", id, out.Repo.Repo)
	})
	return out.Repo, err
}

// RotateSecret replaces the webhook secret and returns the new one, once.
func (s *Service) RotateSecret(ctx context.Context, id int64, actor int64) (Repo, string, error) {
	secret, err := newSecret()
	if err != nil {
		return Repo{}, "", err
	}
	var out repoRow
	err = pgx.BeginFunc(ctx, s.pool, func(tx pgx.Tx) error {
		var err error
		out, err = s.scan(tx.QueryRow(ctx, `update github_star_repos set webhook_secret_enc=$2 where id=$1 returning `+repoColumns, id, s.seal(secret)))
		if err != nil {
			return err
		}
		return s.audit(ctx, tx, actor, "instance.github_stars.secret_rotated", id, out.Repo.Repo)
	})
	if err != nil {
		return Repo{}, "", err
	}
	return out.Repo, secret, nil
}

// Delete removes a repository. With deleteData it also deletes its events,
// state, snapshots, annotations and erasure tombstones in the same
// transaction; without it the repository is only disabled and its history is
// kept. It reports whether the configuration was removed.
func (s *Service) Delete(ctx context.Context, id int64, deleteData bool, actor int64) (bool, error) {
	removed := false
	err := pgx.BeginFunc(ctx, s.pool, func(tx pgx.Tx) error {
		cur, err := s.scan(tx.QueryRow(ctx, `select `+repoColumns+` from github_star_repos where id=$1 for update`, id))
		if err != nil {
			return err
		}
		if !deleteData {
			if _, err := tx.Exec(ctx, `update github_star_repos set enabled=false where id=$1`, id); err != nil {
				return err
			}
			return s.audit(ctx, tx, actor, "instance.github_stars.repo_disabled", id, cur.Repo.Repo)
		}
		for _, table := range []string{"github_star_events", "github_star_state", "github_star_snapshots", "github_star_annotations", "github_star_erasures"} {
			if _, err := tx.Exec(ctx, `delete from `+table+` where repo=$1`, cur.Repo.Repo); err != nil {
				return fmt.Errorf("delete %s: %w", table, err)
			}
		}
		if _, err := tx.Exec(ctx, `delete from github_star_repos where id=$1`, id); err != nil {
			return err
		}
		removed = true
		return s.audit(ctx, tx, actor, "instance.github_stars.repo_removed", id, cur.Repo.Repo)
	})
	return removed, err
}

// StoreFor returns the data store of one repository, bucketed in its time zone.
func (s *Service) StoreFor(ctx context.Context, id int64) (*Store, Repo, error) {
	r, err := s.load(ctx, id)
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
	return NewStore(s.pool, r.Repo.Repo, loc)
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
	return s.scan(s.pool.QueryRow(ctx, `select `+repoColumns+` from github_star_repos where public_id=$1`, publicID))
}
