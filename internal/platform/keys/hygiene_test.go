package keys

import (
	"context"
	"crypto/sha256"
	"errors"
	"net/http"
	"net/http/httptest"
	"regexp"
	"strings"
	"testing"
	"time"

	"supermetry/internal/platform/authz"
	"supermetry/internal/platform/store"
)

func within(got, want time.Time, slack time.Duration) bool {
	d := got.Sub(want)
	return d < slack && d > -slack
}

func TestDefaultExpiryNeverAndExpiringSoon(t *testing.T) {
	keys, owner, _ := newKeys(t)
	ctx := context.Background()

	_, def, err := keys.CreateDefaultExpiry(ctx, owner, "default", []string{"stats:read"}, nil)
	if err != nil {
		t.Fatal(err)
	}
	if def.ExpiresAt == nil || !within(*def.ExpiresAt, time.Now().Add(DefaultExpiry), time.Minute) {
		t.Fatalf("default expiry = %v, want about 90 days", def.ExpiresAt)
	}
	if def.NeverExpires() || def.ExpiringSoon(time.Now()) {
		t.Fatal("a fresh default key is neither never-expiring nor expiring soon")
	}

	_, never, err := keys.Create(ctx, owner, "never", []string{"stats:read"}, nil, nil)
	if err != nil {
		t.Fatal(err)
	}
	if never.ExpiresAt != nil || !never.NeverExpires() || never.ExpiringSoon(time.Now()) {
		t.Fatalf("explicit no-expiry key = %+v", never)
	}

	soon := time.Now().Add(13 * 24 * time.Hour)
	_, near, _ := keys.Create(ctx, owner, "near", []string{"stats:read"}, nil, &soon)
	later := time.Now().Add(15 * 24 * time.Hour)
	_, far, _ := keys.Create(ctx, owner, "far", []string{"stats:read"}, nil, &later)
	if !near.ExpiringSoon(time.Now()) || far.ExpiringSoon(time.Now()) {
		t.Fatalf("expiring soon: 13 days=%t, 15 days=%t", near.ExpiringSoon(time.Now()), far.ExpiringSoon(time.Now()))
	}
}

func TestDefaultExpiryClampsToDelegatingKey(t *testing.T) {
	keys, owner, _ := newKeys(t)
	ctx := context.Background()
	callerExpiry := time.Now().Add(48 * time.Hour)
	plain, _, err := keys.Create(ctx, owner, "Automation", []string{"keys:manage", "stats:read"}, nil, &callerExpiry)
	if err != nil {
		t.Fatal(err)
	}
	caller, _ := keys.Verify(ctx, plain)
	_, child, err := keys.CreateDefaultExpiry(ctx, caller, "child", []string{"stats:read"}, nil)
	if err != nil {
		t.Fatalf("default expiry must clamp to the caller, got %v", err)
	}
	if child.ExpiresAt == nil || child.ExpiresAt.After(callerExpiry.Add(time.Second)) {
		t.Fatalf("child expiry %v exceeds caller %v", child.ExpiresAt, callerExpiry)
	}
}

func TestCurrentKeyFormatAndChecksum(t *testing.T) {
	keys, owner, _ := newKeys(t)
	ctx := context.Background()
	plain, _, err := keys.Create(ctx, owner, "fmt", []string{"stats:read"}, nil, nil)
	if err != nil {
		t.Fatal(err)
	}
	if !regexp.MustCompile(`^smkapi_[a-z2-7]{8}[0-9A-Za-z]{38}$`).MatchString(plain) {
		t.Fatalf("format %q", plain)
	}
	if _, ok := parseKeyPrefix(plain); !ok {
		t.Fatal("a valid key must parse")
	}
	flipped := []byte(plain)
	i := len(plain) - 10 // inside the body
	if flipped[i] == 'A' {
		flipped[i] = 'B'
	} else {
		flipped[i] = 'A'
	}
	for name, bad := range map[string]string{
		"flipped body character": string(flipped),
		"truncated":              plain[:len(plain)-1],
		"extended":               plain + "A",
		"wrong checksum":         plain[:len(plain)-1] + string(rune('A'+(int(plain[len(plain)-1])+1)%26)),
		"non base62":             plain[:len(plain)-2] + "-_",
	} {
		if bad == plain {
			continue
		}
		if _, ok := parseKeyPrefix(bad); ok {
			t.Errorf("%s: parse accepted it", name)
		}
		if _, ok := keys.Verify(ctx, bad); ok {
			t.Errorf("%s: Verify accepted it", name)
		}
	}
	if _, ok := keys.Verify(ctx, plain); !ok {
		t.Fatal("the genuine key must verify")
	}
}

