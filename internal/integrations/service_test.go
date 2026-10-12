package integrations

import (
	"context"
	"crypto/sha256"
	"encoding/base64"
	"errors"
	"net/url"
	"strings"
	"sync"
	"testing"
	"time"

	"github.com/jackc/pgx/v5/pgxpool"

	"supermetry/internal/db"
	"supermetry/internal/platform/authz"
	platformcrypto "supermetry/internal/platform/crypto"
	"supermetry/internal/platform/store"
	"supermetry/internal/testpg"
)

var testNow = time.Date(2026, 10, 12, 9, 0, 0, 0, time.UTC)

// fakeProvider is an in-memory provider; no test touches the network.
type fakeProvider struct {
	mu          sync.Mutex
	configured  bool
	rows        map[string][]Row // keyed by YYYY-MM-DD
	fetchErr    error
	refreshErr  error
	challenge   string
	exchanged   Tokens
	fetched     []string
	refreshes   int
	revoked     []string
	tokensSeen  []string
	verifierBad bool
}

func newFake() *fakeProvider {
	return &fakeProvider{configured: true, rows: map[string][]Row{}, exchanged: Tokens{
		AccessToken: "access-secret-1", RefreshToken: "refresh-secret-1", Expiry: testNow.Add(time.Hour),
	}}
}

func (f *fakeProvider) ID() string       { return "search-console" }
func (f *fakeProvider) Name() string     { return "Fake Search Console" }
func (f *fakeProvider) Configured() bool { return f.configured }
func (f *fakeProvider) AuthURL(p AuthParams) string {
	f.mu.Lock()
	defer f.mu.Unlock()
	f.challenge = p.CodeChallenge
	return "https://accounts.example.test/auth?" + url.Values{"state": {p.State}, "redirect_uri": {p.RedirectURI}, "code_challenge": {p.CodeChallenge}}.Encode()
}
func (f *fakeProvider) Exchange(_ context.Context, code, _, verifier string) (Tokens, error) {
	f.mu.Lock()
	defer f.mu.Unlock()
	sum := sha256.Sum256([]byte(verifier))
	if base64.RawURLEncoding.EncodeToString(sum[:]) != f.challenge {
		f.verifierBad = true
		return Tokens{}, &ProviderError{Kind: KindAuth, Msg: "pkce mismatch"}
	}
	if code == "bad" {
		return Tokens{}, &ProviderError{Kind: KindAuth, Msg: "bad code"}
	}
	return f.exchanged, nil
}
func (f *fakeProvider) Refresh(context.Context, string) (Tokens, error) {
	f.mu.Lock()
	defer f.mu.Unlock()
	f.refreshes++
	if f.refreshErr != nil {
		return Tokens{}, f.refreshErr
	}
	return Tokens{AccessToken: "access-secret-2", Expiry: testNow.Add(time.Hour)}, nil
}
func (f *fakeProvider) Fetch(_ context.Context, access, _ string, day time.Time, _ int) ([]Row, error) {
	f.mu.Lock()
	defer f.mu.Unlock()
	f.tokensSeen = append(f.tokensSeen, access)
	if f.fetchErr != nil {
		return nil, f.fetchErr
	}
	key := day.Format(time.DateOnly)
	f.fetched = append(f.fetched, key)
	return append([]Row(nil), f.rows[key]...), nil
}
func (f *fakeProvider) Revoke(_ context.Context, token string) error {
	f.mu.Lock()
	defer f.mu.Unlock()
	f.revoked = append(f.revoked, token)
	return nil
}

type fixture struct {
	svc      *Service
	pool     *pgxpool.Pool
	fake     *fakeProvider
	owner    authz.Principal
	editor   authz.Principal
	viewer   authz.Principal
	siteID   int32
	otherOrg authz.Principal
	box      *platformcrypto.SecretBox
}

