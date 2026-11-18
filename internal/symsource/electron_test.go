package symsource

import (
	"bytes"
	"compress/gzip"
	"context"
	"io"
	"net/http"
	"net/http/httptest"
	"os"
	"strings"
	"sync"
	"sync/atomic"
	"testing"
	"time"

	"github.com/jackc/pgx/v5/pgxpool"

	"supermetry/internal/appingest"
	"supermetry/internal/db"
	"supermetry/internal/replay"
	"supermetry/internal/symcache"
	"supermetry/internal/testpg"
)

const (
	testID        = "7a3c9e10-55b2-4b8e-8a53-2f0c8d4e9a01"
	testSymPath   = "/electron/7A3C9E1055B24B8E8A532F0C8D4E9A010/electron.sym"
	testCacheKey  = "remote-symbols/electron/" + testID
	testSiteID    = int32(7)
	testElectron  = "electron"
	testMissingID = "00000000-0000-0000-0000-000000000000"
)

type clock struct {
	mu sync.Mutex
	t  time.Time
}

func (c *clock) Now() time.Time {
	c.mu.Lock()
	defer c.mu.Unlock()
	return c.t
}

func (c *clock) Advance(d time.Duration) {
	c.mu.Lock()
	c.t = c.t.Add(d)
	c.mu.Unlock()
}

type rig struct {
	e     *Electron
	srv   *httptest.Server
	blobs *replay.DiskStore
	pool  *pgxpool.Pool
	clk   *clock
	hits  atomic.Int64
	paths chan string
}

// newRig serves handler as the symbol host. The handler may be nil for a host
// that serves testdata/app.sym at testSymPath and 404 everywhere else.
func newRig(t *testing.T, handler http.HandlerFunc, mod func(*ElectronConfig)) *rig {
	t.Helper()
	pool, _ := testpg.New(t)
	if err := db.Migrate(context.Background(), pool); err != nil {
		t.Fatal(err)
	}
	blobs, err := replay.NewDiskStore(t.TempDir())
	if err != nil {
		t.Fatal(err)
	}
	sym, err := os.ReadFile("../breakpad/testdata/app.sym")
	if err != nil {
		t.Fatal(err)
	}
	r := &rig{blobs: blobs, pool: pool, clk: &clock{t: time.Date(2026, 11, 18, 12, 0, 0, 0, time.UTC)}, paths: make(chan string, 256)}
	if handler == nil {
		handler = func(w http.ResponseWriter, req *http.Request) {
			if req.URL.Path != testSymPath {
				http.NotFound(w, req)
				return
			}
			_, _ = w.Write(sym)
		}
	}
	r.srv = httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, req *http.Request) {
		r.hits.Add(1)
		select {
		case r.paths <- req.URL.Path:
		default:
		}
		handler(w, req)
	}))
	t.Cleanup(r.srv.Close)
	cfg := ElectronConfig{Enabled: true, BaseURL: r.srv.URL, TmpDir: t.TempDir(), Now: r.clk.Now}
	if mod != nil {
		mod(&cfg)
	}
	r.e = NewElectron(pool, blobs, r.srv.Client(), cfg)
	t.Cleanup(r.e.Close)
	return r
}

func image(file, id string) appingest.DebugImage {
	return appingest.DebugImage{Type: "elf", DebugFile: file, CodeFile: "/opt/app/" + file, DebugID: id}
}

func (r *rig) lookup(t *testing.T, m appingest.DebugImage) (string, bool) {
	t.Helper()
	key, found, err := r.e.CacheFor(context.Background(), testSiteID, m)
	if err != nil {
		t.Fatal(err)
	}
	return key, found
}

func TestFetchStoresCacheOnce(t *testing.T) {
	r := newRig(t, nil, nil)
	key, found := r.lookup(t, image(testElectron, testID))
	if !found || key != testCacheKey {
		t.Fatalf("lookup = %q, %v", key, found)
	}
	if got := <-r.paths; got != testSymPath {
		t.Fatalf("requested %q, want %q", got, testSymPath)
	}
	rc, err := r.blobs.Open(context.Background(), key)
	if err != nil {
		t.Fatal(err)
	}
	blob, _ := io.ReadAll(rc)
	_ = rc.Close()
	cache, err := symcache.Open(bytes.NewReader(blob), int64(len(blob)))
	if err != nil {
		t.Fatalf("stored blob is not a symcache: %v", err)
	}
	if locs, ok := cache.Lookup(0x123); !ok || len(locs) == 0 || locs[len(locs)-1].Function != "crash_here" {
		t.Fatalf("Lookup(0x123) = %+v, %v", locs, ok)
	}
	var state string
	var size int64
	if err := r.pool.QueryRow(context.Background(), `select state, size from app_remote_symbols where source='electron' and debug_file='electron' and debug_id=$1`, testID).Scan(&state, &size); err != nil {
		t.Fatal(err)
	}
	if state != "ok" || size != int64(len(blob)) {
		t.Fatalf("row = %s/%d, blob %d", state, size, len(blob))
	}
	if _, found := r.lookup(t, image(testElectron, testID)); !found || r.hits.Load() != 1 {
		t.Fatalf("second lookup hit the server again (%d requests)", r.hits.Load())
	}
}

