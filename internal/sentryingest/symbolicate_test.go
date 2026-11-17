package sentryingest

import (
	"bytes"
	"context"
	"encoding/json"
	"fmt"
	"io"
	"os"
	"strings"
	"sync"
	"testing"
	"time"

	"supermetry/internal/appingest"
	"supermetry/internal/replay"
)

const viteDir = "../sourcemap/testdata/vite/"

// memBlobs is an in-memory replay.BlobStore; only Open is used here.
type memBlobs struct {
	mu    sync.Mutex
	files map[string][]byte
	opens map[string]int
}

func newMemBlobs() *memBlobs { return &memBlobs{files: map[string][]byte{}, opens: map[string]int{}} }

func (m *memBlobs) Open(_ context.Context, key string) (io.ReadCloser, error) {
	m.mu.Lock()
	defer m.mu.Unlock()
	b, ok := m.files[key]
	if !ok {
		return nil, os.ErrNotExist
	}
	m.opens[key]++
	return io.NopCloser(bytes.NewReader(b)), nil
}
func (m *memBlobs) Append(context.Context, string, io.Reader) (int64, error) { return 0, nil }
func (m *memBlobs) PutIfAbsent(context.Context, string, io.Reader) (bool, error) {
	return false, nil
}
func (m *memBlobs) DeletePrefix(context.Context, string) (int64, error)     { return 0, nil }
func (m *memBlobs) Delete(context.Context, string) (int64, error)           { return 0, nil }
func (m *memBlobs) List(context.Context, string) ([]replay.BlobInfo, error) { return nil, nil }

var _ replay.BlobStore = (*memBlobs)(nil)

// fakeArtifacts indexes maps like the Postgres store: scoped per site, newest
// registration wins.
type fakeArtifacts struct {
	mu      sync.Mutex
	byDebug map[string]string // "site|debugID" -> blob key
	byURL   map[string]string // "site|release|dist|url" -> blob key
	lookups int
}

func newFakeArtifacts() *fakeArtifacts {
	return &fakeArtifacts{byDebug: map[string]string{}, byURL: map[string]string{}}
}

func (f *fakeArtifacts) MapByDebugID(_ context.Context, siteID int32, debugID string) (string, bool, error) {
	f.mu.Lock()
	defer f.mu.Unlock()
	f.lookups++
	k, ok := f.byDebug[fmt.Sprintf("%d|%s", siteID, debugID)]
	return k, ok, nil
}

func (f *fakeArtifacts) MapByURL(_ context.Context, siteID int32, release, dist, url string) (string, bool, error) {
	f.mu.Lock()
	defer f.mu.Unlock()
	f.lookups++
	k, ok := f.byURL[fmt.Sprintf("%d|%s|%s|%s", siteID, release, dist, url)]
	return k, ok, nil
}

func (f *fakeArtifacts) addDebug(site int32, id, key string) {
	f.mu.Lock()
	f.byDebug[fmt.Sprintf("%d|%s", site, id)] = key
	f.mu.Unlock()
}

func (f *fakeArtifacts) addURL(site int32, release, dist, url, key string) {
	f.mu.Lock()
	f.byURL[fmt.Sprintf("%d|%s|%s|%s", site, release, dist, url)] = key
	f.mu.Unlock()
}

type viteFixture struct {
	mapBytes []byte
	debugID  string
	col      int
}

func loadVite(t testing.TB) viteFixture {
	t.Helper()
	mb, err := os.ReadFile(viteDir + "app.js.map")
	if err != nil {
		t.Fatal(err)
	}
	eb, err := os.ReadFile(viteDir + "expected.json")
	if err != nil {
		t.Fatal(err)
	}
	var exp struct {
		DebugID  string `json:"debugId"`
		Minified struct {
			Col int `json:"col"`
		} `json:"minified"`
	}
	if err := json.Unmarshal(eb, &exp); err != nil {
		t.Fatal(err)
	}
	return viteFixture{mapBytes: mb, debugID: exp.DebugID, col: exp.Minified.Col}
}