func newFixture(t *testing.T) *fixture {
	t.Helper()
	pool, _ := testpg.New(t)
	ctx := context.Background()
	if err := db.Migrate(ctx, pool); err != nil {
		t.Fatal(err)
	}
	var userID, otherUser, orgID, otherOrgID int64
	for _, q := range []struct {
		sql  string
		dest *int64
	}{
		{`insert into users(email,name,password_hash) values ('owner@example.test','Owner','h') returning id`, &userID},
		{`insert into users(email,name,password_hash) values ('other@example.test','Other','h') returning id`, &otherUser},
		{`insert into orgs(name,slug) values ('Example','example') returning id`, &orgID},
		{`insert into orgs(name,slug) values ('Other','other') returning id`, &otherOrgID},
	} {
		if err := pool.QueryRow(ctx, q.sql).Scan(q.dest); err != nil {
			t.Fatal(err)
		}
	}
	var siteID int32
	if err := pool.QueryRow(ctx, `insert into sites(org_id,public_id,name,timezone,retention_class) values ($1,'siteabcdefgh','Site','UTC',30) returning id`, orgID).Scan(&siteID); err != nil {
		t.Fatal(err)
	}
	box, err := platformcrypto.NewSecretBox([]byte("01234567890123456789012345678901"), "supermetry/integrations/v1")
	if err != nil {
		t.Fatal(err)
	}
	fake := newFake()
	svc := New(&store.Store{Pool: pool}, box, "https://app.example.test", fake)
	svc.now = func() time.Time { return testNow }
	return &fixture{
		svc: svc, pool: pool, fake: fake, siteID: siteID, box: box,
		owner:    authz.Principal{UserID: userID, OrgID: orgID, Role: authz.RoleOwner},
		editor:   authz.Principal{UserID: userID + 100, OrgID: orgID, Role: authz.RoleEditor},
		viewer:   authz.Principal{UserID: userID + 200, OrgID: orgID, Role: authz.RoleViewer},
		otherOrg: authz.Principal{UserID: otherUser, OrgID: otherOrgID, Role: authz.RoleOwner},
	}
}

func stateFrom(t *testing.T, authURL string) string {
	t.Helper()
	parsed, err := url.Parse(authURL)
	if err != nil || parsed.Query().Get("state") == "" {
		t.Fatalf("authorization URL has no state: %q", authURL)
	}
	return parsed.Query().Get("state")
}

// connect runs the whole handshake for the owner.
func (f *fixture) connect(t *testing.T, property string, days int) {
	t.Helper()
	ctx := context.Background()
	target, err := f.svc.Start(ctx, f.owner, f.siteID, StartInput{Provider: "search-console", Property: property, BackfillDays: days})
	if err != nil {
		t.Fatal(err)
	}
	if _, err := f.svc.Callback(ctx, f.owner, "search-console", "code-1", stateFrom(t, target), ""); err != nil {
		t.Fatal(err)
	}
}

func (f *fixture) rowCount(t *testing.T) int {
	t.Helper()
	var n int
	if err := f.pool.QueryRow(context.Background(), `select count(*) from search_console_rows where site_id=$1`, f.siteID).Scan(&n); err != nil {
		t.Fatal(err)
	}
	return n
}

func day(s string) time.Time {
	d, err := time.Parse(time.DateOnly, s)
	if err != nil {
		panic(err)
	}
	return d
}

func TestConnectEncryptsCredentialsBindsStateAndAudits(t *testing.T) {
	f := newFixture(t)
	ctx := context.Background()
	target, err := f.svc.Start(ctx, f.owner, f.siteID, StartInput{Provider: "search-console", Property: "sc-domain:example.test"})
	if err != nil {
		t.Fatal(err)
	}
	parsed, _ := url.Parse(target)
	if got := parsed.Query().Get("redirect_uri"); got != "https://app.example.test/api/private/integrations/search-console/callback" {
		t.Fatalf("redirect_uri = %q", got)
	}
	state := stateFrom(t, target)
	var stored int
	if err := f.pool.QueryRow(ctx, `select count(*) from integration_oauth_states where position($1 in encode(state_hash,'escape'))>0`, state).Scan(&stored); err != nil || stored != 0 {
		t.Fatalf("raw state must not be stored: %d %v", stored, err)
	}

	// A different user in the same organization cannot complete someone else's state.
	if _, err := f.svc.Callback(ctx, authz.Principal{UserID: f.owner.UserID + 1, OrgID: f.owner.OrgID, Role: authz.RoleOwner}, "search-console", "code-1", state, ""); !errors.Is(err, ErrInvalidState) {
		t.Fatalf("foreign user callback err = %v, want ErrInvalidState", err)
	}
	// The state is gone after the failed attempt (one-time use).
	if _, err := f.svc.Callback(ctx, f.owner, "search-console", "code-1", state, ""); !errors.Is(err, ErrInvalidState) {
		t.Fatalf("reused state err = %v, want ErrInvalidState", err)
	}

	f.connect(t, "sc-domain:example.test", 10)
	if f.fake.verifierBad {
		t.Fatal("PKCE verifier did not match the challenge")
	}
	var sealed []byte
	var status string
	if err := f.pool.QueryRow(ctx, `select credentials,status from integration_connections where site_id=$1`, f.siteID).Scan(&sealed, &status); err != nil {
		t.Fatal(err)
	}
	if status != "active" || strings.Contains(string(sealed), "refresh-secret-1") || strings.Contains(string(sealed), "access-secret-1") {
		t.Fatalf("credentials stored in plaintext or wrong status %q", status)
	}
	tokens, err := f.svc.open(sealed)
	if err != nil || tokens.RefreshToken != "refresh-secret-1" {
		t.Fatalf("open credentials = %+v, %v", tokens, err)
	}
	var audits int
	if err := f.pool.QueryRow(ctx, `select count(*) from audit_log where org_id=$1 and action='integration.connected' and metadata::text not like '%secret%'`, f.owner.OrgID).Scan(&audits); err != nil || audits != 1 {
		t.Fatalf("connect audit entries = %d, %v", audits, err)
	}
}

