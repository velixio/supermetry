package integrations

import (
	"context"
	"crypto/rand"
	"crypto/sha256"
	"encoding/base64"
	"encoding/json"
	"errors"
	"fmt"
	"log/slog"
	"strings"
	"sync"
	"time"
	"unicode"
	"unicode/utf8"

	"github.com/jackc/pgx/v5"

	"supermetry/internal/platform/audit"
	"supermetry/internal/platform/authz"
	platformcrypto "supermetry/internal/platform/crypto"
	"supermetry/internal/platform/store"
	"supermetry/internal/scheduler"
)

// ErrDenied means the user declined access at the provider.
var ErrDenied = errors.New("authorization was denied at the provider")

const (
	stateTTL         = 10 * time.Minute
	rateWindow       = 10 * time.Minute
	startRateLimit   = 10
	backfillRateMax  = 5
	connectionsBatch = 50
	dataLagDays      = 2
	tokenSkew        = 2 * time.Minute
	syncEvery        = 30 * time.Minute
	idleRecheck      = 6 * time.Hour
	maxBackoff       = 6 * time.Hour
	baseBackoff      = 5 * time.Minute
	maxErrorBytes    = 200
	maxTopLimit      = 500
	maxTopRangeDays  = 366
)

// Service owns connections, OAuth state, the scheduled import and reads.
type Service struct {
	store     *store.Store
	box       *platformcrypto.SecretBox
	providers map[string]Provider
	baseURL   string
	now       func() time.Time

	mu    sync.Mutex
	sends map[string][]time.Time
}

// New returns a service. baseURL is the public origin used to build the strict
// redirect URI; without it no provider can start a connection.
func New(data *store.Store, box *platformcrypto.SecretBox, baseURL string, providers ...Provider) *Service {
	byID := make(map[string]Provider, len(providers))
	for _, provider := range providers {
		byID[provider.ID()] = provider
	}
	return &Service{
		store: data, box: box, providers: byID, baseURL: strings.TrimRight(baseURL, "/"),
		now: time.Now, sends: make(map[string][]time.Time),
	}
}

// RedirectURI is the only callback URI sent to a provider.
func (s *Service) RedirectURI(provider string) string {
	if s.baseURL == "" {
		return ""
	}
	return s.baseURL + "/api/private/integrations/" + provider + "/callback"
}

func (s *Service) manage(p authz.Principal, siteID int32) bool {
	return authz.Can(p, authz.ActionSiteEdit, siteID) && (p.Role == authz.RoleOwner || p.Role == authz.RoleAdmin)
}

func (s *Service) siteInOrg(ctx context.Context, q interface {
	QueryRow(context.Context, string, ...any) pgx.Row
}, orgID int64, siteID int32) error {
	var ok bool
	if err := q.QueryRow(ctx, `select exists(select 1 from sites where id=$1 and org_id=$2 and deleted_at is null)`, siteID, orgID).Scan(&ok); err != nil {
		return fmt.Errorf("check site: %w", err)
	}
	if !ok {
		return ErrNotFound
	}
	return nil
}

func (s *Service) provider(id string) (Provider, error) {
	provider, ok := s.providers[id]
	if !ok {
		return nil, ErrNotFound
	}
	if !provider.Configured() || s.baseURL == "" {
		return nil, ErrUnavailable
	}
	return provider, nil
}

func (s *Service) allow(key string, limit int) bool {
	s.mu.Lock()
	defer s.mu.Unlock()
	now := s.now()
	kept := s.sends[key][:0]
	for _, at := range s.sends[key] {
		if now.Sub(at) < rateWindow {
			kept = append(kept, at)
		}
	}
	if len(kept) >= limit {
		s.sends[key] = kept
		return false
	}
	s.sends[key] = append(kept, now)
	return true
}

