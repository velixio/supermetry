package sentryingest

import (
	"container/list"
	"context"
	"errors"
	"io"
	"log/slog"
	"net/url"
	"path"
	"strconv"
	"strings"
	"sync"
	"time"

	"supermetry/internal/appingest"
	"supermetry/internal/replay"
	"supermetry/internal/sourcemap"
)

const (
	// DefaultSymbolicateWorkers and the queue size are the hot-path bounds of
	// the stage: the HTTP handler only ever does a non-blocking send.
	DefaultSymbolicateWorkers = 2
	DefaultSymbolicateQueue   = 1000
	// DefaultSymbolicateCacheBytes is the parsed-map LRU budget.
	DefaultSymbolicateCacheBytes = 256 << 20
	// DefaultLookupTTL is how long a release/debug-id lookup result is reused,
	// so a busy release costs one index probe per URL per interval, not per event.
	DefaultLookupTTL = 30 * time.Second

	symbolicateTimeout = 15 * time.Second
	maxMapBytes        = 64 << 20 // the bundle cap for one file
	frameContextLines  = 5
	// mapOverhead estimates the parsed size of a map as a multiple of its JSON
	// size (segment table, decoded strings).
	mapOverhead = 4
)

// minifiedFn finds the blob key of the minified script that goes with a map.
type minifiedFn func(ctx context.Context) (key string, found bool, err error)

// ArtifactStore finds the source map blob for a script. Both lookups are
// scoped to the site's organization and to bundles that cover the site, and
// return the newest matching upload.
type ArtifactStore interface {
	// MapByDebugID returns the blob key of the source map with the debug id.
	MapByDebugID(ctx context.Context, siteID int32, debugID string) (blobKey string, found bool, err error)
	// MapByURL returns the blob key of the map for a script URL of a release
	// and dist. url is either a full URL path or "~/path".
	MapByURL(ctx context.Context, siteID int32, release, dist, url string) (blobKey string, found bool, err error)
}

// ScopeSource is an optional ArtifactStore capability: the minified script
// behind a map, which lets frames report the original name of the enclosing
// function instead of the name mapped at the exact frame position.
type ScopeSource interface {
	// MinifiedByDebugID returns the blob key of the minified script with the debug id.
	MinifiedByDebugID(ctx context.Context, siteID int32, debugID string) (blobKey string, found bool, err error)
	// MinifiedByURL returns the blob key of the minified script of a release and dist.
	MinifiedByURL(ctx context.Context, siteID int32, release, dist, url string) (blobKey string, found bool, err error)
}

// SymbolicatorConfig tunes a Symbolicator. Zero values use the defaults.
type SymbolicatorConfig struct {
	Workers    int
	QueueSize  int
	CacheBytes int64
	// LookupTTL caches lookup results; a negative value disables the cache.
	LookupTTL time.Duration
	Stats     *Stats
	// Deliver receives events submitted through Submit, resolved or not.
	Deliver func(appingest.Event, appingest.Body)
}

type job struct {
	ev      appingest.Event
	body    appingest.Body
	sdkFP   []string
	deliver func(appingest.Event, appingest.Body)
}

// Symbolicator resolves minified JavaScript frames off the HTTP path. Events
// go through a bounded queue to a small worker pool; a full queue means the
// event proceeds unsymbolicated (and is counted), never that the request waits.
type Symbolicator struct {
	store ArtifactStore
	blobs replay.BlobStore
	cfg   SymbolicatorConfig
	queue chan job
	wg    sync.WaitGroup

	mu     sync.RWMutex // guards closed against the send in submit
	closed bool

	cache *mapCache

	lmu     sync.Mutex
	lookups map[string]lookupEntry
	loading map[string]*loadCall
}

type lookupEntry struct {
	key   string
	found bool
	at    time.Time
}

type loadCall struct {
	done chan struct{}
	m    *sourcemap.Map
	err  error
}