func TestConcurrentLookupsShareOneFetch(t *testing.T) {
	sym, err := os.ReadFile("../breakpad/testdata/app.sym")
	if err != nil {
		t.Fatal(err)
	}
	release := make(chan struct{})
	r := newRig(t, func(w http.ResponseWriter, req *http.Request) {
		<-release
		_, _ = w.Write(sym)
	}, nil)
	var wg sync.WaitGroup
	for range 10 {
		wg.Add(1)
		go func() {
			defer wg.Done()
			if _, found, err := r.e.CacheFor(context.Background(), testSiteID, image(testElectron, testID)); err != nil || !found {
				t.Errorf("lookup = %v, %v", found, err)
			}
		}()
	}
	time.Sleep(100 * time.Millisecond)
	close(release)
	wg.Wait()
	if n := r.hits.Load(); n != 1 {
		t.Fatalf("%d requests for 10 concurrent lookups, want 1", n)
	}
}

func TestMissingIsNegativelyCached(t *testing.T) {
	r := newRig(t, nil, nil)
	m := image(testElectron, testMissingID)
	if _, found := r.lookup(t, m); found {
		t.Fatal("404 reported as found")
	}
	if r.hits.Load() != 1 {
		t.Fatalf("hits = %d", r.hits.Load())
	}
	r.clk.Advance(23 * time.Hour)
	r.lookup(t, m)
	if r.hits.Load() != 1 {
		t.Fatalf("lookup inside 24 h made a request (%d)", r.hits.Load())
	}
	r.clk.Advance(2 * time.Hour)
	r.lookup(t, m)
	if r.hits.Load() != 2 {
		t.Fatalf("lookup after 24 h made %d requests in total, want 2", r.hits.Load())
	}
}

func TestErrorRetriedAfterAnHour(t *testing.T) {
	r := newRig(t, func(w http.ResponseWriter, req *http.Request) { http.Error(w, "boom", http.StatusInternalServerError) }, nil)
	m := image(testElectron, testID)
	r.lookup(t, m)
	r.clk.Advance(30 * time.Minute)
	r.lookup(t, m)
	if r.hits.Load() != 1 {
		t.Fatalf("error retried inside the hour (%d)", r.hits.Load())
	}
	r.clk.Advance(31 * time.Minute)
	r.lookup(t, m)
	if r.hits.Load() != 2 {
		t.Fatalf("hits = %d, want 2", r.hits.Load())
	}
	var state string
	if err := r.pool.QueryRow(context.Background(), `select state from app_remote_symbols`).Scan(&state); err != nil || state != "error" {
		t.Fatalf("state = %q, %v", state, err)
	}
}

func TestNoRequestWhenNotApplicable(t *testing.T) {
	cases := map[string]struct {
		m   appingest.DebugImage
		mod func(*ElectronConfig)
	}{
		"not an electron module": {m: image("libapp.so", testID)},
		"path tricks":            {m: image("electron..framework", testID)},
		"bad debug id":           {m: image(testElectron, "not-an-id")},
		"site switch off":        {m: image(testElectron, testID), mod: func(c *ElectronConfig) { c.SiteEnabled = func(int32) bool { return false } }},
		"server switch off":      {m: image(testElectron, testID), mod: func(c *ElectronConfig) { c.Enabled = false }},
	}
	for name, tc := range cases {
		t.Run(name, func(t *testing.T) {
			r := newRig(t, nil, tc.mod)
			if key, found := r.lookup(t, tc.m); found || key != "" {
				t.Fatalf("lookup = %q, %v", key, found)
			}
			if r.hits.Load() != 0 {
				t.Fatalf("%d requests made", r.hits.Load())
			}
			var n int
			if err := r.pool.QueryRow(context.Background(), `select count(*) from app_remote_symbols`).Scan(&n); err != nil || n != 0 {
				t.Fatalf("rows = %d, %v", n, err)
			}
		})
	}
}