func TestExpiredStateAndMissingRefreshTokenAreRejected(t *testing.T) {
	f := newFixture(t)
	ctx := context.Background()
	target, err := f.svc.Start(ctx, f.owner, f.siteID, StartInput{Provider: "search-console", Property: "sc-domain:example.test"})
	if err != nil {
		t.Fatal(err)
	}
	f.svc.now = func() time.Time { return testNow.Add(stateTTL + time.Second) }
	if _, err := f.svc.Callback(ctx, f.owner, "search-console", "code-1", stateFrom(t, target), ""); !errors.Is(err, ErrInvalidState) {
		t.Fatalf("expired state err = %v", err)
	}
	f.svc.now = func() time.Time { return testNow }
	f.fake.exchanged.RefreshToken = ""
	target, _ = f.svc.Start(ctx, f.owner, f.siteID, StartInput{Provider: "search-console", Property: "sc-domain:example.test"})
	if _, err := f.svc.Callback(ctx, f.owner, "search-console", "code-1", stateFrom(t, target), ""); !errors.Is(err, ErrNoRefreshToken) {
		t.Fatalf("missing refresh token err = %v", err)
	}
	if n := f.rowsIn(t, "integration_connections"); n != 0 {
		t.Fatalf("connection created without refresh token: %d", n)
	}
	// A denied authorization consumes the state and reports the denial.
	target, _ = f.svc.Start(ctx, f.owner, f.siteID, StartInput{Provider: "search-console", Property: "sc-domain:example.test"})
	if _, err := f.svc.Callback(ctx, f.owner, "search-console", "", stateFrom(t, target), "access_denied"); !errors.Is(err, ErrDenied) {
		t.Fatalf("denied err = %v", err)
	}
}

func (f *fixture) rowsIn(t *testing.T, table string) int {
	t.Helper()
	var n int
	if err := f.pool.QueryRow(context.Background(), `select count(*) from `+table).Scan(&n); err != nil {
		t.Fatal(err)
	}
	return n
}

func TestAuthorizationMatrix(t *testing.T) {
	f := newFixture(t)
	ctx := context.Background()
	f.connect(t, "sc-domain:example.test", 5)
	input := StartInput{Provider: "search-console", Property: "sc-domain:example.test"}
	restricted := f.owner
	restricted.Role = authz.RoleEditor
	restricted.SiteIDs = []int32{f.siteID + 999}
	keyPrincipal := authz.Principal{UserID: 0, KeyID: 5, OrgID: f.owner.OrgID, Role: authz.RoleKey, Scopes: []string{"sites:write", "stats:read"}}

	tests := []struct {
		name string
		call func(authz.Principal) error
		who  authz.Principal
		want error
	}{
		{"viewer cannot start", func(p authz.Principal) error { _, err := f.svc.Start(ctx, p, f.siteID, input); return err }, f.viewer, ErrForbidden},
		{"editor cannot start", func(p authz.Principal) error { _, err := f.svc.Start(ctx, p, f.siteID, input); return err }, f.editor, ErrForbidden},
		{"api key cannot start", func(p authz.Principal) error { _, err := f.svc.Start(ctx, p, f.siteID, input); return err }, keyPrincipal, ErrForbidden},
		{"other org cannot see the site", func(p authz.Principal) error { _, err := f.svc.Start(ctx, p, f.siteID, input); return err }, f.otherOrg, ErrNotFound},
		{"viewer cannot disconnect", func(p authz.Principal) error { return f.svc.Disconnect(ctx, p, f.siteID, "search-console") }, f.viewer, ErrForbidden},
		{"editor cannot disconnect", func(p authz.Principal) error { return f.svc.Disconnect(ctx, p, f.siteID, "search-console") }, f.editor, ErrForbidden},
		{"other org cannot disconnect", func(p authz.Principal) error { return f.svc.Disconnect(ctx, p, f.siteID, "search-console") }, f.otherOrg, ErrNotFound},
		{"viewer cannot backfill", func(p authz.Principal) error { return f.svc.Backfill(ctx, p, f.siteID, "search-console", 5) }, f.viewer, ErrForbidden},
		{"viewer cannot list integrations", func(p authz.Principal) error { _, err := f.svc.List(ctx, p, f.siteID); return err }, f.viewer, ErrForbidden},
		{"scoped editor cannot list", func(p authz.Principal) error { _, err := f.svc.List(ctx, p, f.siteID); return err }, restricted, ErrForbidden},
		{"other org list is hidden", func(p authz.Principal) error { _, err := f.svc.List(ctx, p, f.siteID); return err }, f.otherOrg, ErrNotFound},
		{"share link cannot read search data", func(p authz.Principal) error {
			_, err := f.svc.Top(ctx, p, f.siteID, "query", day("2026-10-01"), day("2026-10-10"), 10)
			return err
		}, authz.Principal{OrgID: f.owner.OrgID, Role: authz.RoleShare, ShareID: 1, SiteIDs: []int32{f.siteID}}, ErrForbidden},
		{"other org cannot read search data", func(p authz.Principal) error {
			_, err := f.svc.Top(ctx, p, f.siteID, "query", day("2026-10-01"), day("2026-10-10"), 10)
			return err
		}, f.otherOrg, ErrNotFound},
	}
	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			if err := tt.call(tt.who); !errors.Is(err, tt.want) {
				t.Fatalf("err = %v, want %v", err, tt.want)
			}
		})
	}
	// Allowed roles.
	if _, err := f.svc.List(ctx, f.editor, f.siteID); err != nil {
		t.Fatalf("editor list: %v", err)
	}
	if _, err := f.svc.Top(ctx, f.viewer, f.siteID, "query", day("2026-10-01"), day("2026-10-10"), 10); err != nil {
		t.Fatalf("viewer top: %v", err)
	}
	// A callback with a state owned by another site/org context is rejected for non-managers.
	target, _ := f.svc.Start(ctx, f.owner, f.siteID, input)
	demoted := f.owner
	demoted.Role = authz.RoleViewer
	if _, err := f.svc.Callback(ctx, demoted, "search-console", "code-1", stateFrom(t, target), ""); !errors.Is(err, ErrForbidden) {
		t.Fatalf("demoted callback err = %v", err)
	}
}