// NewSymbolicator starts the workers. Close stops them after the queue drains.
func NewSymbolicator(store ArtifactStore, blobs replay.BlobStore, cfg SymbolicatorConfig) *Symbolicator {
	if cfg.Workers <= 0 {
		cfg.Workers = DefaultSymbolicateWorkers
	}
	if cfg.QueueSize <= 0 {
		cfg.QueueSize = DefaultSymbolicateQueue
	}
	if cfg.CacheBytes <= 0 {
		cfg.CacheBytes = DefaultSymbolicateCacheBytes
	}
	if cfg.LookupTTL == 0 {
		cfg.LookupTTL = DefaultLookupTTL
	}
	if cfg.Stats == nil {
		cfg.Stats = &Stats{}
	}
	s := &Symbolicator{
		store: store, blobs: blobs, cfg: cfg, queue: make(chan job, cfg.QueueSize),
		cache: newMapCache(cfg.CacheBytes), lookups: map[string]lookupEntry{}, loading: map[string]*loadCall{},
	}
	for i := 0; i < cfg.Workers; i++ {
		s.wg.Add(1)
		go s.worker()
	}
	return s
}

// Stats returns the counters (symbolication_ok, _missing, _partial, _skipped, _error).
func (s *Symbolicator) Stats() *Stats { return s.cfg.Stats }

// CacheBytes reports the estimated size of the parsed-map cache.
func (s *Symbolicator) CacheBytes() int64 { return s.cache.size() }

// Close stops accepting events, finishes the queued ones and waits for the workers.
func (s *Symbolicator) Close() {
	s.mu.Lock()
	if s.closed {
		s.mu.Unlock()
		return
	}
	s.closed = true
	close(s.queue)
	s.mu.Unlock()
	s.wg.Wait()
}

// Submit queues an event for symbolication and reports whether it took it; the
// result goes to the configured Deliver. false means the caller continues with
// the event as it is: it has nothing to resolve, the queue is full (counted as
// symbolication_skipped) or the stage is closed.
func (s *Symbolicator) Submit(ev appingest.Event, body appingest.Body, sdkFingerprint []string) bool {
	return s.SubmitWith(ev, body, sdkFingerprint, s.cfg.Deliver)
}

// SubmitWith is Submit with a per-event delivery function, for callers that
// carry request context (the site, the ingest key) with the event.
func (s *Symbolicator) SubmitWith(ev appingest.Event, body appingest.Body, sdkFingerprint []string, deliver func(appingest.Event, appingest.Body)) bool {
	if deliver == nil || !needsSymbolication(body) {
		return false
	}
	s.mu.RLock()
	defer s.mu.RUnlock()
	if s.closed {
		return false
	}
	select {
	case s.queue <- job{ev: ev, body: body, sdkFP: sdkFingerprint, deliver: deliver}:
		return true
	default:
		s.cfg.Stats.Add("symbolication_skipped", 1)
		return false
	}
}

func (s *Symbolicator) worker() {
	defer s.wg.Done()
	for j := range s.queue {
		s.run(j)
	}
}

func (s *Symbolicator) run(j job) {
	ev, body := j.ev, j.body
	func() {
		defer func() {
			if r := recover(); r != nil {
				slog.Error("sentryingest: symbolication panic", "panic", r)
				s.cfg.Stats.Add("symbolication_error", 1)
				ev, body = j.ev, j.body
			}
		}()
		ctx, cancel := context.WithTimeout(context.Background(), symbolicateTimeout)
		defer cancel()
		ev, body = s.symbolicate(ctx, j.ev, j.body, j.sdkFP)
	}()
	j.deliver(ev, body)
}

var jsExt = map[string]struct{}{".js": {}, ".mjs": {}, ".cjs": {}, ".jsx": {}, ".ts": {}, ".tsx": {}}

// jsFrame reports whether the frame looks like a minified script position.
func jsFrame(platform string, f appingest.Frame) bool {
	if f.AbsURL == "" || f.Lineno <= 0 {
		return false
	}
	if platform == "javascript" || platform == "node" {
		return true
	}
	_, ok := jsExt[strings.ToLower(path.Ext(urlPathOf(f.AbsURL)))]
	return ok
}

func needsSymbolication(b appingest.Body) bool {
	for _, ex := range b.Exceptions {
		for _, f := range ex.Frames {
			if jsFrame(b.Platform, f) {
				return true
			}
		}
	}
	return false
}