// jsEvent is a Sentry event whose single frame is the minified throw site.
func jsEvent(t testing.TB, v viteFixture, absPath, release string, debugID string) (appingest.Event, appingest.Body) {
	t.Helper()
	dm := ""
	if debugID != "" {
		dm = fmt.Sprintf(`,"debug_meta":{"images":[{"type":"sourcemap","code_file":%q,"debug_id":%q}]}`, absPath, debugID)
	}
	payload := fmt.Sprintf(`{"event_id":"%032x","timestamp":%d,"platform":"javascript","release":%q,
		"exception":{"values":[{"type":"Error","value":"cart limit exceeded","stacktrace":{"frames":[
		 {"abs_path":%q,"filename":%q,"function":"?","lineno":1,"colno":%d,"in_app":true}]}}]}%s}`,
		time.Now().UnixNano(), testNow.Unix(), release, absPath, absPath, v.col+1, dm)
	return normalise(t, payload)
}

type collector struct {
	mu   sync.Mutex
	evs  []appingest.Event
	body []appingest.Body
	ch   chan struct{}
}

func newCollector() *collector { return &collector{ch: make(chan struct{}, 4096)} }

func (c *collector) deliver(ev appingest.Event, b appingest.Body) {
	c.mu.Lock()
	c.evs = append(c.evs, ev)
	c.body = append(c.body, b)
	c.mu.Unlock()
	c.ch <- struct{}{}
}

func (c *collector) wait(t testing.TB) (appingest.Event, appingest.Body) {
	t.Helper()
	select {
	case <-c.ch:
	case <-time.After(5 * time.Second):
		t.Fatal("event not delivered")
	}
	c.mu.Lock()
	defer c.mu.Unlock()
	return c.evs[len(c.evs)-1], c.body[len(c.body)-1]
}

func newSymb(t testing.TB, store ArtifactStore, blobs replay.BlobStore, mut ...func(*SymbolicatorConfig)) (*Symbolicator, *collector) {
	t.Helper()
	col := newCollector()
	cfg := SymbolicatorConfig{Workers: 2, QueueSize: 16, CacheBytes: 64 << 20, Stats: &Stats{}, Deliver: col.deliver}
	for _, m := range mut {
		m(&cfg)
	}
	s := NewSymbolicator(store, blobs, cfg)
	t.Cleanup(s.Close)
	return s, col
}

func topFrame(b appingest.Body) appingest.Frame {
	frames := b.Exceptions[len(b.Exceptions)-1].Frames
	return frames[len(frames)-1]
}

func TestSymbolicateByDebugID(t *testing.T) {
	v := loadVite(t)
	store, blobs := newFakeArtifacts(), newMemBlobs()
	blobs.files["bundles/1/aa/app.js.map"] = v.mapBytes
	store.addDebug(42, v.debugID, "bundles/1/aa/app.js.map")
	s, col := newSymb(t, store, blobs)

	ev, body := jsEvent(t, v, "https://shop.example.com/assets/app.js", "1.0.0", v.debugID)
	rawKey := ev.Fingerprint
	if !s.Submit(ev, body, body.Row.SDKFingerprint) {
		t.Fatal("Submit refused a symbolicatable event")
	}
	got, gb := col.wait(t)
	f := topFrame(gb)
	if f.Filename != "src/cart.js" || f.Lineno != 12 || f.Colno != 5 {
		t.Fatalf("frame = %+v", f)
	}
	if f.AbsPath != "assets/app.js" || !f.InApp {
		t.Fatalf("abs_path/in_app = %q %v", f.AbsPath, f.InApp)
	}
	if !strings.Contains(f.ContextLine, "throw") || len(f.PreContext) != 5 || len(f.PostContext) == 0 {
		t.Fatalf("context = %q pre=%d post=%d", f.ContextLine, len(f.PreContext), len(f.PostContext))
	}
	if got.Symbolication != "ok" || gb.Symbolication != "ok" {
		t.Fatalf("status = %q / %q", got.Symbolication, gb.Symbolication)
	}
	if got.Fingerprint == rawKey {
		t.Fatal("grouping key was not recomputed after resolution")
	}
	wantKey, _ := appingest.GroupingKey(got, gb, nil)
	if got.Fingerprint != wantKey {
		t.Fatal("grouping key is not computed from the resolved frames")
	}
	if !strings.Contains(got.Frames, "src/cart.js:12") {
		t.Fatalf("event frames not refreshed: %q", got.Frames)
	}
}