// List returns the catalog of configured providers with each connection.
func (s *Service) List(ctx context.Context, p authz.Principal, siteID int32) ([]CatalogEntry, error) {
	if !authz.Can(p, authz.ActionSiteEdit, siteID) || p.Role == authz.RoleKey {
		return nil, ErrForbidden
	}
	if err := s.siteInOrg(ctx, s.store.Pool, p.OrgID, siteID); err != nil {
		return nil, err
	}
	rows, err := s.store.Pool.Query(ctx, `select id,provider,property,status,synced_through,last_sync_at,last_error,created_at from integration_connections where site_id=$1`, siteID)
	if err != nil {
		return nil, fmt.Errorf("list integration connections: %w", err)
	}
	defer rows.Close()
	connections := map[string]*Connection{}
	for rows.Next() {
		var c Connection
		var through *time.Time
		if err := rows.Scan(&c.ID, &c.Provider, &c.Property, &c.Status, &through, &c.LastSyncAt, &c.LastError, &c.CreatedAt); err != nil {
			return nil, fmt.Errorf("scan integration connection: %w", err)
		}
		if through != nil {
			day := through.Format(time.DateOnly)
			c.SyncedThrough = &day
		}
		connections[c.Provider] = &c
	}
	if err := rows.Err(); err != nil {
		return nil, fmt.Errorf("iterate integration connections: %w", err)
	}
	entries := make([]CatalogEntry, 0, len(s.providers))
	for _, provider := range s.providers {
		if !provider.Configured() {
			continue
		}
		entries = append(entries, CatalogEntry{Provider: provider.ID(), Name: provider.Name(), Available: s.baseURL != "", Connection: connections[provider.ID()]})
	}
	sortEntries(entries)
	return entries, nil
}

func sortEntries(entries []CatalogEntry) {
	for i := 1; i < len(entries); i++ {
		for j := i; j > 0 && entries[j].Provider < entries[j-1].Provider; j-- {
			entries[j], entries[j-1] = entries[j-1], entries[j]
		}
	}
}

func validProperty(property string) bool {
	if property == "" || len(property) > MaxPropertyBytes || !utf8.ValidString(property) {
		return false
	}
	for _, r := range property {
		if unicode.IsControl(r) || unicode.IsSpace(r) {
			return false
		}
	}
	return true
}

func randomToken() (string, error) {
	raw := make([]byte, 32)
	if _, err := rand.Read(raw); err != nil {
		return "", fmt.Errorf("generate random token: %w", err)
	}
	return base64.RawURLEncoding.EncodeToString(raw), nil
}

func stateHash(state string) []byte {
	sum := sha256.Sum256([]byte(state))
	return sum[:]
}

// Start records a one-time authorization state bound to the user, organization
// and site, and returns the provider URL the browser must visit.
func (s *Service) Start(ctx context.Context, p authz.Principal, siteID int32, in StartInput) (string, error) {
	if !s.manage(p, siteID) {
		return "", ErrForbidden
	}
	provider, err := s.provider(in.Provider)
	if err != nil {
		return "", err
	}
	in.Property = strings.TrimSpace(in.Property)
	if in.BackfillDays == 0 {
		in.BackfillDays = DefaultBackfillDays
	}
	if !validProperty(in.Property) || in.BackfillDays < 1 || in.BackfillDays > MaxBackfillDays {
		return "", ErrInvalid
	}
	if err := s.siteInOrg(ctx, s.store.Pool, p.OrgID, siteID); err != nil {
		return "", err
	}
	if !s.allow(fmt.Sprintf("start:%d", p.UserID), startRateLimit) {
		return "", ErrRateLimited
	}
	state, err := randomToken()
	if err != nil {
		return "", err
	}
	verifier, err := randomToken()
	if err != nil {
		return "", err
	}
	challenge := sha256.Sum256([]byte(verifier))
	now := s.now()
	err = s.store.Tx(ctx, func(tx pgx.Tx) error {
		if _, err := tx.Exec(ctx, `delete from integration_oauth_states where expires_at<=$1`, now); err != nil {
			return fmt.Errorf("purge expired states: %w", err)
		}
		_, err := tx.Exec(ctx, `insert into integration_oauth_states(state_hash,user_id,org_id,site_id,provider,property,backfill_days,code_verifier,expires_at) values ($1,$2,$3,$4,$5,$6,$7,$8,$9)`,
			stateHash(state), p.UserID, p.OrgID, siteID, provider.ID(), in.Property, in.BackfillDays, s.box.Seal([]byte(verifier)), now.Add(stateTTL))
		if err != nil {
			return fmt.Errorf("store authorization state: %w", err)
		}
		return nil
	})
	if err != nil {
		return "", err
	}
	return provider.AuthURL(AuthParams{
		State: state, RedirectURI: s.RedirectURI(provider.ID()),
		CodeChallenge: base64.RawURLEncoding.EncodeToString(challenge[:]),
	}), nil
}

