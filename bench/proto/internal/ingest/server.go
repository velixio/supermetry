package ingest

import (
	"context"
	"encoding/json"
	"errors"
	"log/slog"
	"net"
	"net/http"
	"os"
	"runtime"
	"strconv"
	"strings"
	"sync/atomic"
	"time"

	"panel/bench/proto/internal/core"
	"panel/bench/proto/internal/query"
)

// Server is the HTTP API on :8090.
type Server struct {
	Pipe     *Pipeline
	Builder  *Builder
	Query    *query.Engine
	Log      *slog.Logger
	TrustXFF bool // take the client IP from X-Forwarded-For (proxy / load tests)

	bots, bad atomic.Int64
}

// Handler returns the routes.
func (s *Server) Handler() http.Handler {
	mux := http.NewServeMux()
	mux.HandleFunc("POST /api/event", s.event)
	mux.HandleFunc("GET /api/query/{name}", s.query)
	mux.HandleFunc("GET /api/realtime", s.realtime)
	mux.HandleFunc("GET /api/stats", s.stats)
	return mux
}

func (s *Server) clientIP(r *http.Request) string {
	if s.TrustXFF {
		if xff := r.Header.Get("X-Forwarded-For"); xff != "" {
			ip, _, _ := strings.Cut(xff, ",")
			return strings.TrimSpace(ip)
		}
	}
	host, _, err := net.SplitHostPort(r.RemoteAddr)
	if err != nil {
		return r.RemoteAddr
	}
	return host
}

func (s *Server) event(w http.ResponseWriter, r *http.Request) {
	r.Body = http.MaxBytesReader(w, r.Body, 8<<10)
	var body Body
	if err := json.NewDecoder(r.Body).Decode(&body); err != nil {
		s.bad.Add(1)
		http.Error(w, "bad body", http.StatusBadRequest)
		return
	}
	ev, err := s.Builder.FromBody(&body, s.clientIP(r), r.UserAgent(), time.Now())
	if errors.Is(err, ErrBot) {
		s.bots.Add(1)
		w.WriteHeader(http.StatusAccepted)
		return
	}
	if err != nil {
		s.bad.Add(1)
		http.Error(w, err.Error(), http.StatusBadRequest)
		return
	}
	switch err := s.Pipe.TrySubmit(ev); {
	case err == nil:
		w.WriteHeader(http.StatusAccepted)
	default: // ErrBackpressure or ErrClosed: tell the client to retry, never block
		w.Header().Set("Retry-After", "1")
		http.Error(w, err.Error(), http.StatusServiceUnavailable)
	}
}

// ParseParams reads site/from/to/filter/limit/mode/event/key from a query string.
func ParseParams(get func(string) string, filters []string) (query.Params, error) {
	var p query.Params
	site, err := strconv.Atoi(get("site"))
	if err != nil {
		return p, errors.New("site required")
	}
	p.Site = int32(site)
	if p.From, err = core.ParseDay(get("from")); err != nil {
		return p, err
	}
	if p.To, err = core.ParseDay(get("to")); err != nil {
		return p, err
	}
	for _, f := range filters {
		k, v, ok := strings.Cut(f, "=")
		if !ok {
			return p, errors.New("filter must be key=value")
		}
		p.Filters = append(p.Filters, core.Filter{Key: k, Value: v})
	}
	if l := get("limit"); l != "" {
		if p.Limit, err = strconv.Atoi(l); err != nil {
			return p, err
		}
	}
	p.Mode, p.PropName, p.PropKey = get("mode"), get("event"), get("key")
	return p, nil
}

func writeJSON(w http.ResponseWriter, code int, v any) {
	w.Header().Set("Content-Type", "application/json")
	w.WriteHeader(code)
	json.NewEncoder(w).Encode(v)
}

func (s *Server) query(w http.ResponseWriter, r *http.Request) {
	q := r.URL.Query()
	p, err := ParseParams(q.Get, q["filter"])
	if err != nil {
		writeJSON(w, http.StatusBadRequest, map[string]string{"error": err.Error()})
		return
	}
	start := time.Now()
	res, err := s.Query.Run(r.Context(), r.PathValue("name"), p)
	if err != nil {
		writeJSON(w, http.StatusBadRequest, map[string]string{"error": err.Error()})
		return
	}
	writeJSON(w, http.StatusOK, map[string]any{"query": r.PathValue("name"), "params": p, "result": res,
		"took_ms": float64(time.Since(start).Microseconds()) / 1000})
}