func TestSymbolicateReleaseURLFallbackTilde(t *testing.T) {
	v := loadVite(t)
	store, blobs := newFakeArtifacts(), newMemBlobs()
	blobs.files["k"] = v.mapBytes
	store.addURL(42, "1.0.0", "", "~/assets/app.js", "k")
	s, col := newSymb(t, store, blobs)

	ev, body := jsEvent(t, v, "https://shop.example.com/assets/app.js?v=3", "1.0.0", "")
	s.Submit(ev, body, nil)
	got, gb := col.wait(t)
	if got.Symbolication != "ok" || topFrame(gb).Filename != "src/cart.js" {
		t.Fatalf("status %q frame %+v", got.Symbolication, topFrame(gb))
	}
}

func TestSymbolicateExactURLBeatsTilde(t *testing.T) {
	v := loadVite(t)
	store, blobs := newFakeArtifacts(), newMemBlobs()
	blobs.files["exact"] = v.mapBytes
	blobs.files["tilde"] = []byte(`{"version":3,"sources":["other.js"],"names":[],"mappings":"AAAA"}`)
	store.addURL(42, "1.0.0", "", "https://shop.example.com/assets/app.js", "exact")
	store.addURL(42, "1.0.0", "", "~/assets/app.js", "tilde")
	s, col := newSymb(t, store, blobs)
	ev, body := jsEvent(t, v, "https://shop.example.com/assets/app.js", "1.0.0", "")
	s.Submit(ev, body, nil)
	_, gb := col.wait(t)
	if topFrame(gb).Filename != "src/cart.js" {
		t.Fatalf("frame %+v", topFrame(gb))
	}
}

func TestSymbolicateMissingMapKeepsRawGrouping(t *testing.T) {
	v := loadVite(t)
	s, col := newSymb(t, newFakeArtifacts(), newMemBlobs())
	ev, body := jsEvent(t, v, "https://shop.example.com/assets/app.js", "9.9.9", "")
	raw := ev.Fingerprint
	s.Submit(ev, body, nil)
	got, gb := col.wait(t)
	if got.Symbolication != "missing" || gb.Symbolication != "missing" {
		t.Fatalf("status %q", got.Symbolication)
	}
	if got.Fingerprint != raw || topFrame(gb).Lineno != 1 {
		t.Fatal("unresolved event must keep the raw frames and key")
	}
	if gb.Release != "9.9.9" {
		t.Fatalf("release = %q", gb.Release)
	}
}

func TestSymbolicateNotNeededIsRefused(t *testing.T) {
	s, _ := newSymb(t, newFakeArtifacts(), newMemBlobs())
	ev, body := normalise(t, `{"event_id":"`+strings.Repeat("a", 32)+`","timestamp":`+fmt.Sprint(testNow.Unix())+`,"message":"hi"}`)
	if s.Submit(ev, body, nil) {
		t.Fatal("an event without minified frames must not be queued")
	}
}

func TestSymbolicateTwoBuildsSameIssue(t *testing.T) {
	v := loadVite(t)
	store, blobs := newFakeArtifacts(), newMemBlobs()
	// Build B: the same map under another debug id and another minified column
	// layout is simulated by a second blob with identical sources.
	other := bytes.Replace(v.mapBytes, []byte(v.debugID), []byte("11111111-2222-4333-8444-555555555555"), -1)
	blobs.files["a"], blobs.files["b"] = v.mapBytes, other
	store.addDebug(42, v.debugID, "a")
	store.addDebug(42, "11111111-2222-4333-8444-555555555555", "b")
	s, col := newSymb(t, store, blobs)
	evA, bodyA := jsEvent(t, v, "https://shop.example.com/assets/app.A.js", "1.0.0", v.debugID)
	evB, bodyB := jsEvent(t, v, "https://shop.example.com/assets/app.B.js", "1.0.1", "11111111-2222-4333-8444-555555555555")
	if evA.Fingerprint == evB.Fingerprint {
		t.Log("raw keys already equal; the check below still holds")
	}
	s.Submit(evA, bodyA, nil)
	a, _ := col.wait(t)
	s.Submit(evB, bodyB, nil)
	b, _ := col.wait(t)
	if a.Symbolication != "ok" || b.Symbolication != "ok" || a.Fingerprint != b.Fingerprint {
		t.Fatalf("builds split into two issues: %q %q (%s vs %s)", a.Symbolication, b.Symbolication, a.Fingerprint, b.Fingerprint)
	}
}

