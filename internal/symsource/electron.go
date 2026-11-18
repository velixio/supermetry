// Package symsource fetches public debug symbols for native crashes of
// third-party runtimes. Today that is Electron's symbol server: a module of
// Electron's own binaries whose symbols the project did not upload is looked up
// there, converted to a symbol cache and kept in a cache shared by every
// organization.
//
// Fetching happens only on a symbolication miss, off the ingest path (the
// caller is the symbolicator, never the HTTP handler), against one fixed host.
// Only a module file name and a debug id leave the server.
package symsource

import (
	"bufio"
	"compress/gzip"
	"context"
	"errors"
	"fmt"
	"io"
	"log/slog"
	"net"
	"net/http"
	"net/url"
	"os"
	"path"
	"regexp"
	"strings"
	"sync"
	"time"

	"github.com/jackc/pgx/v5/pgxpool"
	"golang.org/x/sync/singleflight"

	"supermetry/internal/appingest"
	"supermetry/internal/breakpad"
	"supermetry/internal/replay"
	"supermetry/internal/safehttp"
	"supermetry/internal/symcache"
)

const (
	// SourceElectron names Electron's symbol server in app_remote_symbols and
	// in the blob prefix.
	SourceElectron = "electron"
	// DefaultBaseURL is the only host a production Electron fetcher talks to.
	DefaultBaseURL = "https://symbols.electronjs.org"

	defaultTimeout     = 60 * time.Second
	defaultMaxDownload = 1 << 30
	defaultConcurrent  = 2
	defaultMissingTTL  = 24 * time.Hour
	defaultErrorTTL    = time.Hour
	maxRedirects       = 2
	fetchBurst         = 30
	fetchRefill        = time.Second
	maxDebugFileLen    = 128
	blobPrefix         = "remote-symbols/"
	userAgent          = "supermetry-symbols"
	maxCacheBlobBytes  = int64(2) << 30
)

// RemoteSymbols finds a symbol cache for a module the project has not
// uploaded. found is false when no source has it (or the source is off);
// cacheKey then is empty. An error means "unknown right now": callers treat it
// like a miss and must not fail the event over it.
type RemoteSymbols interface {
	CacheFor(ctx context.Context, siteID int32, m appingest.DebugImage) (cacheKey string, found bool, err error)
}

// ElectronConfig tunes the Electron fetcher; the zero value of every field but
// Enabled means the documented default.
type ElectronConfig struct {
	// Enabled is the server switch (SUPERMETRY_ELECTRON_SYMBOLS).
	Enabled bool
	// SiteEnabled reports the per-project switch; nil means on for every site.
	SiteEnabled func(siteID int32) bool
	// BaseURL replaces DefaultBaseURL. For tests only: production code leaves
	// it empty so the host stays fixed.
	BaseURL string
	// TmpDir holds the symbol cache while it is built, before it moves to the
	// blob store. Empty means the OS temp dir.
	TmpDir        string
	Now           func() time.Time
	Timeout       time.Duration // per lookup, queueing included; default 60 s
	MaxDownload   int64         // decoded bytes of one .sym file; default 1 GiB
	MaxConcurrent int           // simultaneous downloads; default 2
	MissingTTL    time.Duration // before a 404 is retried; default 24 h
	ErrorTTL      time.Duration // before a failed fetch is retried; default 1 h
}

// Electron fetches Electron's public Breakpad symbols on demand.
type Electron struct {
	cfg    ElectronConfig
	base   *url.URL
	table  cacheTable
	blobs  replay.BlobStore
	client *http.Client

	flight singleflight.Group
	sem    chan struct{}
	limit  *bucket

	ctx    context.Context
	cancel context.CancelFunc
	wg     sync.WaitGroup
}

var _ RemoteSymbols = (*Electron)(nil)

// NewHTTPClient returns the client for the real symbol host: no proxy, no
// private or link-local destinations (even though the host is fixed, DNS is
// not). NewElectron adds the redirect policy.
func NewHTTPClient() *http.Client {
	dialer := &net.Dialer{Timeout: 10 * time.Second}
	return &http.Client{Transport: &http.Transport{
		DialContext:           safehttp.GuardedDial(net.DefaultResolver, dialer.DialContext, false),
		TLSHandshakeTimeout:   10 * time.Second,
		ResponseHeaderTimeout: 20 * time.Second,
		MaxIdleConns:          4,
		IdleConnTimeout:       30 * time.Second,
	}}
}

