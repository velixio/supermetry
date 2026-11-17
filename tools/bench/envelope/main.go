// Command envelope replays Sentry SDK fixture envelopes against
// POST /api/{site}/envelope/ for a fixed time with many forwarded client
// addresses, and reports accepted events per second and latency percentiles.
//
// Each request is a fixture body with the DSN and every event_id rewritten, so
// nothing is deduplicated. Sites and keys come from a file of "<site id> <key>"
// lines: the per-key budget is 600 events a minute, so a load above 10 events/s
// needs many keys. Throwaway benchmark code, like the rest of tools/bench.
package main

import (
	"bufio"
	"bytes"
	"compress/gzip"
	"crypto/rand"
	"encoding/hex"
	"encoding/json"
	"flag"
	"fmt"
	"io"
	mrand "math/rand/v2"
	"net/http"
	"os"
	"path/filepath"
	"regexp"
	"sort"
	"strings"
	"sync"
	"sync/atomic"
	"time"
)

type fixture struct {
	name   string
	header []byte // first envelope line, with the DSN as a placeholder
	rest   []byte // everything after the first newline
	events int    // items of type "event"
	items  int
}

var eventID = regexp.MustCompile(`"event_id":"[0-9a-f]{32}"`)
var dsnRe = regexp.MustCompile(`"dsn":"[^"]*"`)

func loadFixtures(dir string) ([]fixture, error) {
	var out []fixture
	for _, sdk := range []string{"node", "browser", "python", "go"} {
		files, _ := filepath.Glob(filepath.Join(dir, sdk, "*.envelope"))
		for _, f := range files {
			if strings.HasSuffix(f, "-br.envelope") {
				continue // brotli is not in the standard library
			}
			raw, err := os.ReadFile(f)
			if err != nil {
				return nil, err
			}
			if len(raw) > 2 && raw[0] == 0x1f && raw[1] == 0x8b {
				zr, err := gzip.NewReader(bytes.NewReader(raw))
				if err != nil {
					return nil, err
				}
				if raw, err = io.ReadAll(zr); err != nil {
					return nil, err
				}
			}
			head, rest, ok := bytes.Cut(raw, []byte("\n"))
			if !ok {
				continue
			}
			fx := fixture{name: sdk + "/" + strings.TrimSuffix(filepath.Base(f), ".envelope"), header: head, rest: rest}
			fx.events, fx.items = countItems(rest)
			out = append(out, fx)
		}
	}
	return out, nil
}

// countItems walks "<item header>\n<payload>\n" pairs, using the item length.
func countItems(rest []byte) (events, items int) {
	for len(rest) > 0 {
		line, tail, ok := bytes.Cut(rest, []byte("\n"))
		if !ok {
			return
		}
		var h struct {
			Type   string `json:"type"`
			Length *int   `json:"length"`
		}
		if json.Unmarshal(line, &h) != nil {
			return
		}
		items++
		if h.Type == "event" {
			events++
		}
		if h.Length != nil {
			if *h.Length+1 > len(tail) {
				return
			}
			rest = tail[*h.Length+1:]
			continue
		}
		_, rest, ok = bytes.Cut(tail, []byte("\n"))
		if !ok {
			return
		}
	}
	return
}

func newID() string {
	var b [16]byte
	_, _ = rand.Read(b[:])
	return hex.EncodeToString(b[:])
}

type key struct{ site, key string }

func readKeys(path string) ([]key, error) {
	f, err := os.Open(path)
	if err != nil {
		return nil, err
	}
	defer f.Close()
	var ks []key
	sc := bufio.NewScanner(f)
	for sc.Scan() {
		if p := strings.Fields(sc.Text()); len(p) == 2 {
			ks = append(ks, key{p[0], p[1]})
		}
	}
	return ks, sc.Err()
}