func TestMalformedKeyRejectedBeforeDatabase(t *testing.T) {
	// A Keys with no store panics on any lookup, so this proves the checksum
	// gate runs first.
	keys := &Keys{}
	plain, _ := newCurrentKey("abcdefgh")
	bad := plain[:len(plain)-1] + "!"
	if _, ok := keys.Verify(context.Background(), bad); ok {
		t.Fatal("malformed key accepted")
	}
}

func TestLegacyKeyStillAuthenticates(t *testing.T) {
	keys, owner, pool := newKeys(t)
	ctx := context.Background()
	legacy := "pnl_abcdefgh_" + strings.Repeat("ABCDEFGH", 6) + "2345"
	hash := sha256.Sum256([]byte(legacy))
	if _, err := pool.Exec(ctx, `insert into api_keys(org_id,name,prefix,hash,scopes,created_by) values ($1,'legacy','abcdefgh',$2,'{stats:read}',$3)`,
		owner.OrgID, hash[:], owner.UserID); err != nil {
		t.Fatal(err)
	}
	principal, ok := keys.Verify(ctx, legacy)
	if !ok || principal.Role != authz.RoleKey {
		t.Fatalf("legacy key rejected: %+v %t", principal, ok)
	}
	list, err := keys.List(ctx, owner)
	if err != nil || len(list) != 1 || !list[0].NeverExpires() {
		t.Fatalf("legacy key listing = %+v %v", list, err)
	}
}

func TestClientFamily(t *testing.T) {
	for ua, want := range map[string]string{
		"curl/8.5.0":                         ClientCurl,
		"supermetry-mcp/1.2":                 ClientMCP,
		"Claude-MCP-Client":                  ClientMCP,
		"supermetry-js/0.3 node/22":          ClientSDKJS,
		"python-requests/2.32":               ClientSDKPython,
		"supermetry-python/1.0":              ClientSDKPython,
		"Mozilla/5.0 (X11; Linux) Firefox/1": ClientBrowser,
		"PostmanRuntime/7":                   ClientOther,
		"":                                   ClientOther,
	} {
		if got := ClientFamily(ua); got != want {
			t.Errorf("ClientFamily(%q) = %q, want %q", ua, got, want)
		}
	}
}

func TestLastUsedMetadataIsThrottledAndNeverStoresRawInput(t *testing.T) {
	_, owner, pool := newKeys(t)
	ctx := context.Background()
	keys := newWithFlushInterval(&store.Store{Pool: pool}, 20*time.Millisecond)
	t.Cleanup(keys.Close)
	country := "DE"
	keys.SetCountryResolver(func(*http.Request) string { return country })
	plain, info, err := keys.Create(ctx, owner, "used", []string{"stats:read"}, nil, nil)
	if err != nil {
		t.Fatal(err)
	}
	request := func(ua string) *http.Request {
		r := httptest.NewRequest(http.MethodGet, "/", nil)
		r.RemoteAddr = "203.0.113.9:1234"
		r.Header.Set("User-Agent", ua)
		return r
	}
	read := func() (at *time.Time, c, cl *string) {
		if err := pool.QueryRow(ctx, `select last_used_at,last_used_country,last_used_client from api_keys where id=$1`, info.ID).Scan(&at, &c, &cl); err != nil {
			t.Fatal(err)
		}
		return
	}
	if _, ok := keys.VerifyRequest(request("curl/8.5.0 secret-build-123"), plain); !ok {
		t.Fatal("verify")
	}
	time.Sleep(150 * time.Millisecond)
	first, c, cl := read()
	if first == nil || c == nil || *c != "DE" || cl == nil || *cl != ClientCurl {
		t.Fatalf("first use recorded %v %v %v", first, c, cl)
	}
	// Many more uses inside the minute change nothing and write nothing.
	country = "FR"
	for range 20 {
		if _, ok := keys.VerifyRequest(request("Mozilla/5.0"), plain); !ok {
			t.Fatal("verify")
		}
	}
	time.Sleep(150 * time.Millisecond)
	again, c2, cl2 := read()
	if !again.Equal(*first) || *c2 != "DE" || *cl2 != ClientCurl {
		t.Fatalf("a use within the minute wrote again: %v %v %v", again, *c2, *cl2)
	}
	// Once the minute has passed the next use updates the metadata.
	if _, err := pool.Exec(ctx, `update api_keys set last_used_at = now() - interval '2 minutes' where id=$1`, info.ID); err != nil {
		t.Fatal(err)
	}
	if _, ok := keys.VerifyRequest(request("Mozilla/5.0"), plain); !ok {
		t.Fatal("verify")
	}
	time.Sleep(150 * time.Millisecond)
	_, c3, cl3 := read()
	if *c3 != "FR" || *cl3 != ClientBrowser {
		t.Fatalf("after the window: %v %v", *c3, *cl3)
	}
	// Neither the IP nor the raw User-Agent is stored anywhere in the row.
	var dump string
	if err := pool.QueryRow(ctx, `select row(api_keys.*)::text from api_keys where id=$1`, info.ID).Scan(&dump); err != nil {
		t.Fatal(err)
	}
	if strings.Contains(dump, "203.0.113") || strings.Contains(dump, "secret-build") {
		t.Fatalf("raw request data stored: %s", dump)
	}
	// Verify without a request keeps the stored metadata.
	if _, err := pool.Exec(ctx, `update api_keys set last_used_at = now() - interval '2 minutes' where id=$1`, info.ID); err != nil {
		t.Fatal(err)
	}
	keys.Verify(ctx, plain)
	time.Sleep(150 * time.Millisecond)
	if _, c4, cl4 := read(); *c4 != "FR" || *cl4 != ClientBrowser {
		t.Fatalf("a request-less verify cleared metadata: %v %v", *c4, *cl4)
	}
}