// NewElectron builds the fetcher. client is copied: its redirect policy is
// replaced by one that follows at most two redirects, and only to the same
// scheme and host. Close stops fetches in flight.
func NewElectron(pool *pgxpool.Pool, blobs replay.BlobStore, client *http.Client, cfg ElectronConfig) *Electron {
	if cfg.BaseURL == "" {
		cfg.BaseURL = DefaultBaseURL
	}
	if cfg.Now == nil {
		cfg.Now = time.Now
	}
	if cfg.Timeout <= 0 {
		cfg.Timeout = defaultTimeout
	}
	if cfg.MaxDownload <= 0 {
		cfg.MaxDownload = defaultMaxDownload
	}
	if cfg.MaxConcurrent <= 0 {
		cfg.MaxConcurrent = defaultConcurrent
	}
	if cfg.MissingTTL <= 0 {
		cfg.MissingTTL = defaultMissingTTL
	}
	if cfg.ErrorTTL <= 0 {
		cfg.ErrorTTL = defaultErrorTTL
	}
	base, err := url.Parse(cfg.BaseURL)
	if err != nil || base.Host == "" || (base.Scheme != "https" && base.Scheme != "http") {
		cfg.Enabled = false // a malformed base URL must never turn into a request
		base = &url.URL{Scheme: "https", Host: "invalid.invalid"}
	}
	if client == nil {
		client = NewHTTPClient()
	}
	c := *client
	c.Timeout = cfg.Timeout
	c.CheckRedirect = func(req *http.Request, via []*http.Request) error {
		if len(via) > maxRedirects {
			return errors.New("too many redirects")
		}
		if req.URL.Host != base.Host || req.URL.Scheme != base.Scheme {
			return errors.New("redirect to another host")
		}
		return nil
	}
	ctx, cancel := context.WithCancel(context.Background())
	return &Electron{
		cfg: cfg, base: base, blobs: blobs, client: &c,
		table: cacheTable{pool: pool, source: SourceElectron},
		sem:   make(chan struct{}, cfg.MaxConcurrent),
		limit: newBucket(fetchBurst, fetchRefill, cfg.Now),
		ctx:   ctx, cancel: cancel,
	}
}

// Close cancels fetches in flight and waits for them to finish.
func (e *Electron) Close() {
	e.cancel()
	e.wg.Wait()
}

// CacheKey is the blob key of a remote symbol cache.
func CacheKey(source, debugID string) string { return blobPrefix + source + "/" + debugID }

type outcome struct {
	key   string
	found bool
}

// CacheFor implements RemoteSymbols.
func (e *Electron) CacheFor(ctx context.Context, siteID int32, m appingest.DebugImage) (string, bool, error) {
	if !e.cfg.Enabled || e.table.pool == nil {
		return "", false, nil
	}
	file, id, ok := electronTarget(m)
	if !ok {
		return "", false, nil
	}
	if e.cfg.SiteEnabled != nil && !e.cfg.SiteEnabled(siteID) {
		return "", false, nil
	}
	now := e.cfg.Now()
	r, found, err := e.table.get(ctx, file, id)
	if err != nil {
		return "", false, err
	}
	if found {
		if out, done := e.decide(r, now); done {
			if out.found {
				if err := e.table.touch(ctx, file, id, r, now); err != nil {
					slog.Warn("symsource: touch remote symbol", "error", err)
				}
			}
			return out.key, out.found, nil
		}
	}
	// One fetch per key at a time. The fetch runs under the fetcher's own
	// context, so one caller giving up does not fail the others.
	ch := e.flight.DoChan(file+"\x00"+id, func() (any, error) {
		e.wg.Add(1)
		defer e.wg.Done()
		return e.fill(file, id)
	})
	select {
	case <-ctx.Done():
		return "", false, ctx.Err()
	case res := <-ch:
		if res.Err != nil {
			return "", false, res.Err
		}
		out := res.Val.(outcome)
		return out.key, out.found, nil
	}
}

// decide turns a stored row into an answer; done is false when the row is a
// negative entry old enough to try again.
func (e *Electron) decide(r row, now time.Time) (outcome, bool) {
	switch r.State {
	case stateOK:
		return outcome{key: r.CacheKey, found: true}, true
	case stateMissing:
		return outcome{}, now.Before(r.FetchedAt.Add(e.cfg.MissingTTL))
	case stateError:
		return outcome{}, now.Before(r.FetchedAt.Add(e.cfg.ErrorTTL))
	}
	return outcome{}, false
}