func (s *Service) seal(tokens Tokens) ([]byte, error) {
	raw, err := json.Marshal(tokens)
	if err != nil {
		return nil, errors.New("encode credentials")
	}
	return s.box.Seal(raw), nil
}

func (s *Service) open(sealed []byte) (Tokens, error) {
	raw, err := s.box.Open(sealed)
	if err != nil {
		return Tokens{}, errors.New("decrypt credentials")
	}
	var tokens Tokens
	if err := json.Unmarshal(raw, &tokens); err != nil {
		return Tokens{}, errors.New("decode credentials")
	}
	return tokens, nil
}

// Callback completes the OAuth handshake. The state is consumed exactly once
// and must belong to the signed-in user and organization. It returns the site
// that was connected. providerError is the raw "error" query value, if any.
func (s *Service) Callback(ctx context.Context, p authz.Principal, providerID, code, state, providerError string) (int32, error) {
	if state == "" || len(state) > 128 {
		return 0, ErrInvalidState
	}
	var (
		userID, orgID int64
		siteID        int32
		provName      string
		property      string
		backfillDays  int
		sealedVerif   []byte
		expires       time.Time
	)
	err := s.store.Pool.QueryRow(ctx, `delete from integration_oauth_states where state_hash=$1 returning user_id,org_id,site_id,provider,property,backfill_days,code_verifier,expires_at`, stateHash(state)).
		Scan(&userID, &orgID, &siteID, &provName, &property, &backfillDays, &sealedVerif, &expires)
	if errors.Is(err, pgx.ErrNoRows) {
		return 0, ErrInvalidState
	}
	if err != nil {
		return 0, fmt.Errorf("consume authorization state: %w", err)
	}
	if !s.now().Before(expires) || userID != p.UserID || orgID != p.OrgID || provName != providerID {
		return 0, ErrInvalidState
	}
	if !s.manage(p, siteID) {
		return 0, ErrForbidden
	}
	if providerError != "" {
		return siteID, ErrDenied
	}
	provider, err := s.provider(provName)
	if err != nil {
		return 0, err
	}
	if code == "" || len(code) > 2048 {
		return 0, ErrInvalidState
	}
	verifier, err := s.box.Open(sealedVerif)
	if err != nil {
		return 0, errors.New("decrypt code verifier")
	}
	tokens, err := provider.Exchange(ctx, code, s.RedirectURI(provider.ID()), string(verifier))
	if err != nil {
		return siteID, fmt.Errorf("exchange authorization code: %w", err)
	}
	if tokens.RefreshToken == "" {
		return siteID, ErrNoRefreshToken
	}
	sealed, err := s.seal(tokens)
	if err != nil {
		return 0, err
	}
	now := s.now().UTC()
	from := dateOnly(now).AddDate(0, 0, -backfillDays)
	err = s.store.Tx(ctx, func(tx pgx.Tx) error {
		if err := s.siteInOrg(ctx, tx, p.OrgID, siteID); err != nil {
			return err
		}
		var oldProperty string
		err := tx.QueryRow(ctx, `select property from integration_connections where site_id=$1 and provider=$2 for update`, siteID, provName).Scan(&oldProperty)
		existing := err == nil
		if err != nil && !errors.Is(err, pgx.ErrNoRows) {
			return fmt.Errorf("load connection: %w", err)
		}
		if existing && oldProperty != property {
			if _, err := tx.Exec(ctx, `delete from search_console_rows where site_id=$1`, siteID); err != nil {
				return fmt.Errorf("clear data of previous property: %w", err)
			}
			if _, err := tx.Exec(ctx, `update integration_connections set synced_through=null where site_id=$1 and provider=$2`, siteID, provName); err != nil {
				return fmt.Errorf("reset sync cursor: %w", err)
			}
		}
		var id int64
		if err := tx.QueryRow(ctx, `insert into integration_connections(org_id,site_id,provider,property,status,credentials,connected_by,backfill_from,next_attempt_at)
			values ($1,$2,$3,$4,'active',$5,$6,$7,$8)
			on conflict (site_id,provider) do update set property=excluded.property,status='active',credentials=excluded.credentials,connected_by=excluded.connected_by,
				backfill_from=excluded.backfill_from,last_error='',failure_count=0,next_attempt_at=excluded.next_attempt_at,updated_at=excluded.next_attempt_at
			returning id`, p.OrgID, siteID, provName, property, sealed, p.UserID, from, now).Scan(&id); err != nil {
			return fmt.Errorf("save connection: %w", err)
		}
		return audit.Write(ctx, tx, p, "integration.connected", "integration_connection", id, map[string]any{"provider": provName, "site_id": siteID})
	})
	if err != nil {
		return 0, err
	}
	return siteID, nil
}