func TestUnconfiguredProviderIsHiddenAndRejected(t *testing.T) {
	f := newFixture(t)
	f.fake.configured = false
	entries, err := f.svc.List(context.Background(), f.owner, f.siteID)
	if err != nil || len(entries) != 0 {
		t.Fatalf("List() = %+v, %v; want empty", entries, err)
	}
	_, err = f.svc.Start(context.Background(), f.owner, f.siteID, StartInput{Provider: "search-console", Property: "sc-domain:example.test"})
	if !errors.Is(err, ErrUnavailable) {
		t.Fatalf("Start err = %v", err)
	}
	if _, err = f.svc.Start(context.Background(), f.owner, f.siteID, StartInput{Provider: "nope", Property: "x"}); !errors.Is(err, ErrNotFound) {
		t.Fatalf("unknown provider err = %v", err)
	}
}

func TestStartValidatesInputAndRateLimits(t *testing.T) {
	f := newFixture(t)
	ctx := context.Background()
	for _, in := range []StartInput{
		{Provider: "search-console", Property: ""},
		{Provider: "search-console", Property: "has space"},
		{Provider: "search-console", Property: strings.Repeat("a", MaxPropertyBytes+1)},
		{Provider: "search-console", Property: "sc-domain:x", BackfillDays: MaxBackfillDays + 1},
		{Provider: "search-console", Property: "sc-domain:x", BackfillDays: -1},
	} {
		if _, err := f.svc.Start(ctx, f.owner, f.siteID, in); !errors.Is(err, ErrInvalid) {
			t.Fatalf("Start(%+v) err = %v, want ErrInvalid", in, err)
		}
	}
	for i := range startRateLimit {
		if _, err := f.svc.Start(ctx, f.owner, f.siteID, StartInput{Provider: "search-console", Property: "sc-domain:x"}); err != nil {
			t.Fatalf("start %d: %v", i, err)
		}
	}
	if _, err := f.svc.Start(ctx, f.owner, f.siteID, StartInput{Provider: "search-console", Property: "sc-domain:x"}); !errors.Is(err, ErrRateLimited) {
		t.Fatalf("err = %v, want ErrRateLimited", err)
	}
}