func (e *Electron) fill(file, id string) (any, error) {
	ctx, cancel := context.WithTimeout(e.ctx, e.cfg.Timeout)
	defer cancel()
	// A concurrent caller may have finished the same key between this
	// caller's read and its turn in the flight.
	if r, found, err := e.table.get(ctx, file, id); err != nil {
		return nil, err
	} else if found {
		if out, done := e.decide(r, e.cfg.Now()); done {
			return out, nil
		}
	}
	// Lookups for ids nobody ever shipped are cheap for a sender to cause, so
	// the number of fetches started is bounded globally. A refused start is not
	// recorded: the lookup simply happens later.
	if !e.limit.allow() {
		return outcome{}, nil
	}
	select {
	case e.sem <- struct{}{}:
		defer func() { <-e.sem }()
	case <-ctx.Done():
		return outcome{}, nil
	}
	state, size, err := e.fetch(ctx, file, id)
	if err != nil && e.ctx.Err() != nil {
		return nil, e.ctx.Err() // shutting down: record nothing
	}
	key := ""
	if state == stateOK {
		key = CacheKey(SourceElectron, id)
	}
	dbCtx, dbCancel := context.WithTimeout(context.WithoutCancel(ctx), 10*time.Second)
	defer dbCancel()
	if perr := e.table.put(dbCtx, file, id, state, key, size, e.cfg.Now()); perr != nil {
		if state == stateOK {
			_, _ = e.blobs.Delete(dbCtx, key)
		}
		return nil, perr
	}
	if err != nil {
		slog.Warn("symsource: electron symbol fetch failed", "debug_file", file, "debug_id", id, "error", err)
	}
	return outcome{key: key, found: state == stateOK}, nil
}

var errTooLarge = errors.New("symbol file exceeds the download cap")

// fetch downloads one .sym file and stores its symbol cache. The raw file is
// streamed through the builder and never kept; a failure leaves no blob.
func (e *Electron) fetch(ctx context.Context, file, id string) (state string, size int64, err error) {
	req, err := http.NewRequestWithContext(ctx, http.MethodGet, e.symURL(file, id), nil)
	if err != nil {
		return stateError, 0, err
	}
	req.Header.Set("User-Agent", userAgent)
	resp, err := e.client.Do(req)
	if err != nil {
		return stateError, 0, fmt.Errorf("request: %w", err)
	}
	defer resp.Body.Close()
	switch {
	case resp.StatusCode == http.StatusNotFound || resp.StatusCode == http.StatusForbidden || resp.StatusCode == http.StatusGone:
		_, _ = io.Copy(io.Discard, io.LimitReader(resp.Body, 4096))
		return stateMissing, 0, nil
	case resp.StatusCode != http.StatusOK:
		return stateError, 0, fmt.Errorf("status %d", resp.StatusCode)
	case resp.ContentLength > e.cfg.MaxDownload:
		return stateError, 0, errTooLarge
	}

	body, err := decodedBody(resp.Body)
	if err != nil {
		return stateError, 0, err
	}
	tmp, err := os.CreateTemp(e.cfg.TmpDir, "remote-sym-*.cache")
	if err != nil {
		return stateError, 0, err
	}
	defer func() {
		_ = tmp.Close()
		_ = os.Remove(tmp.Name())
	}()
	capped := &capReader{r: body, left: e.cfg.MaxDownload}
	bw := bufio.NewWriterSize(tmp, 1<<20)
	if _, err := breakpad.Build(capped, bw, e.cfg.TmpDir); err != nil {
		if capped.exceeded {
			return stateError, 0, errTooLarge
		}
		return stateError, 0, fmt.Errorf("build symbol cache: %w", err)
	}
	if capped.exceeded {
		return stateError, 0, errTooLarge
	}
	if err := bw.Flush(); err != nil {
		return stateError, 0, err
	}
	size, err = tmp.Seek(0, io.SeekEnd)
	if err != nil || size == 0 || size > maxCacheBlobBytes {
		return stateError, 0, fmt.Errorf("symbol cache size %d", size)
	}
	if _, err := symcache.Open(tmp, size); err != nil {
		return stateError, 0, fmt.Errorf("symbol cache is not valid: %w", err)
	}
	if _, err := tmp.Seek(0, io.SeekStart); err != nil {
		return stateError, 0, err
	}
	key := CacheKey(SourceElectron, id)
	// Replace whatever an earlier crashed fetch left under this key.
	if _, err := e.blobs.Delete(ctx, key); err != nil {
		return stateError, 0, err
	}
	if _, err := e.blobs.PutIfAbsent(ctx, key, tmp); err != nil {
		_, _ = e.blobs.Delete(context.WithoutCancel(ctx), key)
		return stateError, 0, err
	}
	return stateOK, size, nil
}

// symURL builds <base>/<debug_file>/<BREAKPADID>/<debug_file sans .pdb>.sym.
func (e *Electron) symURL(file, id string) string {
	u := *e.base
	name := strings.TrimSuffix(file, ".pdb")
	u.Path = "/" + file + "/" + breakpadID(id) + "/" + name + ".sym"
	u.RawPath = "/" + url.PathEscape(file) + "/" + breakpadID(id) + "/" + url.PathEscape(name) + ".sym"
	u.RawQuery, u.Fragment = "", ""
	return u.String()
}