// urlPathOf returns the path of a URL, or the input when it is not one.
func urlPathOf(s string) string {
	if strings.Contains(s, "://") {
		if u, err := url.Parse(s); err == nil {
			return u.Path
		}
	}
	return s
}

// scriptKeys lists the release-file names to try for an abs_path, in order:
// the exact value, then "~/" plus the path.
func scriptKeys(abs string) []string {
	keys := []string{abs}
	p := urlPathOf(abs)
	if strings.Contains(abs, "://") {
		keys = append(keys, "~"+p)
	} else if !strings.HasPrefix(abs, "~/") {
		keys = append(keys, "~/"+strings.TrimPrefix(p, "/"))
	}
	return keys
}

func (s *Symbolicator) symbolicate(ctx context.Context, ev appingest.Event, body appingest.Body, sdkFP []string) (appingest.Event, appingest.Body) {
	siteID := body.SiteID
	// Frames are copied before they are changed: the caller's body is not shared.
	exceptions := make([]appingest.Exception, len(body.Exceptions))
	copy(exceptions, body.Exceptions)
	attempted, resolved := 0, 0
	maps := map[string]*sourcemap.Map{} // per event, by script URL
	missing := map[string]bool{}
	for i := range exceptions {
		frames := make([]appingest.Frame, len(exceptions[i].Frames))
		copy(frames, exceptions[i].Frames)
		exceptions[i].Frames = frames
		for k := range frames {
			f := frames[k]
			if !jsFrame(body.Platform, f) {
				continue
			}
			attempted++
			m := maps[f.AbsURL]
			if m == nil && !missing[f.AbsURL] {
				m = s.mapFor(ctx, siteID, ev, body, f)
				if m == nil {
					missing[f.AbsURL] = true
				} else {
					maps[f.AbsURL] = m
				}
			}
			if m == nil {
				continue
			}
			if nf, ok := resolveFrame(m, f); ok {
				frames[k] = nf
				resolved++
			}
		}
	}
	switch {
	case attempted == 0:
		return ev, body
	case resolved == 0:
		ev.Symbolication, body.Symbolication = "missing", "missing"
		s.cfg.Stats.Add("symbolication_missing", 1)
		return ev, body
	}
	body.Exceptions = exceptions
	status := "ok"
	if resolved < attempted {
		status = "partial"
	}
	s.cfg.Stats.Add("symbolication_"+status, 1)
	ev.Symbolication, body.Symbolication = status, status
	ev.Frames = eventFrames(exceptions)
	ev.Culprit = culprit(exceptions, ev.Transaction)
	body.Row.Culprit = ev.Culprit
	ev.Fingerprint, ev.GroupingVersion = appingest.GroupingKeyRules(ev, body, sdkFP, body.Row.Rules)
	ev.OwnerUserID = appingest.OwnerOf(body, body.Row.Rules)
	body.Row.GroupingVersion = ev.GroupingVersion
	return ev, body
}

// resolveFrame rewrites one frame from the map; ok is false when the map has
// no mapping at the position.
func resolveFrame(m *sourcemap.Map, f appingest.Frame) (appingest.Frame, bool) {
	mp, ok := m.Lookup(max(f.Lineno-1, 0), max(f.Colno-1, 0))
	if !ok {
		return f, false
	}
	src := cleanSource(mp.Source)
	out := f
	out.AbsPath = scrubFilename(f.AbsURL)
	out.Filename = scrubFilename(src)
	out.Lineno = mp.Line
	out.Colno = mp.Col + 1
	if name, ok := m.ScopeName(max(f.Lineno-1, 0), max(f.Colno-1, 0)); ok {
		out.Function = clip(name, 200)
	} else if mp.HasName && mp.Name != "" {
		out.Function = clip(mp.Name, 200)
	}
	out.InApp = inAppSource(src)
	out.PreContext, out.ContextLine, out.PostContext = nil, "", nil
	pre, cur, post := m.SourceContext(mp.Source, mp.Line, frameContextLines)
	if cur != "" || len(pre) > 0 || len(post) > 0 {
		out.ContextLine = scrubClip(cur, maxContextLine)
		for _, l := range pre {
			out.PreContext = append(out.PreContext, scrubClip(l, maxContextLine))
		}
		for _, l := range post {
			out.PostContext = append(out.PostContext, scrubClip(l, maxContextLine))
		}
	}
	return out, true
}