func TestSyncBackfillIsBoundedAndRerunsReplaceRows(t *testing.T) {
	f := newFixture(t)
	ctx := context.Background()
	// Seed one row per day for the whole window.
	for d := day("2026-10-02"); !d.After(day("2026-10-10")); d = d.AddDate(0, 0, 1) {
		f.fake.rows[d.Format(time.DateOnly)] = []Row{{Query: "q", Page: "/p", Country: "USA", Device: "DESKTOP", Clicks: 2, Impressions: 20, Position: 3}}
	}
	f.connect(t, "sc-domain:example.test", 10) // backfill_from = 2026-10-02

	if err := f.svc.Sync(ctx); err != nil {
		t.Fatal(err)
	}
	if got := len(f.fake.fetched); got != MaxDaysPerRun {
		t.Fatalf("first run fetched %d days, want the bounded window of %d: %v", got, MaxDaysPerRun, f.fake.fetched)
	}
	if f.fake.fetched[0] != "2026-10-02" || f.fake.fetched[MaxDaysPerRun-1] != "2026-10-08" {
		t.Fatalf("first window = %v", f.fake.fetched)
	}
	if n := f.rowCount(t); n != MaxDaysPerRun {
		t.Fatalf("rows after first run = %d, want %d", n, MaxDaysPerRun)
	}

	// Second run continues with the recent re-fetch overlap and reaches the data lag boundary (10-10).
	f.fake.fetched = nil
	if err := f.svc.Sync(ctx); err != nil {
		t.Fatal(err)
	}
	if strings.Join(f.fake.fetched, ",") != "2026-10-06,2026-10-07,2026-10-08,2026-10-09,2026-10-10" {
		t.Fatalf("second window = %v", f.fake.fetched)
	}
	if n := f.rowCount(t); n != 9 {
		t.Fatalf("rows after second run = %d, want 9 (reruns must not duplicate)", n)
	}

	// Caught up: nothing is due for six hours.
	f.fake.fetched = nil
	if err := f.svc.Sync(ctx); err != nil {
		t.Fatal(err)
	}
	if len(f.fake.fetched) != 0 {
		t.Fatalf("caught-up connection fetched %v", f.fake.fetched)
	}

	// Later, the provider revises recent days: a rerun replaces, including removed rows.
	f.fake.rows["2026-10-09"] = []Row{
		{Query: "q", Page: "/p", Country: "USA", Device: "DESKTOP", Clicks: 4, Impressions: 40, Position: 1},
		{Query: "new", Page: "/n", Country: "DEU", Device: "MOBILE", Clicks: 1, Impressions: 5, Position: 9},
	}
	f.fake.rows["2026-10-10"] = nil
	f.svc.now = func() time.Time { return testNow.Add(idleRecheck + time.Minute) }
	if err := f.svc.Sync(ctx); err != nil {
		t.Fatal(err)
	}
	var clicks, count int
	if err := f.pool.QueryRow(ctx, `select coalesce(sum(clicks),0),count(*) from search_console_rows where site_id=$1 and day='2026-10-09'`, f.siteID).Scan(&clicks, &count); err != nil || clicks != 5 || count != 2 {
		t.Fatalf("day 10-09 clicks=%d rows=%d err=%v", clicks, count, err)
	}
	if err := f.pool.QueryRow(ctx, `select count(*) from search_console_rows where site_id=$1 and day='2026-10-10'`, f.siteID).Scan(&count); err != nil || count != 0 {
		t.Fatalf("removed day rows = %d", count)
	}
}

func TestSyncMergesDuplicatesAndClampsFields(t *testing.T) {
	f := newFixture(t)
	f.fake.rows["2026-10-10"] = []Row{
		{Query: "Dup", Page: "/p", Country: "USA", Device: "DESKTOP", Clicks: 1, Impressions: 10, Position: 2},
		{Query: "Dup", Page: "/p", Country: "usa", Device: "desktop", Clicks: 3, Impressions: 30, Position: 4},
		{Query: strings.Repeat("é", 2000), Page: "/long", Country: "USA", Device: "DESKTOP", Clicks: 0, Impressions: 1, Position: 1},
		{Query: "bad", Page: "/bad", Country: "USA", Device: "DESKTOP", Clicks: 9, Impressions: 3, Position: 1}, // clicks > impressions
	}
	f.connect(t, "sc-domain:example.test", 3)
	if err := f.svc.Sync(context.Background()); err != nil {
		t.Fatal(err)
	}
	var clicks, impressions int
	var position float64
	if err := f.pool.QueryRow(context.Background(), `select clicks,impressions,position from search_console_rows where site_id=$1 and query='Dup'`, f.siteID).Scan(&clicks, &impressions, &position); err != nil {
		t.Fatal(err)
	}
	if clicks != 4 || impressions != 40 || position != 3.5 { // (2*10+4*30)/40
		t.Fatalf("merged = %d/%d/%v", clicks, impressions, position)
	}
	var maxLen int
	if err := f.pool.QueryRow(context.Background(), `select max(octet_length(query)) from search_console_rows where site_id=$1`, f.siteID).Scan(&maxLen); err != nil || maxLen > MaxQueryBytes {
		t.Fatalf("query length %d err=%v", maxLen, err)
	}
	if n := f.rowCount(t); n != 2 {
		t.Fatalf("rows = %d, want 2 (invalid row dropped)", n)
	}
}