// Disconnect deletes the stored tokens and all imported data for the site.
func (s *Service) Disconnect(ctx context.Context, p authz.Principal, siteID int32, providerID string) error {
	if !s.manage(p, siteID) {
		return ErrForbidden
	}
	var sealed []byte
	err := s.store.Tx(ctx, func(tx pgx.Tx) error {
		var id int64
		err := tx.QueryRow(ctx, `select id,credentials from integration_connections where site_id=$1 and provider=$2 and org_id=$3 for update`, siteID, providerID, p.OrgID).Scan(&id, &sealed)
		if errors.Is(err, pgx.ErrNoRows) {
			return ErrNotFound
		}
		if err != nil {
			return fmt.Errorf("load connection: %w", err)
		}
		if _, err := tx.Exec(ctx, `delete from search_console_rows where site_id=$1`, siteID); err != nil {
			return fmt.Errorf("delete imported data: %w", err)
		}
		if _, err := tx.Exec(ctx, `delete from integration_connections where id=$1`, id); err != nil {
			return fmt.Errorf("delete connection: %w", err)
		}
		return audit.Write(ctx, tx, p, "integration.disconnected", "integration_connection", id, map[string]any{"provider": providerID, "site_id": siteID})
	})
	if err != nil {
		return err
	}
	if provider, ok := s.providers[providerID]; ok {
		if tokens, openErr := s.open(sealed); openErr == nil && tokens.RefreshToken != "" {
			revokeCtx, cancel := context.WithTimeout(context.WithoutCancel(ctx), 10*time.Second)
			defer cancel()
			_ = provider.Revoke(revokeCtx, tokens.RefreshToken)
		}
	}
	return nil
}

// Backfill re-imports the last days days, in bounded windows over later runs.
func (s *Service) Backfill(ctx context.Context, p authz.Principal, siteID int32, providerID string, days int) error {
	if !s.manage(p, siteID) {
		return ErrForbidden
	}
	if days < 1 || days > MaxBackfillDays {
		return ErrInvalid
	}
	if !s.allow(fmt.Sprintf("backfill:%d", p.UserID), backfillRateMax) {
		return ErrRateLimited
	}
	now := s.now().UTC()
	return s.store.Tx(ctx, func(tx pgx.Tx) error {
		var id int64
		err := tx.QueryRow(ctx, `update integration_connections set backfill_from=$4,synced_through=null,next_attempt_at=$5,updated_at=$5
			where site_id=$1 and provider=$2 and org_id=$3 and status='active' returning id`, siteID, providerID, p.OrgID, dateOnly(now).AddDate(0, 0, -days), now).Scan(&id)
		if errors.Is(err, pgx.ErrNoRows) {
			return ErrNotFound
		}
		if err != nil {
			return fmt.Errorf("request backfill: %w", err)
		}
		return audit.Write(ctx, tx, p, "integration.backfill_requested", "integration_connection", id, map[string]any{"provider": providerID, "days": days})
	})
}