func TestRollKey(t *testing.T) {
	keys, owner, pool := newKeys(t)
	ctx := context.Background()
	expiry := time.Now().Add(30 * 24 * time.Hour)
	oldPlain, oldInfo, err := keys.Create(ctx, owner, "CI", []string{"stats:read"}, []int32{10}, &expiry)
	if err != nil {
		t.Fatal(err)
	}
	newPlain, next, err := keys.Roll(ctx, owner, oldInfo.ID, 24)
	if err != nil {
		t.Fatal(err)
	}
	if next.Name != "CI" || len(next.Scopes) != 1 || next.Scopes[0] != "stats:read" || len(next.SiteIDs) != 1 || next.SiteIDs[0] != 10 {
		t.Fatalf("successor = %+v", next)
	}
	if next.ExpiresAt == nil || !within(*next.ExpiresAt, time.Now().Add(30*24*time.Hour), time.Minute) {
		t.Fatalf("successor expiry %v, want the same 30 day policy", next.ExpiresAt)
	}
	if !strings.HasPrefix(newPlain, "smkapi_") || newPlain == oldPlain {
		t.Fatalf("successor secret %q", newPlain)
	}
	if _, ok := keys.Verify(ctx, newPlain); !ok {
		t.Fatal("successor must verify")
	}
	if _, ok := keys.Verify(ctx, oldPlain); !ok {
		t.Fatal("the old key keeps working during the grace window")
	}
	var oldExpires time.Time
	var rolledFrom *int64
	if err := pool.QueryRow(ctx, `select expires_at from api_keys where id=$1`, oldInfo.ID).Scan(&oldExpires); err != nil {
		t.Fatal(err)
	}
	if !within(oldExpires, time.Now().Add(24*time.Hour), time.Minute) {
		t.Fatalf("old key expires %v, want in 24h", oldExpires)
	}
	if err := pool.QueryRow(ctx, `select rolled_from_id from api_keys where id=$1`, next.ID).Scan(&rolledFrom); err != nil || rolledFrom == nil || *rolledFrom != oldInfo.ID {
		t.Fatalf("rolled_from_id = %v %v", rolledFrom, err)
	}
	var audited int
	if err := pool.QueryRow(ctx, `select count(*) from audit_log where action='api_key.rolled' and target_id=$1 and metadata->>'successor_id'=$2`, oldInfo.ID, itoa(int(next.ID))).Scan(&audited); err != nil || audited != 1 {
		t.Fatalf("audit rows = %d %v", audited, err)
	}

	// The grace window ends: the old key stops, the new one stays.
	if _, err := pool.Exec(ctx, `update api_keys set expires_at = now() - interval '1 second' where id=$1`, oldInfo.ID); err != nil {
		t.Fatal(err)
	}
	if _, ok := keys.Verify(ctx, oldPlain); ok {
		t.Fatal("the old key must stop after the grace window")
	}
	if _, ok := keys.Verify(ctx, newPlain); !ok {
		t.Fatal("successor must keep working")
	}
}