// decodedBody undoes a gzip layer the transport did not (a .sym stored
// pre-compressed and served without Content-Encoding).
func decodedBody(r io.Reader) (io.Reader, error) {
	br := bufio.NewReaderSize(r, 4096)
	if magic, err := br.Peek(2); err == nil && magic[0] == 0x1f && magic[1] == 0x8b {
		return gzip.NewReader(br)
	}
	return br, nil
}

// capReader fails reads once more than left bytes have been delivered.
type capReader struct {
	r        io.Reader
	left     int64
	exceeded bool
}

func (c *capReader) Read(p []byte) (int, error) {
	if c.left <= 0 {
		// A clean EOF at exactly the cap is fine; one more byte is too much.
		var one [1]byte
		n, err := c.r.Read(one[:])
		if n > 0 {
			c.exceeded = true
			return 0, errTooLarge
		}
		return 0, err
	}
	if int64(len(p)) > c.left {
		p = p[:c.left]
	}
	n, err := c.r.Read(p)
	c.left -= int64(n)
	return n, err
}

// electronModules are the module names (basename, .pdb/.so/.dll/.dylib
// stripped, lower-cased) of Electron's own binaries; nothing else is ever
// looked up, so a project's own modules never leave the server.
var electronModules = map[string]bool{
	"electron": true, "electron framework": true, "electron.exe": true,
	"libegl": true, "libglesv2": true, "libffmpeg": true,
	"libvk_swiftshader": true, "vk_swiftshader": true, "chrome_crashpad_handler": true,
}

var debugFilePattern = regexp.MustCompile(`^[A-Za-z0-9][A-Za-z0-9._ +-]*$`)

// electronTarget returns the symbol-server debug file name and the normalised
// debug id of an image, or ok=false when it is not an Electron binary or its
// names are not safe to put into a URL.
func electronTarget(m appingest.DebugImage) (file, id string, ok bool) {
	id = strings.ToLower(strings.TrimSpace(m.DebugID))
	if breakpadID(id) == "" {
		return "", "", false
	}
	file = baseName(m.DebugFile)
	if file == "" {
		file = baseName(m.CodeFile)
		if lower := strings.ToLower(file); strings.HasSuffix(lower, ".exe") || strings.HasSuffix(lower, ".dll") {
			file += ".pdb"
		}
	}
	if file == "" || len(file) > maxDebugFileLen || !debugFilePattern.MatchString(file) || strings.Contains(file, "..") {
		return "", "", false
	}
	name := strings.TrimSuffix(strings.ToLower(file), ".pdb")
	for _, ext := range []string{".so", ".dll", ".dylib"} {
		name = strings.TrimSuffix(name, ext)
	}
	if !electronModules[name] && !electronModules[strings.TrimSuffix(name, ".exe")] {
		return "", "", false
	}
	return file, id, true
}

func baseName(p string) string {
	p = strings.ReplaceAll(strings.TrimSpace(p), `\`, "/")
	if p == "" {
		return ""
	}
	return strings.TrimSpace(path.Base(p))
}

// breakpadID converts a debug id (GUID, optionally "-<age hex>") to the
// uppercase GUID+age form used in symbol-server paths. It returns "" for
// anything that is not one.
func breakpadID(id string) string {
	parts := strings.Split(id, "-")
	if len(parts) != 5 && len(parts) != 6 {
		return ""
	}
	var guid strings.Builder
	for i, w := range []int{8, 4, 4, 4, 12} {
		if len(parts[i]) != w || !isHex(parts[i]) {
			return ""
		}
		guid.WriteString(parts[i])
	}
	age := "0"
	if len(parts) == 6 {
		if len(parts[5]) > 8 || !isHex(parts[5]) {
			return ""
		}
		age = parts[5]
	}
	return strings.ToUpper(guid.String() + age)
}

func isHex(s string) bool {
	for i := 0; i < len(s); i++ {
		c := s[i]
		if !(c >= '0' && c <= '9' || c >= 'a' && c <= 'f' || c >= 'A' && c <= 'F') {
			return false
		}
	}
	return s != ""
}

// bucket is a small token bucket: burst tokens, one more every refill.
type bucket struct {
	mu     sync.Mutex
	tokens int
	burst  int
	refill time.Duration
	last   time.Time
	now    func() time.Time
}

func newBucket(burst int, refill time.Duration, now func() time.Time) *bucket {
	return &bucket{tokens: burst, burst: burst, refill: refill, now: now, last: now()}
}

func (b *bucket) allow() bool {
	b.mu.Lock()
	defer b.mu.Unlock()
	if gained := int(b.now().Sub(b.last) / b.refill); gained > 0 {
		b.tokens = min(b.burst, b.tokens+gained)
		b.last = b.last.Add(time.Duration(gained) * b.refill)
	}
	if b.tokens == 0 {
		return false
	}
	b.tokens--
	return true
}