// Top aggregates imported rows by query or page for an inclusive date range.
func (s *Service) Top(ctx context.Context, p authz.Principal, siteID int32, dimension string, from, to time.Time, limit int) (TopResult, error) {
	// Search queries are not part of public share links.
	if p.Role == authz.RoleShare || !authz.Can(p, authz.ActionStatsRead, siteID) {
		return TopResult{}, ErrForbidden
	}
	column := ""
	switch dimension {
	case "query":
		column = "query"
	case "page":
		column = "page"
	default:
		return TopResult{}, ErrInvalid
	}
	if limit == 0 {
		limit = 50
	}
	from, to = dateOnly(from), dateOnly(to)
	if limit < 1 || limit > maxTopLimit || to.Before(from) || to.Sub(from) > maxTopRangeDays*24*time.Hour {
		return TopResult{}, ErrInvalid
	}
	if err := s.siteInOrg(ctx, s.store.Pool, p.OrgID, siteID); err != nil {
		return TopResult{}, err
	}
	result := TopResult{Dimension: dimension, From: from.Format(time.DateOnly), To: to.Format(time.DateOnly), Rows: []TopRow{}}
	rows, err := s.store.Pool.Query(ctx, `select `+column+`,sum(clicks)::bigint,sum(impressions)::bigint,coalesce(sum(position*impressions)/nullif(sum(impressions),0),0)
		from search_console_rows where site_id=$1 and day between $2 and $3 group by 1 order by 2 desc,3 desc,1 limit $4`, siteID, from, to, limit)
	if err != nil {
		return TopResult{}, fmt.Errorf("query search console rows: %w", err)
	}
	defer rows.Close()
	for rows.Next() {
		var row TopRow
		if err := rows.Scan(&row.Key, &row.Clicks, &row.Impressions, &row.Position); err != nil {
			return TopResult{}, fmt.Errorf("scan search console row: %w", err)
		}
		row.CTR = ratio(row.Clicks, row.Impressions)
		result.Rows = append(result.Rows, row)
	}
	if err := rows.Err(); err != nil {
		return TopResult{}, fmt.Errorf("iterate search console rows: %w", err)
	}
	rows.Close()
	totals := &result.Totals
	if err := s.store.Pool.QueryRow(ctx, `select coalesce(sum(clicks),0)::bigint,coalesce(sum(impressions),0)::bigint,coalesce(sum(position*impressions)/nullif(sum(impressions),0),0)
		from search_console_rows where site_id=$1 and day between $2 and $3`, siteID, from, to).Scan(&totals.Clicks, &totals.Impressions, &totals.Position); err != nil {
		return TopResult{}, fmt.Errorf("total search console rows: %w", err)
	}
	totals.CTR = ratio(totals.Clicks, totals.Impressions)
	return result, nil
}

func ratio(clicks, impressions int64) float64 {
	if impressions == 0 {
		return 0
	}
	return float64(clicks) / float64(impressions)
}

func dateOnly(t time.Time) time.Time {
	y, m, d := t.UTC().Date()
	return time.Date(y, m, d, 0, 0, 0, 0, time.UTC)
}

// SyncJob imports due connections every half hour.
func (s *Service) SyncJob() scheduler.Job {
	return scheduler.Job{Name: "integrations-sync", Every: syncEvery, Run: s.Sync}
}

// RetentionJob prunes expired states and old or excess rows daily.
func (s *Service) RetentionJob() scheduler.Job {
	return scheduler.Job{Name: "integrations-retention", Every: 24 * time.Hour, Run: s.Retention}
}

type dueConnection struct {
	id            int64
	siteID        int32
	provider      string
	property      string
	credentials   []byte
	syncedThrough *time.Time
	backfillFrom  time.Time
	failures      int
}

// Sync imports every due connection. Failures are recorded on the connection
// (with backoff) and never abort the run.
func (s *Service) Sync(ctx context.Context) error {
	rows, err := s.store.Pool.Query(ctx, `select id,site_id,provider,property,credentials,synced_through,backfill_from,failure_count from integration_connections
		where status='active' and next_attempt_at<=$1 order by next_attempt_at limit $2`, s.now(), connectionsBatch)
	if err != nil {
		return fmt.Errorf("list due connections: %w", err)
	}
	var due []dueConnection
	for rows.Next() {
		var c dueConnection
		if err := rows.Scan(&c.id, &c.siteID, &c.provider, &c.property, &c.credentials, &c.syncedThrough, &c.backfillFrom, &c.failures); err != nil {
			rows.Close()
			return fmt.Errorf("scan due connection: %w", err)
		}
		due = append(due, c)
	}
	rows.Close()
	if err := rows.Err(); err != nil {
		return fmt.Errorf("iterate due connections: %w", err)
	}
	for _, c := range due {
		if ctx.Err() != nil {
			return nil
		}
		s.syncOne(ctx, c)
	}
	return nil
}