func TestOversizedDownloadIsAnErrorAndLeavesNoBlob(t *testing.T) {
	big := "MODULE Linux x86_64 7A3C9E1055B24B8E8A532F0C8D4E9A010 electron\n" + strings.Repeat("PUBLIC 300 0 helper_function_name\n", 4000)
	r := newRig(t, func(w http.ResponseWriter, req *http.Request) {
		// Unknown length, so the cap has to fire while streaming.
		w.(http.Flusher).Flush()
		_, _ = io.WriteString(w, big)
	}, func(c *ElectronConfig) { c.MaxDownload = 4096 })
	if _, found := r.lookup(t, image(testElectron, testID)); found {
		t.Fatal("oversized download reported as found")
	}
	var state string
	if err := r.pool.QueryRow(context.Background(), `select state from app_remote_symbols`).Scan(&state); err != nil || state != "error" {
		t.Fatalf("state = %q, %v", state, err)
	}
	blobs, err := r.blobs.List(context.Background(), "remote-symbols/")
	if err != nil || len(blobs) != 0 {
		t.Fatalf("blobs left behind: %v, %v", blobs, err)
	}
}

func TestGarbageBodyIsAnError(t *testing.T) {
	r := newRig(t, func(w http.ResponseWriter, req *http.Request) {
		_, _ = io.WriteString(w, "<html>not a symbol file</html>")
	}, nil)
	if _, found := r.lookup(t, image(testElectron, testID)); found {
		t.Fatal("garbage reported as found")
	}
	blobs, _ := r.blobs.List(context.Background(), "remote-symbols/")
	if len(blobs) != 0 {
		t.Fatalf("blobs left behind: %v", blobs)
	}
}

func TestGzipBodyWithoutContentEncoding(t *testing.T) {
	sym, err := os.ReadFile("../breakpad/testdata/app.sym")
	if err != nil {
		t.Fatal(err)
	}
	var zipped bytes.Buffer
	zw := gzip.NewWriter(&zipped)
	_, _ = zw.Write(sym)
	_ = zw.Close()
	r := newRig(t, func(w http.ResponseWriter, req *http.Request) { _, _ = w.Write(zipped.Bytes()) }, nil)
	if _, found := r.lookup(t, image(testElectron, testID)); !found {
		t.Fatal("pre-compressed .sym was not decoded")
	}
}

func TestRedirectToAnotherHostIsRefused(t *testing.T) {
	var elsewhere atomic.Int64
	other := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, req *http.Request) { elsewhere.Add(1) }))
	t.Cleanup(other.Close)
	r := newRig(t, func(w http.ResponseWriter, req *http.Request) {
		http.Redirect(w, req, other.URL+"/x", http.StatusFound)
	}, nil)
	if _, found := r.lookup(t, image(testElectron, testID)); found {
		t.Fatal("redirect target accepted")
	}
	if elsewhere.Load() != 0 {
		t.Fatal("followed a redirect to another host")
	}
}

func TestSameHostRedirectIsFollowed(t *testing.T) {
	sym, _ := os.ReadFile("../breakpad/testdata/app.sym")
	r := newRig(t, func(w http.ResponseWriter, req *http.Request) {
		if req.URL.Path == testSymPath {
			http.Redirect(w, req, "/moved/electron.sym", http.StatusFound)
			return
		}
		_, _ = w.Write(sym)
	}, nil)
	if _, found := r.lookup(t, image(testElectron, testID)); !found {
		t.Fatal("same-host redirect not followed")
	}
}

func TestConcurrencyLimit(t *testing.T) {
	sym, _ := os.ReadFile("../breakpad/testdata/app.sym")
	var running, peak atomic.Int64
	r := newRig(t, func(w http.ResponseWriter, req *http.Request) {
		n := running.Add(1)
		for {
			p := peak.Load()
			if n <= p || peak.CompareAndSwap(p, n) {
				break
			}
		}
		time.Sleep(50 * time.Millisecond)
		running.Add(-1)
		_, _ = w.Write(sym)
	}, nil)
	ids := []string{
		"11111111-1111-1111-1111-111111111111", "22222222-2222-2222-2222-222222222222",
		"33333333-3333-3333-3333-333333333333", "44444444-4444-4444-4444-444444444444",
		"55555555-5555-5555-5555-555555555555", "66666666-6666-6666-6666-666666666666",
	}
	var wg sync.WaitGroup
	for _, id := range ids {
		wg.Add(1)
		go func() {
			defer wg.Done()
			_, _, _ = r.e.CacheFor(context.Background(), testSiteID, image(testElectron, id))
		}()
	}
	wg.Wait()
	if peak.Load() > 2 || peak.Load() == 0 {
		t.Fatalf("peak concurrent downloads = %d, want 1..2", peak.Load())
	}
}