func TestSymbolicateFullQueueSkips(t *testing.T) {
	v := loadVite(t)
	store, blobs := newFakeArtifacts(), newMemBlobs()
	release := make(chan struct{})
	gate := &gatedStore{ArtifactStore: store, gate: release}
	stats := &Stats{}
	s, col := newSymb(t, gate, blobs, func(c *SymbolicatorConfig) { c.Workers = 1; c.QueueSize = 2; c.Stats = stats })
	submit := func() bool {
		ev, body := jsEvent(t, v, "https://shop.example.com/assets/app.js", "1.0.0", "")
		return s.Submit(ev, body, nil)
	}
	// One job is held by the worker, two fill the queue; wait until the worker took the first.
	if !submit() {
		t.Fatal("first submit refused")
	}
	gate.waitEntered(t)
	if !submit() || !submit() {
		t.Fatal("queue refused before it was full")
	}
	if submit() {
		t.Fatal("a full queue must refuse")
	}
	if got := stats.Snapshot()["symbolication_skipped"]; got != 1 {
		t.Fatalf("symbolication_skipped = %d", got)
	}
	close(release)
	for i := 0; i < 3; i++ {
		col.wait(t)
	}
}

type gatedStore struct {
	ArtifactStore
	gate    chan struct{}
	once    sync.Once
	entered chan struct{}
	mu      sync.Mutex
}

func (g *gatedStore) init() {
	g.once.Do(func() { g.entered = make(chan struct{}, 1024) })
}

func (g *gatedStore) waitEntered(t testing.TB) {
	g.init()
	select {
	case <-g.entered:
	case <-time.After(5 * time.Second):
		t.Fatal("worker never started")
	}
}

func (g *gatedStore) MapByDebugID(ctx context.Context, site int32, id string) (string, bool, error) {
	return g.ArtifactStore.MapByDebugID(ctx, site, id)
}

func (g *gatedStore) MapByURL(ctx context.Context, site int32, r, d, u string) (string, bool, error) {
	g.init()
	g.entered <- struct{}{}
	<-g.gate
	return g.ArtifactStore.MapByURL(ctx, site, r, d, u)
}

func TestSymbolicateTenantIsolation(t *testing.T) {
	v := loadVite(t)
	store, blobs := newFakeArtifacts(), newMemBlobs()
	blobs.files["k"] = v.mapBytes
	// The debug id and the release+URL exist only for another site (another org).
	store.addDebug(7, v.debugID, "k")
	store.addURL(7, "1.0.0", "", "~/assets/app.js", "k")
	s, col := newSymb(t, store, blobs)
	ev, body := jsEvent(t, v, "https://shop.example.com/assets/app.js", "1.0.0", v.debugID)
	s.Submit(ev, body, nil)
	got, gb := col.wait(t)
	if got.Symbolication != "missing" || topFrame(gb).Filename == "src/cart.js" {
		t.Fatalf("cross-tenant resolve: %q %+v", got.Symbolication, topFrame(gb))
	}
}

func TestSymbolicateReuploadNewestWinsStoredEventUntouched(t *testing.T) {
	v := loadVite(t)
	store, blobs := newFakeArtifacts(), newMemBlobs()
	blobs.files["old"] = []byte(`{"version":3,"sources":["old/cart.js"],"names":[],"mappings":"AAAA"}`)
	blobs.files["new"] = v.mapBytes
	store.addURL(42, "1.0.0", "", "~/assets/app.js", "old")
	s, col := newSymb(t, store, blobs, func(c *SymbolicatorConfig) { c.LookupTTL = -1 })
	ev1, b1 := jsEvent(t, v, "https://shop.example.com/assets/app.js", "1.0.0", "")
	s.Submit(ev1, b1, nil)
	first, fb := col.wait(t)
	store.addURL(42, "1.0.0", "", "~/assets/app.js", "new") // the re-upload
	ev2, b2 := jsEvent(t, v, "https://shop.example.com/assets/app.js", "1.0.0", "")
	s.Submit(ev2, b2, nil)
	_, sb := col.wait(t)
	if topFrame(sb).Filename != "src/cart.js" {
		t.Fatalf("newest map not used: %+v", topFrame(sb))
	}
	if topFrame(fb).Filename != "old/cart.js" || first.Symbolication != "ok" {
		t.Fatalf("already delivered event was rewritten: %+v", topFrame(fb))
	}
}