func main() {
	base := flag.String("base", "http://127.0.0.1:8787", "server base URL")
	keysFile := flag.String("keys", "", "file of '<site id> <public key>' lines (required)")
	dir := flag.String("fixtures", "internal/sentryingest/testdata", "fixture directory")
	minutes := flag.Float64("minutes", 5, "duration")
	workers := flag.Int("workers", 24, "concurrent clients (the server caps concurrent body reads at 32 by default)")
	gz := flag.Float64("gzip", 0.5, "share of requests sent gzip-compressed")
	flag.Parse()
	if *keysFile == "" {
		fmt.Fprintln(os.Stderr, "-keys is required")
		os.Exit(2)
	}
	keys, err := readKeys(*keysFile)
	if err != nil || len(keys) == 0 {
		fmt.Fprintln(os.Stderr, "no keys:", err)
		os.Exit(2)
	}
	fixtures, err := loadFixtures(*dir)
	if err != nil || len(fixtures) == 0 {
		fmt.Fprintln(os.Stderr, "no fixtures:", err)
		os.Exit(2)
	}

	var (
		accEvents, accItems, reqs, ok200, r429, r503, other, netErr atomic.Int64
		mu                                                          sync.Mutex
		lat                                                         []time.Duration
		statuses                                                    sync.Map
	)
	client := &http.Client{Transport: &http.Transport{MaxIdleConnsPerHost: *workers * 2, MaxConnsPerHost: *workers * 2}}
	deadline := time.Now().Add(time.Duration(*minutes * float64(time.Minute)))
	var wg sync.WaitGroup
	start := time.Now()
	for w := 0; w < *workers; w++ {
		wg.Add(1)
		go func(w int) {
			defer wg.Done()
			r := mrand.New(mrand.NewPCG(uint64(w), 7))
			var local []time.Duration
			for time.Now().Before(deadline) {
				fx := fixtures[r.IntN(len(fixtures))]
				k := keys[r.IntN(len(keys))]
				head := dsnRe.ReplaceAll(fx.header, []byte(fmt.Sprintf(`"dsn":"http://%s@%s/%s"`, k.key, strings.TrimPrefix(*base, "http://"), k.site)))
				body := append(append(append([]byte{}, head...), '\n'), fx.rest...)
				id := newID()
				body = eventID.ReplaceAll(body, []byte(`"event_id":"`+id+`"`))
				req, _ := http.NewRequest(http.MethodPost, fmt.Sprintf("%s/api/%s/envelope/", *base, k.site), nil)
				if r.Float64() < *gz {
					var b bytes.Buffer
					zw := gzip.NewWriter(&b)
					_, _ = zw.Write(body)
					_ = zw.Close()
					body = b.Bytes()
					req.Header.Set("Content-Encoding", "gzip")
				}
				req.Body = io.NopCloser(bytes.NewReader(body))
				req.ContentLength = int64(len(body))
				req.Header.Set("X-Sentry-Auth", "Sentry sentry_version=7, sentry_client=bench/1, sentry_key="+k.key)
				req.Header.Set("X-Forwarded-For", fmt.Sprintf("%d.%d.%d.%d", 20+r.IntN(180), r.IntN(256), r.IntN(256), 1+r.IntN(254)))
				t0 := time.Now()
				resp, err := client.Do(req)
				if err != nil {
					netErr.Add(1)
					continue
				}
				_, _ = io.Copy(io.Discard, resp.Body)
				resp.Body.Close()
				local = append(local, time.Since(t0))
				reqs.Add(1)
				switch resp.StatusCode {
				case 200:
					ok200.Add(1)
					accEvents.Add(int64(fx.events))
					accItems.Add(int64(fx.items))
				case 429:
					r429.Add(1)
				case 503:
					r503.Add(1)
				default:
					other.Add(1)
					c, _ := statuses.LoadOrStore(resp.StatusCode, new(atomic.Int64))
					c.(*atomic.Int64).Add(1)
				}
			}
			mu.Lock()
			lat = append(lat, local...)
			mu.Unlock()
		}(w)
	}
	wg.Wait()
	secs := time.Since(start).Seconds()
	sort.Slice(lat, func(i, j int) bool { return lat[i] < lat[j] })
	pct := func(p float64) time.Duration {
		if len(lat) == 0 {
			return 0
		}
		return lat[int(float64(len(lat)-1)*p)]
	}
	fmt.Printf("duration=%.1fs workers=%d sites=%d fixtures=%d\n", secs, *workers, len(keys), len(fixtures))
	fmt.Printf("requests=%d (%.0f/s) ok=%d 429=%d 503=%d other=%d net_errors=%d\n", reqs.Load(), float64(reqs.Load())/secs, ok200.Load(), r429.Load(), r503.Load(), other.Load(), netErr.Load())
	statuses.Range(func(k, v any) bool { fmt.Printf("  status %v: %d\n", k, v.(*atomic.Int64).Load()); return true })
	fmt.Printf("accepted events/s=%.0f  accepted items/s=%.0f\n", float64(accEvents.Load())/secs, float64(accItems.Load())/secs)
	fmt.Printf("latency p50=%s p99=%s max=%s\n", pct(0.5), pct(0.99), pct(1))
}