func TestRollGraceChoicesAndNoExpiryPolicy(t *testing.T) {
	keys, owner, _ := newKeys(t)
	ctx := context.Background()
	plain, info, _ := keys.Create(ctx, owner, "forever", []string{"stats:read"}, nil, nil)
	if _, _, err := keys.Roll(ctx, owner, info.ID, 5); !errors.Is(err, ErrInvalidGrace) {
		t.Fatalf("grace 5h err = %v", err)
	}
	_, next, err := keys.Roll(ctx, owner, info.ID, 0)
	if err != nil {
		t.Fatal(err)
	}
	if next.ExpiresAt != nil {
		t.Fatalf("a never-expiring key rolls to a never-expiring successor, got %v", next.ExpiresAt)
	}
	if _, ok := keys.Verify(ctx, plain); ok {
		t.Fatal("grace 0 ends the old key immediately")
	}
	if _, _, err := keys.Roll(ctx, owner, 999999, 24); !errors.Is(err, ErrNotFound) {
		t.Fatalf("unknown key err = %v", err)
	}
	if err := keys.Revoke(ctx, owner, next.ID); err != nil {
		t.Fatal(err)
	}
	if _, _, err := keys.Roll(ctx, owner, next.ID, 24); !errors.Is(err, ErrNotFound) {
		t.Fatalf("revoked key err = %v", err)
	}
	for _, hours := range []int{1, 24, 168} {
		_, k, _ := keys.Create(ctx, owner, "g", []string{"stats:read"}, nil, nil)
		if _, _, err := keys.Roll(ctx, owner, k.ID, hours); err != nil {
			t.Fatalf("grace %dh: %v", hours, err)
		}
	}
}

func TestRollGraceNeverExtendsAnEarlierExpiry(t *testing.T) {
	keys, owner, pool := newKeys(t)
	ctx := context.Background()
	soon := time.Now().Add(2 * time.Hour)
	_, info, _ := keys.Create(ctx, owner, "soon", []string{"stats:read"}, nil, &soon)
	if _, _, err := keys.Roll(ctx, owner, info.ID, 168); err != nil {
		t.Fatal(err)
	}
	var got time.Time
	_ = pool.QueryRow(ctx, `select expires_at from api_keys where id=$1`, info.ID).Scan(&got)
	if got.After(soon.Add(time.Second)) {
		t.Fatalf("grace extended the old key to %v past %v", got, soon)
	}
}

func TestRollNoEscalation(t *testing.T) {
	keys, owner, _ := newKeys(t)
	ctx := context.Background()
	callerPlain, _, err := keys.Create(ctx, owner, "mgr", []string{"keys:manage", "stats:read"}, []int32{10}, nil)
	if err != nil {
		t.Fatal(err)
	}
	caller, _ := keys.Verify(ctx, callerPlain)
	_, broad, _ := keys.Create(ctx, owner, "broad", []string{"stats:read", "sites:write"}, []int32{10}, nil)
	_, allSites, _ := keys.Create(ctx, owner, "all", []string{"stats:read"}, nil, nil)
	_, otherSite, _ := keys.Create(ctx, owner, "other", []string{"stats:read"}, []int32{20}, nil)
	_, manager, _ := keys.Create(ctx, owner, "mgr2", []string{"keys:manage", "stats:read"}, []int32{10}, nil)
	_, narrow, _ := keys.Create(ctx, owner, "narrow", []string{"stats:read"}, []int32{10}, nil)

	for name, id := range map[string]int64{"broader scope": broad.ID, "all sites": allSites.ID, "other site": otherSite.ID} {
		if _, _, err := keys.Roll(ctx, caller, id, 24); !errors.Is(err, ErrNotFound) {
			t.Errorf("%s: err = %v, want ErrNotFound", name, err)
		}
	}
	if _, _, err := keys.Roll(ctx, caller, manager.ID, 24); !errors.Is(err, ErrForbidden) {
		t.Errorf("rolling a keys:manage key: err = %v, want ErrForbidden", err)
	}
	if _, _, err := keys.Roll(ctx, caller, caller.KeyID, 24); !errors.Is(err, ErrForbidden) {
		t.Errorf("rolling itself: err = %v, want ErrForbidden", err)
	}
	if _, next, err := keys.Roll(ctx, caller, narrow.ID, 24); err != nil || next.CreatedBy != owner.UserID {
		t.Errorf("within authority: %+v %v", next, err)
	}
	// A key cannot lengthen life: the successor never outlives the caller.
	callerExpiry := time.Now().Add(72 * time.Hour)
	p2, _, _ := keys.Create(ctx, owner, "short mgr", []string{"keys:manage", "stats:read"}, []int32{10}, &callerExpiry)
	short, _ := keys.Verify(ctx, p2)
	_, forever, _ := keys.Create(ctx, owner, "forever", []string{"stats:read"}, []int32{10}, nil)
	_, next, err := keys.Roll(ctx, short, forever.ID, 24)
	if err != nil || next.ExpiresAt == nil || next.ExpiresAt.After(callerExpiry.Add(time.Second)) {
		t.Errorf("successor must be clamped to the caller expiry: %+v %v", next, err)
	}
}