func TestCallerCancelDoesNotFailOthers(t *testing.T) {
	sym, _ := os.ReadFile("../breakpad/testdata/app.sym")
	release := make(chan struct{})
	r := newRig(t, func(w http.ResponseWriter, req *http.Request) {
		<-release
		_, _ = w.Write(sym)
	}, nil)
	ctx, cancel := context.WithCancel(context.Background())
	done := make(chan error, 1)
	go func() {
		_, _, err := r.e.CacheFor(ctx, testSiteID, image(testElectron, testID))
		done <- err
	}()
	time.Sleep(50 * time.Millisecond)
	cancel()
	if err := <-done; err == nil {
		t.Fatal("cancelled caller got no error")
	}
	close(release)
	deadline := time.Now().Add(5 * time.Second)
	for {
		if _, found := r.lookup(t, image(testElectron, testID)); found {
			break
		}
		if time.Now().After(deadline) {
			t.Fatal("fetch did not complete after the first caller left")
		}
		time.Sleep(20 * time.Millisecond)
	}
}

func TestFetchRateLimit(t *testing.T) {
	r := newRig(t, nil, nil)
	for i := range fetchBurst + 5 {
		id := "00000000-0000-0000-0000-0000000000" + twoDigits(i)
		_, _, _ = r.e.CacheFor(context.Background(), testSiteID, image(testElectron, id))
	}
	if n := r.hits.Load(); n != fetchBurst {
		t.Fatalf("%d requests started, want the burst of %d", n, fetchBurst)
	}
}

func twoDigits(i int) string { return string([]byte{byte('0' + i/10), byte('0' + i%10)}) }

func TestElectronTarget(t *testing.T) {
	good := []appingest.DebugImage{
		{DebugFile: "electron", DebugID: testID},
		{DebugFile: "Electron Framework", DebugID: testID},
		{DebugFile: "electron.exe.pdb", DebugID: "9c2a902b-6fdf-40ad-8308-588a41d572a0-1"},
		{CodeFile: `C:\app\electron.exe`, DebugID: "9c2a902b-6fdf-40ad-8308-588a41d572a0-1"},
		{DebugFile: "libEGL.so", DebugID: testID},
		{CodeFile: "/usr/lib/libffmpeg.so", DebugID: testID},
		{DebugFile: "chrome_crashpad_handler", DebugID: testID},
		{DebugFile: "libvk_swiftshader.dll.pdb", DebugID: testID},
	}
	for _, m := range good {
		if _, _, ok := electronTarget(m); !ok {
			t.Errorf("%+v rejected", m)
		}
	}
	bad := []appingest.DebugImage{
		{DebugFile: "myapp", DebugID: testID},
		{DebugFile: "electron-helper-app", DebugID: testID},
		{DebugFile: "electron/../x", DebugID: testID},
		{DebugFile: "electron\x00", DebugID: testID},
		{DebugFile: "electron", DebugID: ""},
	}
	for _, m := range bad {
		if _, _, ok := electronTarget(m); ok {
			t.Errorf("%+v accepted", m)
		}
	}
	if got := breakpadID("9c2a902b-6fdf-40ad-8308-588a41d572a0-1"); got != "9C2A902B6FDF40AD8308588A41D572A01" {
		t.Errorf("breakpadID = %q", got)
	}
	if got := breakpadID(testID); got != "7A3C9E1055B24B8E8A532F0C8D4E9A010" {
		t.Errorf("breakpadID = %q", got)
	}
}

func TestSymURLEscapesNames(t *testing.T) {
	e := NewElectron(nil, nil, nil, ElectronConfig{Enabled: true})
	got := e.symURL("Electron Framework", testID)
	want := "https://symbols.electronjs.org/Electron%20Framework/7A3C9E1055B24B8E8A532F0C8D4E9A010/Electron%20Framework.sym"
	if got != want {
		t.Fatalf("symURL = %q, want %q", got, want)
	}
	if got := e.symURL("electron.exe.pdb", testID); !strings.HasSuffix(got, "/electron.exe.pdb/7A3C9E1055B24B8E8A532F0C8D4E9A010/electron.exe.sym") {
		t.Fatalf("symURL = %q", got)
	}
}