func TestSyncRefreshesExpiredTokenAndNeverLogsOrStoresPlaintext(t *testing.T) {
	f := newFixture(t)
	ctx := context.Background()
	f.fake.exchanged.Expiry = testNow.Add(-time.Hour) // already expired
	f.connect(t, "sc-domain:example.test", 3)
	if err := f.svc.Sync(ctx); err != nil {
		t.Fatal(err)
	}
	if f.fake.refreshes != 1 {
		t.Fatalf("refreshes = %d, want 1", f.fake.refreshes)
	}
	for _, seen := range f.fake.tokensSeen {
		if seen != "access-secret-2" {
			t.Fatalf("fetch used token %q, want the refreshed one", seen)
		}
	}
	var sealed []byte
	if err := f.pool.QueryRow(ctx, `select credentials from integration_connections where site_id=$1`, f.siteID).Scan(&sealed); err != nil {
		t.Fatal(err)
	}
	tokens, err := f.svc.open(sealed)
	if err != nil || tokens.AccessToken != "access-secret-2" || tokens.RefreshToken != "refresh-secret-1" {
		t.Fatalf("stored tokens = %+v, %v (refresh token must be kept when the provider omits it)", tokens, err)
	}
}

func TestSyncFailureBacksOffAndAuthFailureNeedsReconnect(t *testing.T) {
	f := newFixture(t)
	ctx := context.Background()
	f.connect(t, "sc-domain:example.test", 3)
	f.fake.fetchErr = &ProviderError{Kind: KindTransient, Msg: "provider quota exceeded"}
	if err := f.svc.Sync(ctx); err != nil {
		t.Fatal(err)
	}
	var failures int
	var lastErr string
	var next time.Time
	read := func() {
		if err := f.pool.QueryRow(ctx, `select failure_count,last_error,next_attempt_at from integration_connections where site_id=$1`, f.siteID).Scan(&failures, &lastErr, &next); err != nil {
			t.Fatal(err)
		}
	}
	read()
	if failures != 1 || lastErr != "provider quota exceeded" || !next.Equal(testNow.Add(baseBackoff)) {
		t.Fatalf("after 1 failure: %d %q %v", failures, lastErr, next)
	}
	// Not due again until the backoff passes.
	f.fake.fetchErr = nil
	if err := f.svc.Sync(ctx); err != nil || len(f.fake.fetched) != 0 {
		t.Fatalf("backed-off connection was retried early: %v %v", err, f.fake.fetched)
	}
	// Second failure doubles the delay.
	f.svc.now = func() time.Time { return testNow.Add(baseBackoff + time.Second) }
	f.fake.fetchErr = &ProviderError{Kind: KindTransient, Msg: "provider returned status 503"}
	if err := f.svc.Sync(ctx); err != nil {
		t.Fatal(err)
	}
	read()
	if failures != 2 || !next.Equal(testNow.Add(baseBackoff+time.Second+2*baseBackoff)) {
		t.Fatalf("after 2 failures: %d %v", failures, next)
	}
	// Auth failure parks the connection.
	f.svc.now = func() time.Time { return testNow.Add(time.Hour) }
	f.fake.fetchErr = &ProviderError{Kind: KindAuth, Msg: "access to the property was lost; reconnect"}
	if err := f.svc.Sync(ctx); err != nil {
		t.Fatal(err)
	}
	var status string
	if err := f.pool.QueryRow(ctx, `select status,last_error from integration_connections where site_id=$1`, f.siteID).Scan(&status, &lastErr); err != nil || status != "reauth" || !strings.Contains(lastErr, "reconnect") {
		t.Fatalf("status=%q err=%q %v", status, lastErr, err)
	}
	f.fake.fetchErr = nil
	f.fake.fetched = nil
	f.svc.now = func() time.Time { return testNow.Add(48 * time.Hour) }
	if err := f.svc.Sync(ctx); err != nil || len(f.fake.fetched) != 0 {
		t.Fatalf("reauth connection must not sync: %v %v", err, f.fake.fetched)
	}
}

func TestDisconnectDeletesTokensAndImportedData(t *testing.T) {
	f := newFixture(t)
	ctx := context.Background()
	f.fake.rows["2026-10-10"] = []Row{{Query: "q", Page: "/p", Country: "USA", Device: "DESKTOP", Clicks: 1, Impressions: 2, Position: 1}}
	f.connect(t, "sc-domain:example.test", 3)
	if err := f.svc.Sync(ctx); err != nil {
		t.Fatal(err)
	}
	if f.rowCount(t) == 0 {
		t.Fatal("no data imported")
	}
	if err := f.svc.Disconnect(ctx, f.owner, f.siteID, "search-console"); err != nil {
		t.Fatal(err)
	}
	if n := f.rowsIn(t, "integration_connections"); n != 0 {
		t.Fatalf("connections left: %d", n)
	}
	if n := f.rowCount(t); n != 0 {
		t.Fatalf("imported rows left: %d", n)
	}
	if len(f.fake.revoked) != 1 || f.fake.revoked[0] != "refresh-secret-1" {
		t.Fatalf("revoked = %v", f.fake.revoked)
	}
	var audits int
	if err := f.pool.QueryRow(ctx, `select count(*) from audit_log where org_id=$1 and action='integration.disconnected'`, f.owner.OrgID).Scan(&audits); err != nil || audits != 1 {
		t.Fatalf("disconnect audits = %d, %v", audits, err)
	}
	if err := f.svc.Disconnect(ctx, f.owner, f.siteID, "search-console"); !errors.Is(err, ErrNotFound) {
		t.Fatalf("second disconnect err = %v", err)
	}
	// A sync that raced with the disconnect cannot write data back.
	if err := f.svc.replaceDay(ctx, dueConnection{id: 1, siteID: f.siteID}, day("2026-10-10"), f.fake.rows["2026-10-10"]); !errors.Is(err, ErrNotFound) {
		t.Fatalf("replaceDay after disconnect err = %v", err)
	}
	if n := f.rowCount(t); n != 0 {
		t.Fatalf("data written after disconnect: %d", n)
	}
}