// cleanSource turns a map source name into a display path: bundler schemes
// and leading "../" go.
func cleanSource(src string) string {
	for _, p := range []string{"webpack://", "webpack-internal://", "rollup://", "file://"} {
		if rest, ok := strings.CutPrefix(src, p); ok {
			src = rest
			break
		}
	}
	src = strings.TrimLeft(src, "/")
	for strings.HasPrefix(src, "../") || strings.HasPrefix(src, "./") {
		src = strings.TrimPrefix(strings.TrimPrefix(src, "../"), "./")
	}
	return src
}

func inAppSource(src string) bool {
	return !strings.Contains(src, "node_modules") && !strings.Contains(src, "webpack/runtime")
}

// mapFor finds and loads the map for one frame: debug id first, then the
// release, dist and URL. nil means there is none.
func (s *Symbolicator) mapFor(ctx context.Context, siteID int32, ev appingest.Event, body appingest.Body, f appingest.Frame) *sourcemap.Map {
	ss, _ := s.store.(ScopeSource)
	for _, im := range body.DebugImages {
		if !strings.EqualFold(im.Type, "sourcemap") || im.CodeFile == "" || !sameScript(im.CodeFile, f.AbsURL) {
			continue
		}
		id := sourcemap.NormalizeDebugID(im.DebugID)
		if id == "" {
			continue
		}
		key, found := s.lookup(ctx, "d|"+itoa32(siteID)+"|"+id, func() (string, bool, error) {
			return s.store.MapByDebugID(ctx, siteID, id)
		})
		if found {
			var mf minifiedFn
			if ss != nil {
				mf = func(ctx context.Context) (string, bool, error) { return ss.MinifiedByDebugID(ctx, siteID, id) }
			}
			if m := s.load(ctx, key, mf); m != nil {
				return m
			}
		}
	}
	for _, name := range scriptKeys(f.AbsURL) {
		key, found := s.lookup(ctx, "u|"+itoa32(siteID)+"|"+ev.Release+"|"+ev.Dist+"|"+name, func() (string, bool, error) {
			return s.store.MapByURL(ctx, siteID, ev.Release, ev.Dist, name)
		})
		if found {
			var mf minifiedFn
			if ss != nil {
				mf = func(ctx context.Context) (string, bool, error) {
					return ss.MinifiedByURL(ctx, siteID, ev.Release, ev.Dist, name)
				}
			}
			if m := s.load(ctx, key, mf); m != nil {
				return m
			}
		}
	}
	return nil
}

func itoa32(n int32) string { return strconv.Itoa(int(n)) }

func sameScript(a, b string) bool {
	return a == b || urlPathOf(a) == urlPathOf(b)
}

func (s *Symbolicator) lookup(ctx context.Context, cacheKey string, fn func() (string, bool, error)) (string, bool) {
	ttl := s.cfg.LookupTTL
	now := time.Now()
	if ttl > 0 {
		s.lmu.Lock()
		e, ok := s.lookups[cacheKey]
		s.lmu.Unlock()
		if ok && now.Sub(e.at) < ttl {
			return e.key, e.found
		}
	}
	key, found, err := fn()
	if err != nil {
		if !errors.Is(err, context.Canceled) {
			slog.Warn("sentryingest: source map lookup", "err", err)
		}
		s.cfg.Stats.Add("symbolication_error", 1)
		return "", false
	}
	if ttl > 0 {
		s.lmu.Lock()
		if len(s.lookups) >= 10_000 {
			clear(s.lookups)
		}
		s.lookups[cacheKey] = lookupEntry{key: key, found: found, at: now}
		s.lmu.Unlock()
	}
	return key, found
}