func (s *Server) realtime(w http.ResponseWriter, r *http.Request) {
	site, err := strconv.Atoi(r.URL.Query().Get("site"))
	if err != nil {
		writeJSON(w, http.StatusBadRequest, map[string]string{"error": "site required"})
		return
	}
	since := time.Now().Add(-5 * time.Minute).UnixMicro()
	writeJSON(w, http.StatusOK, map[string]any{"site": site, "active_visitors_5m": s.Pipe.Cache.Active(int32(site), since)})
}

func (s *Server) stats(w http.ResponseWriter, r *http.Request) {
	writeJSON(w, http.StatusOK, map[string]any{"pipeline": s.Pipe.Stats(), "bots": s.bots.Load(), "bad": s.bad.Load(),
		"rss_kb": RSSKB(os.Getpid()), "goroutines": runtime.NumGoroutine()})
}

// RSSKB returns VmRSS of pid in KiB (Linux), or -1.
func RSSKB(pid int) int64 {
	b, err := os.ReadFile("/proc/" + strconv.Itoa(pid) + "/status")
	if err != nil {
		return -1
	}
	for _, l := range strings.Split(string(b), "\n") {
		if v, ok := strings.CutPrefix(l, "VmRSS:"); ok {
			n, _ := strconv.ParseInt(strings.TrimSpace(strings.TrimSuffix(strings.TrimSpace(v), "kB")), 10, 64)
			return n
		}
	}
	return -1
}

// SaltStore is what the scheduler needs from the store.
type SaltStore interface {
	SaveSalt(ctx context.Context, k core.SaltKey, salt []byte) error
	PurgeSalts(ctx context.Context, maxAge time.Duration) (int64, error)
	RecomputeDay(ctx context.Context, d core.Day, sites []int32) error
	RecomputeMonth(ctx context.Context, first core.Day, sites []int32) error
}

// Scheduler runs the in-process jobs of the server: persist new salts,
// pre-create tomorrow's salt in the last hour of each site's day, purge
// salts after 48 h, and roll up each site's previous local day once closed.
type Scheduler struct {
	Store   SaltStore
	Sites   *Sites
	Salts   *core.Salts
	Persist <-chan core.SaltKey // fed by the Salts persist hook
	Log     *slog.Logger
	done    map[core.SaltKey]bool
}

// Run blocks until ctx is done.
func (sc *Scheduler) Run(ctx context.Context) {
	sc.done = map[core.SaltKey]bool{}
	t := time.NewTicker(time.Minute)
	defer t.Stop()
	sc.tick(ctx)
	for {
		select {
		case <-ctx.Done():
			return
		case k := <-sc.Persist:
			if err := sc.Store.SaveSalt(ctx, k, sc.Salts.Get(k.Site, k.Day)); err != nil {
				sc.Log.Error("save salt", "err", err)
			}
		case <-t.C:
			sc.tick(ctx)
		}
	}
}

func (sc *Scheduler) tick(ctx context.Context) {
	now := time.Now()
	minToday := core.DayOf(now, time.UTC) + 1
	due := map[core.Day][]int32{}
	for _, s := range sc.Sites.All() {
		today := core.DayOf(now, s.Loc)
		minToday = min(minToday, today)
		sc.Salts.Get(s.ID, today)
		if _, end := today.Bounds(s.Loc); end.Sub(now) < time.Hour {
			sc.Salts.Get(s.ID, today+1)
		}
		// Roll up yesterday once it has been closed for 2 minutes.
		y := today - 1
		if _, end := y.Bounds(s.Loc); now.Sub(end) > 2*time.Minute && !sc.done[core.SaltKey{Site: s.ID, Day: y}] {
			due[y] = append(due[y], s.ID)
		}
	}
	for d, ids := range due { // one pass over the day's partition for all due sites
		if err := sc.Store.RecomputeDay(ctx, d, ids); err != nil {
			sc.Log.Error("rollup", "day", d, "sites", len(ids), "err", err)
			continue
		}
		for _, id := range ids {
			sc.done[core.SaltKey{Site: id, Day: d}] = true
		}
		if t := d.Time(); t.AddDate(0, 0, 1).Day() == 1 { // last day of a month: build the monthly rollup
			first := core.Day(time.Date(t.Year(), t.Month(), 1, 0, 0, 0, 0, time.UTC).Unix() / 86400)
			if err := sc.Store.RecomputeMonth(ctx, first, ids); err != nil {
				sc.Log.Error("monthly rollup", "month", first, "err", err)
			}
		}
	}
	sc.Salts.Purge(minToday - 1) // keep yesterday's salt for every zone
	if _, err := sc.Store.PurgeSalts(ctx, 48*time.Hour); err != nil {
		sc.Log.Error("purge salts", "err", err)
	}
}