func TestReconnectWithNewPropertyClearsPreviousData(t *testing.T) {
	f := newFixture(t)
	f.fake.rows["2026-10-10"] = []Row{{Query: "q", Page: "/p", Country: "USA", Device: "DESKTOP", Clicks: 1, Impressions: 2, Position: 1}}
	f.connect(t, "sc-domain:one.test", 3)
	if err := f.svc.Sync(context.Background()); err != nil {
		t.Fatal(err)
	}
	f.connect(t, "sc-domain:two.test", 3)
	if n := f.rowCount(t); n != 0 {
		t.Fatalf("previous property data survived reconnect: %d", n)
	}
	if n := f.rowsIn(t, "integration_connections"); n != 1 {
		t.Fatalf("connections = %d, want 1", n)
	}
}

func TestBackfillRequestResetsCursorAndAudits(t *testing.T) {
	f := newFixture(t)
	ctx := context.Background()
	f.connect(t, "sc-domain:example.test", 3)
	if err := f.svc.Sync(ctx); err != nil {
		t.Fatal(err)
	}
	if err := f.svc.Backfill(ctx, f.owner, f.siteID, "search-console", 0); !errors.Is(err, ErrInvalid) {
		t.Fatalf("zero days err = %v", err)
	}
	if err := f.svc.Backfill(ctx, f.owner, f.siteID, "search-console", MaxBackfillDays+1); !errors.Is(err, ErrInvalid) {
		t.Fatalf("too many days err = %v", err)
	}
	if err := f.svc.Backfill(ctx, f.owner, f.siteID, "search-console", 60); err != nil {
		t.Fatal(err)
	}
	var through *time.Time
	var from time.Time
	if err := f.pool.QueryRow(ctx, `select synced_through,backfill_from from integration_connections where site_id=$1`, f.siteID).Scan(&through, &from); err != nil {
		t.Fatal(err)
	}
	if through != nil || !from.Equal(day("2026-08-13")) {
		t.Fatalf("cursor = %v from = %v", through, from)
	}
	var audits int
	if err := f.pool.QueryRow(ctx, `select count(*) from audit_log where action='integration.backfill_requested'`).Scan(&audits); err != nil || audits != 1 {
		t.Fatalf("backfill audits = %d, %v", audits, err)
	}
	for range backfillRateMax {
		_ = f.svc.Backfill(ctx, f.owner, f.siteID, "search-console", 5)
	}
	if err := f.svc.Backfill(ctx, f.owner, f.siteID, "search-console", 5); !errors.Is(err, ErrRateLimited) {
		t.Fatalf("err = %v, want rate limit", err)
	}
	if err := f.svc.Backfill(ctx, f.otherOrg, f.siteID, "search-console", 5); !errors.Is(err, ErrNotFound) {
		t.Fatalf("other org err = %v", err)
	}
}