func (s *Service) syncOne(ctx context.Context, c dueConnection) {
	provider, ok := s.providers[c.provider]
	if !ok || !provider.Configured() {
		s.recordFailure(ctx, c, &ProviderError{Kind: KindTransient, Msg: "provider is not configured"})
		return
	}
	tokens, err := s.open(c.credentials)
	if err != nil {
		s.recordFailure(ctx, c, &ProviderError{Kind: KindAuth, Msg: "stored credentials are unreadable; reconnect"})
		return
	}
	now := s.now().UTC()
	if tokens.AccessToken == "" || !tokens.Expiry.After(now.Add(tokenSkew)) {
		refreshed, err := provider.Refresh(ctx, tokens.RefreshToken)
		if err != nil {
			s.recordFailure(ctx, c, err)
			return
		}
		if refreshed.RefreshToken == "" {
			refreshed.RefreshToken = tokens.RefreshToken
		}
		tokens = refreshed
		sealed, err := s.seal(tokens)
		if err != nil {
			s.recordFailure(ctx, c, err)
			return
		}
		if _, err := s.store.Pool.Exec(ctx, `update integration_connections set credentials=$2,updated_at=now() where id=$1`, c.id, sealed); err != nil {
			slog.WarnContext(ctx, "save refreshed integration credentials failed", "connection_id", c.id)
			return
		}
	}
	target := dateOnly(now).AddDate(0, 0, -dataLagDays)
	start := dateOnly(c.backfillFrom)
	if c.syncedThrough != nil {
		if resume := dateOnly(*c.syncedThrough).AddDate(0, 0, -(RefetchDays - 1)); resume.After(start) {
			start = resume
		}
	}
	end := start.AddDate(0, 0, MaxDaysPerRun-1)
	if end.After(target) {
		end = target
	}
	for day := start; !day.After(end); day = day.AddDate(0, 0, 1) {
		rows, err := provider.Fetch(ctx, tokens.AccessToken, c.property, day, MaxRowsPerDay)
		if err != nil {
			s.recordFailure(ctx, c, err)
			return
		}
		if err := s.replaceDay(ctx, c, day, rows); err != nil {
			if errors.Is(err, ErrNotFound) {
				return // disconnected while syncing
			}
			s.recordFailure(ctx, c, err)
			return
		}
	}
	next := now.Add(idleRecheck)
	if end.Before(target) {
		next = now // more backfill windows remain
	}
	if _, err := s.store.Pool.Exec(ctx, `update integration_connections set last_sync_at=$2,last_error='',failure_count=0,next_attempt_at=$3,updated_at=$2 where id=$1`, c.id, now, next); err != nil {
		slog.WarnContext(ctx, "record integration sync failed", "connection_id", c.id)
	}
}

type rowAggregate struct {
	Row
	positionSum float64
}

// normalize clamps, truncates and merges rows that share the same dimensions.
func normalize(day time.Time, rows []Row) map[[16]byte]*rowAggregate {
	merged := make(map[[16]byte]*rowAggregate, len(rows))
	for _, row := range rows {
		row.Query = truncateBytes(row.Query, MaxQueryBytes)
		row.Page = truncateBytes(row.Page, MaxPageBytes)
		row.Country = truncateBytes(strings.ToLower(row.Country), 8)
		row.Device = truncateBytes(strings.ToLower(row.Device), 16)
		if row.Clicks < 0 || row.Impressions < 0 || row.Clicks > row.Impressions && row.Impressions > 0 {
			continue
		}
		key := rowKey(row)
		agg, ok := merged[key]
		if !ok {
			row.Day = day
			agg = &rowAggregate{Row: row}
			agg.Clicks, agg.Impressions = 0, 0
			merged[key] = agg
		}
		agg.Clicks += row.Clicks
		agg.Impressions += row.Impressions
		agg.positionSum += row.Position * float64(row.Impressions)
	}
	return merged
}

func rowKey(row Row) [16]byte {
	sum := sha256.Sum256([]byte(row.Query + "\x00" + row.Page + "\x00" + row.Country + "\x00" + row.Device))
	var key [16]byte
	copy(key[:], sum[:16])
	return key
}

func truncateBytes(value string, limit int) string {
	value = strings.ToValidUTF8(strings.ReplaceAll(value, "\x00", ""), "")
	if len(value) <= limit {
		return value
	}
	value = value[:limit]
	for !utf8.ValidString(value) {
		value = value[:len(value)-1]
	}
	return value
}