// load returns the parsed map for a blob key, from the cache or the blob
// store, with its scope index when the minified script is available.
// Concurrent loads of one key share one read.
func (s *Symbolicator) load(ctx context.Context, key string, minified minifiedFn) *sourcemap.Map {
	if m := s.cache.get(key); m != nil {
		return m
	}
	s.lmu.Lock()
	if c, ok := s.loading[key]; ok {
		s.lmu.Unlock()
		<-c.done
		return c.m
	}
	c := &loadCall{done: make(chan struct{})}
	s.loading[key] = c
	s.lmu.Unlock()

	var cost int64
	c.m, cost, c.err = s.read(ctx, key, minified)
	if c.err != nil {
		slog.Warn("sentryingest: load source map", "key", key, "err", c.err)
		s.cfg.Stats.Add("symbolication_error", 1)
	} else {
		s.cache.put(key, c.m, cost)
	}
	s.lmu.Lock()
	delete(s.loading, key)
	s.lmu.Unlock()
	close(c.done)
	return c.m
}

func (s *Symbolicator) read(ctx context.Context, key string, minified minifiedFn) (*sourcemap.Map, int64, error) {
	rc, err := s.blobs.Open(ctx, key)
	if err != nil {
		return nil, 0, err
	}
	defer rc.Close()
	raw, err := io.ReadAll(io.LimitReader(rc, maxMapBytes+1))
	if err != nil {
		return nil, 0, err
	}
	if len(raw) > maxMapBytes {
		return nil, 0, errors.New("source map too large")
	}
	m, err := sourcemap.ParseMap(raw)
	if err != nil {
		return nil, 0, err
	}
	cost := int64(len(raw)) * mapOverhead
	if minified != nil {
		sc := s.scopesFor(ctx, minified)
		m.SetScopes(sc)
		cost += sc.Bytes()
	}
	return m, cost, nil
}

// scopesFor builds the scope index of the minified script. Any failure leaves
// frames on the exact-position name: scopes only ever improve a result.
func (s *Symbolicator) scopesFor(ctx context.Context, minified minifiedFn) *sourcemap.Scopes {
	key, found, err := minified(ctx)
	if err != nil || !found {
		return nil
	}
	rc, err := s.blobs.Open(ctx, key)
	if err != nil {
		return nil
	}
	defer rc.Close()
	src, err := io.ReadAll(io.LimitReader(rc, sourcemap.MaxScopeSource+1))
	if err != nil || len(src) > sourcemap.MaxScopeSource {
		return nil
	}
	sc, err := sourcemap.BuildScopes(ctx, src)
	if err != nil {
		return nil
	}
	return sc
}

// mapCache is a byte-budgeted LRU of parsed maps keyed by blob key. A map
// larger than the whole budget is not cached.
type mapCache struct {
	mu     sync.Mutex
	budget int64
	used   int64
	order  *list.List // front = most recent
	items  map[string]*list.Element
}

type cacheItem struct {
	key  string
	m    *sourcemap.Map
	cost int64
}

func newMapCache(budget int64) *mapCache {
	return &mapCache{budget: budget, order: list.New(), items: map[string]*list.Element{}}
}

func (c *mapCache) get(key string) *sourcemap.Map {
	c.mu.Lock()
	defer c.mu.Unlock()
	el, ok := c.items[key]
	if !ok {
		return nil
	}
	c.order.MoveToFront(el)
	return el.Value.(*cacheItem).m
}

func (c *mapCache) put(key string, m *sourcemap.Map, cost int64) {
	if cost > c.budget {
		return
	}
	c.mu.Lock()
	defer c.mu.Unlock()
	if el, ok := c.items[key]; ok {
		c.order.MoveToFront(el)
		return
	}
	c.items[key] = c.order.PushFront(&cacheItem{key: key, m: m, cost: cost})
	c.used += cost
	for c.used > c.budget {
		el := c.order.Back()
		it := el.Value.(*cacheItem)
		c.order.Remove(el)
		delete(c.items, it.key)
		c.used -= it.cost
	}
}

func (c *mapCache) size() int64 {
	c.mu.Lock()
	defer c.mu.Unlock()
	return c.used
}