func TestTopAggregatesWithHandComputedFixture(t *testing.T) {
	f := newFixture(t)
	ctx := context.Background()
	f.fake.rows["2026-10-08"] = []Row{
		{Query: "alpha", Page: "/a", Country: "USA", Device: "DESKTOP", Clicks: 10, Impressions: 100, Position: 2},
		{Query: "beta", Page: "/a", Country: "USA", Device: "MOBILE", Clicks: 15, Impressions: 50, Position: 1},
	}
	f.fake.rows["2026-10-09"] = []Row{
		{Query: "alpha", Page: "/b", Country: "DEU", Device: "DESKTOP", Clicks: 5, Impressions: 100, Position: 4},
	}
	f.connect(t, "sc-domain:example.test", 5)
	if err := f.svc.Sync(ctx); err != nil {
		t.Fatal(err)
	}
	// alpha: clicks 15, impressions 200, ctr 0.075, position (2*100+4*100)/200 = 3. beta: 15/50, ctr 0.3, pos 1.
	// Clicks tie at 15, so impressions (200 > 50) put alpha first.
	got, err := f.svc.Top(ctx, f.viewer, f.siteID, "query", day("2026-10-08"), day("2026-10-09"), 10)
	if err != nil {
		t.Fatal(err)
	}
	want := []TopRow{
		{Key: "alpha", Clicks: 15, Impressions: 200, CTR: 0.075, Position: 3},
		{Key: "beta", Clicks: 15, Impressions: 50, CTR: 0.3, Position: 1},
	}
	if len(got.Rows) != 2 || got.Rows[0] != want[0] || got.Rows[1] != want[1] {
		t.Fatalf("query rows = %+v, want %+v", got.Rows, want)
	}
	// Totals: clicks 30, impressions 250, ctr 0.12, position (200+50+400)/250 = 2.6.
	if got.Totals.Clicks != 30 || got.Totals.Impressions != 250 || got.Totals.CTR != 0.12 || got.Totals.Position < 2.59 || got.Totals.Position > 2.61 {
		t.Fatalf("totals = %+v", got.Totals)
	}
	pages, err := f.svc.Top(ctx, f.viewer, f.siteID, "page", day("2026-10-09"), day("2026-10-09"), 10)
	if err != nil || len(pages.Rows) != 1 || pages.Rows[0].Key != "/b" || pages.Rows[0].Clicks != 5 {
		t.Fatalf("page rows = %+v, %v", pages.Rows, err)
	}
	limited, err := f.svc.Top(ctx, f.viewer, f.siteID, "query", day("2026-10-08"), day("2026-10-09"), 1)
	if err != nil || len(limited.Rows) != 1 {
		t.Fatalf("limit not applied: %+v %v", limited.Rows, err)
	}
	for _, bad := range []struct {
		dim      string
		from, to time.Time
		limit    int
	}{
		{"country", day("2026-10-08"), day("2026-10-09"), 5},
		{"query", day("2026-10-09"), day("2026-10-08"), 5},
		{"query", day("2024-01-01"), day("2026-10-09"), 5},
		{"query", day("2026-10-08"), day("2026-10-09"), maxTopLimit + 1},
	} {
		if _, err := f.svc.Top(ctx, f.viewer, f.siteID, bad.dim, bad.from, bad.to, bad.limit); !errors.Is(err, ErrInvalid) {
			t.Fatalf("Top(%+v) err = %v, want ErrInvalid", bad, err)
		}
	}
}

func TestRetentionDropsOldRowsAndEnforcesCap(t *testing.T) {
	f := newFixture(t)
	ctx := context.Background()
	f.connect(t, "sc-domain:example.test", 3)
	old := testNow.AddDate(0, 0, -RetentionDays-5)
	recent := testNow.AddDate(0, 0, -5)
	for i, d := range []time.Time{old, recent} {
		if _, err := f.pool.Exec(ctx, `insert into search_console_rows(site_id,day,row_key,query,page,country,device,clicks,impressions,ctr,position) values ($1,$2,$3,'q','/p','usa','desktop',1,2,0.5,1)`,
			f.siteID, d, []byte{byte(i), 1, 2, 3, 4, 5, 6, 7, 8, 9, 10, 11, 12, 13, 14, 15}); err != nil {
			t.Fatal(err)
		}
	}
	if _, err := f.pool.Exec(ctx, `insert into integration_oauth_states(state_hash,user_id,org_id,site_id,provider,property,backfill_days,code_verifier,expires_at) values ($1,$2,$3,$4,'search-console','x',1,'\x00',$5)`,
		make([]byte, 32), f.owner.UserID, f.owner.OrgID, f.siteID, testNow.Add(-time.Hour)); err != nil {
		t.Fatal(err)
	}
	if err := f.svc.Retention(ctx); err != nil {
		t.Fatal(err)
	}
	if n := f.rowCount(t); n != 1 {
		t.Fatalf("rows after retention = %d, want 1", n)
	}
	if n := f.rowsIn(t, "integration_oauth_states"); n != 0 {
		t.Fatalf("expired states left: %d", n)
	}
}

func TestSiteDeletionCascadesImportedData(t *testing.T) {
	f := newFixture(t)
	ctx := context.Background()
	f.fake.rows["2026-10-10"] = []Row{{Query: "q", Page: "/p", Country: "USA", Device: "DESKTOP", Clicks: 1, Impressions: 2, Position: 1}}
	f.connect(t, "sc-domain:example.test", 3)
	if err := f.svc.Sync(ctx); err != nil {
		t.Fatal(err)
	}
	if _, err := f.pool.Exec(ctx, `delete from sites where id=$1`, f.siteID); err != nil {
		t.Fatal(err)
	}
	if f.rowsIn(t, "integration_connections")+f.rowsIn(t, "search_console_rows") != 0 {
		t.Fatal("integration data survived site deletion")
	}
}