// replaceDay swaps the stored rows of one day atomically, so a rerun never
// duplicates and rows that disappeared at the provider are removed.
func (s *Service) replaceDay(ctx context.Context, c dueConnection, day time.Time, rows []Row) error {
	merged := normalize(day, rows)
	return s.store.Tx(ctx, func(tx pgx.Tx) error {
		// Locking the connection serialises with Disconnect: once it is gone
		// nothing may be written back.
		var id int64
		if err := tx.QueryRow(ctx, `select id from integration_connections where id=$1 for share`, c.id).Scan(&id); err != nil {
			if errors.Is(err, pgx.ErrNoRows) {
				return ErrNotFound
			}
			return fmt.Errorf("lock connection: %w", err)
		}
		if _, err := tx.Exec(ctx, `delete from search_console_rows where site_id=$1 and day=$2`, c.siteID, day); err != nil {
			return fmt.Errorf("clear day: %w", err)
		}
		copyRows := make([][]any, 0, len(merged))
		for key, agg := range merged {
			position := 0.0
			if agg.Impressions > 0 {
				position = agg.positionSum / float64(agg.Impressions)
			}
			copyRows = append(copyRows, []any{c.siteID, day, key[:], agg.Query, agg.Page, agg.Country, agg.Device,
				int32(agg.Clicks), int32(agg.Impressions), float32(ratio(int64(agg.Clicks), int64(agg.Impressions))), float32(position)})
		}
		if _, err := tx.CopyFrom(ctx, pgx.Identifier{"search_console_rows"},
			[]string{"site_id", "day", "row_key", "query", "page", "country", "device", "clicks", "impressions", "ctr", "position"},
			pgx.CopyFromRows(copyRows)); err != nil {
			return fmt.Errorf("insert day: %w", err)
		}
		_, err := tx.Exec(ctx, `update integration_connections set synced_through=greatest(coalesce(synced_through,$2),$2) where id=$1`, c.id, day)
		return err
	})
}

func (s *Service) recordFailure(ctx context.Context, c dueConnection, cause error) {
	message := "sync failed; will retry"
	kind := KindTransient
	var providerErr *ProviderError
	if errors.As(cause, &providerErr) {
		kind, message = providerErr.Kind, truncateBytes(providerErr.Msg, maxErrorBytes)
	}
	now := s.now().UTC()
	if kind == KindAuth {
		_, err := s.store.Pool.Exec(ctx, `update integration_connections set status='reauth',last_error=$2,updated_at=$3 where id=$1`, c.id, message, now)
		if err != nil {
			slog.WarnContext(ctx, "record integration auth failure failed", "connection_id", c.id)
		}
		return
	}
	failures := c.failures + 1
	delay := baseBackoff
	for i := 1; i < failures && delay < maxBackoff; i++ {
		delay *= 2
	}
	delay = min(delay, maxBackoff)
	slog.WarnContext(ctx, "integration sync failed", "connection_id", c.id, "provider", c.provider, "failures", failures, "reason", message)
	if _, err := s.store.Pool.Exec(ctx, `update integration_connections set failure_count=$2,last_error=$3,next_attempt_at=$4,updated_at=$5 where id=$1`, c.id, failures, message, now.Add(delay), now); err != nil {
		slog.WarnContext(ctx, "record integration failure failed", "connection_id", c.id)
	}
}

// Retention drops expired states, rows older than the retention window and the
// oldest days of any site above the row cap.
func (s *Service) Retention(ctx context.Context) error {
	now := s.now().UTC()
	if _, err := s.store.Pool.Exec(ctx, `delete from integration_oauth_states where expires_at<=$1`, now); err != nil {
		return fmt.Errorf("purge states: %w", err)
	}
	if _, err := s.store.Pool.Exec(ctx, `delete from search_console_rows where day<$1`, dateOnly(now).AddDate(0, 0, -RetentionDays)); err != nil {
		return fmt.Errorf("purge old rows: %w", err)
	}
	rows, err := s.store.Pool.Query(ctx, `select site_id from search_console_rows group by site_id having count(*)>$1`, MaxRowsPerSite)
	if err != nil {
		return fmt.Errorf("find oversized sites: %w", err)
	}
	var oversized []int32
	for rows.Next() {
		var id int32
		if err := rows.Scan(&id); err != nil {
			rows.Close()
			return err
		}
		oversized = append(oversized, id)
	}
	rows.Close()
	for _, siteID := range oversized {
		for range MaxBackfillDays + RetentionDays {
			var count int64
			if err := s.store.Pool.QueryRow(ctx, `select count(*) from search_console_rows where site_id=$1`, siteID).Scan(&count); err != nil {
				return err
			}
			if count <= MaxRowsPerSite {
				break
			}
			if _, err := s.store.Pool.Exec(ctx, `delete from search_console_rows where site_id=$1 and day=(select min(day) from search_console_rows where site_id=$1)`, siteID); err != nil {
				return err
			}
		}
	}
	return nil
}