func TestSymbolicateCacheStaysUnderBudget(t *testing.T) {
	v := loadVite(t)
	store, blobs := newFakeArtifacts(), newMemBlobs()
	for i := 0; i < 100; i++ {
		key := fmt.Sprintf("m%d", i)
		blobs.files[key] = bytes.Replace(v.mapBytes, []byte("cart limit"), []byte(fmt.Sprintf("pad%03d", i)), 1)
		store.addURL(42, fmt.Sprintf("r%d", i), "", "~/assets/app.js", key)
	}
	budget := int64(len(v.mapBytes)) * 8
	s, col := newSymb(t, store, blobs, func(c *SymbolicatorConfig) { c.CacheBytes = budget })
	for i := 0; i < 100; i++ {
		ev, body := jsEvent(t, v, "https://shop.example.com/assets/app.js", fmt.Sprintf("r%d", i), "")
		s.Submit(ev, body, nil)
		got, _ := col.wait(t)
		if got.Symbolication != "ok" {
			t.Fatalf("release r%d: %q", i, got.Symbolication)
		}
		if used := s.CacheBytes(); used > budget {
			t.Fatalf("cache %d > budget %d after %d maps", used, budget, i+1)
		}
	}
	if s.CacheBytes() == 0 {
		t.Fatal("nothing cached")
	}
}

func TestSymbolicateContextIsScrubbed(t *testing.T) {
	v := loadVite(t)
	m := `{"version":3,"sources":["src/a.js"],"sourcesContent":["line1\nsend(\"you@example.com\")\nline3"],"names":[],"mappings":"AACA"}`
	store, blobs := newFakeArtifacts(), newMemBlobs()
	blobs.files["k"] = []byte(m)
	store.addURL(42, "1.0.0", "", "~/assets/app.js", "k")
	s, col := newSymb(t, store, blobs)
	ev, body := jsEvent(t, v, "https://shop.example.com/assets/app.js", "1.0.0", "")
	s.Submit(ev, body, nil)
	_, gb := col.wait(t)
	f := topFrame(gb)
	if strings.Contains(f.ContextLine, "you@example.com") || f.ContextLine == "" {
		t.Fatalf("context line = %q", f.ContextLine)
	}
}

func TestSymbolicateInAppRule(t *testing.T) {
	if inAppSource("node_modules/react/index.js") || inAppSource("webpack/runtime/chunk loaded") || !inAppSource("src/cart.js") {
		t.Fatal("in_app rule wrong")
	}
}

func TestSymbolicateCloseDrainsAndRefuses(t *testing.T) {
	v := loadVite(t)
	store, blobs := newFakeArtifacts(), newMemBlobs()
	blobs.files["k"] = v.mapBytes
	store.addURL(42, "1.0.0", "", "~/assets/app.js", "k")
	col := newCollector()
	s := NewSymbolicator(store, blobs, SymbolicatorConfig{Deliver: col.deliver, Stats: &Stats{}})
	ev, body := jsEvent(t, v, "https://shop.example.com/assets/app.js", "1.0.0", "")
	if !s.Submit(ev, body, nil) {
		t.Fatal("refused")
	}
	s.Close()
	col.wait(t) // delivered before Close returned
	if s.Submit(ev, body, nil) {
		t.Fatal("Submit after Close must refuse")
	}
}

func TestSymbolicateFunctionFromMapNames(t *testing.T) {
	v := loadVite(t)
	store, blobs := newFakeArtifacts(), newMemBlobs()
	blobs.files["k"] = []byte(`{"version":3,"sources":["src/a.js"],"names":["checkout"],"mappings":"AAAAA"}`)
	store.addURL(42, "1.0.0", "", "~/assets/app.js", "k")
	s, col := newSymb(t, store, blobs)
	ev, body := jsEvent(t, v, "https://shop.example.com/assets/app.js", "1.0.0", "")
	s.Submit(ev, body, nil)
	_, gb := col.wait(t)
	if f := topFrame(gb); f.Function != "checkout" {
		t.Fatalf("function = %q", f.Function)
	}
}
