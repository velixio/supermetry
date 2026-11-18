package platformhttp

import (
	"encoding/hex"
	"errors"
	"net/http"
	"strconv"
	"strings"
	"time"

	"supermetry/internal/obslogs"
	"supermetry/internal/obslogs/search"
	"supermetry/internal/platform/authz"
	coresites "supermetry/internal/sites"
)

// obsLogSite resolves an app project the caller may read. Dashboard roles and
// API keys with stats:read read logs; a foreign organization's site is a 404.
func (h *handler) obsLogSite(w http.ResponseWriter, r *http.Request) (coresites.Site, bool) {
	_, site, ok := h.sitePrincipal(w, r, authz.ActionStatsRead, h.resourceRoles(r)...)
	if !ok {
		return site, false
	}
	if site.Kind != coresites.KindApp || h.deps.Store == nil {
		writeProblem(w, http.StatusNotFound, "Not found", "not an app project")
		return site, false
	}
	return site, true
}

func (h *handler) obsLogReader() *obslogs.Reader {
	return &obslogs.Reader{Pool: h.deps.Store.Pool, Now: h.deps.Now}
}

func obsLogError(w http.ResponseWriter, err error) {
	var parse *search.ParseError
	switch {
	case errors.As(err, &parse):
		writeProblem(w, http.StatusBadRequest, "Bad request", parse.Error())
	case errors.Is(err, obslogs.ErrInvalid):
		writeProblem(w, http.StatusBadRequest, "Bad request", strings.TrimPrefix(err.Error(), obslogs.ErrInvalid.Error()+": "))
	default:
		serviceError(w, err)
	}
}

func queryTime(w http.ResponseWriter, r *http.Request, name string, fallback time.Time) (time.Time, bool) {
	raw := r.URL.Query().Get(name)
	if raw == "" {
		return fallback, true
	}
	t, err := time.Parse(time.RFC3339, raw)
	if err != nil {
		writeProblem(w, http.StatusBadRequest, "Bad request", name+" must be an RFC 3339 timestamp")
		return time.Time{}, false
	}
	return t.UTC(), true
}

func queryLimit(w http.ResponseWriter, r *http.Request) (int, bool) {
	raw := r.URL.Query().Get("limit")
	if raw == "" {
		return 0, true
	}
	n, err := strconv.Atoi(raw)
	if err != nil || n < 1 || n > obslogs.MaxLimit {
		writeProblem(w, http.StatusBadRequest, "Bad request", "limit must be between 1 and "+strconv.Itoa(obslogs.MaxLimit))
		return 0, false
	}
	return n, true
}

// searchObsLogs serves GET .../obs/logs on the private and public surfaces.
func (h *handler) searchObsLogs(w http.ResponseWriter, r *http.Request) {
	site, ok := h.obsLogSite(w, r)
	if !ok {
		return
	}
	query := r.URL.Query()
	q, err := search.Parse(query.Get("q"))
	if err != nil {
		obsLogError(w, err)
		return
	}
	now := h.deps.Now()
	to, ok := queryTime(w, r, "to", now)
	if !ok {
		return
	}
	from, ok := queryTime(w, r, "from", to.Add(-24*time.Hour))
	if !ok {
		return
	}
	limit, ok := queryLimit(w, r)
	if !ok {
		return
	}
	var cursor *obslogs.Cursor
	if raw := query.Get("cursor"); raw != "" {
		if cursor, err = obslogs.ParseCursor(raw); err != nil {
			obsLogError(w, err)
			return
		}
	}
	page, err := h.obsLogReader().Search(r.Context(), site.ID, q, from, to, limit, cursor)
	if err != nil {
		obsLogError(w, err)
		return
	}
	rows := page.Rows
	if rows == nil {
		rows = []obslogs.LogRow{}
	}
	var next *string
	if page.Next != nil {
		c := page.Next.Encode()
		next = &c
	}
	writeJSON(w, http.StatusOK, map[string]any{"data": rows, "next_cursor": next})
}

// traceObsLogs serves GET .../obs/traces/{trace_id}/logs.
func (h *handler) traceObsLogs(w http.ResponseWriter, r *http.Request) {
	site, ok := h.obsLogSite(w, r)
	if !ok {
		return
	}
	id, err := hex.DecodeString(r.PathValue("trace_id"))
	if err != nil || len(id) != 16 {
		writeProblem(w, http.StatusBadRequest, "Bad request", "trace_id must be 32 hex characters")
		return
	}
	around, ok := queryTime(w, r, "around", h.deps.Now())
	if !ok {
		return
	}
	limit, ok := queryLimit(w, r)
	if !ok {
		return
	}
	rows, err := h.obsLogReader().TraceLogs(r.Context(), site.ID, id, around, limit)
	if err != nil {
		obsLogError(w, err)
		return
	}
	if rows == nil {
		rows = []obslogs.LogRow{}
	}
	writeJSON(w, http.StatusOK, map[string]any{"data": rows})
}

type obsIngestDay struct {
	Day             string `json:"day"`
	LogsAccepted    int64  `json:"logs_accepted"`
	LogsRejected    int64  `json:"logs_rejected"`
	MetricsAccepted int64  `json:"metrics_accepted"`
	MetricsRejected int64  `json:"metrics_rejected"`
}

// obsIngestHealth answers the last 7 UTC days of accepted and rejected log
// records and metric points, oldest first, with zero rows for quiet days.
func (h *handler) obsIngestHealth(w http.ResponseWriter, r *http.Request) {
	site, ok := h.obsLogSite(w, r)
	if !ok {
		return
	}
	today := h.deps.Now().UTC().Truncate(24 * time.Hour)
	days := make([]obsIngestDay, 7)
	index := map[string]int{}
	for i := range days {
		d := today.AddDate(0, 0, i-6).Format("2006-01-02")
		days[i].Day = d
		index[d] = i
	}
	rows, err := h.deps.Store.Pool.Query(r.Context(), `select to_char(day, 'YYYY-MM-DD'), class, accepted, rejected
		from obs_ingest_daily where site_id = $1 and day >= $2::date and day <= $3::date`,
		site.ID, days[0].Day, days[6].Day)
	if err != nil {
		serviceError(w, err)
		return
	}
	defer rows.Close()
	for rows.Next() {
		var day, class string
		var accepted, rejected int64
		if err := rows.Scan(&day, &class, &accepted, &rejected); err != nil {
			serviceError(w, err)
			return
		}
		i, ok := index[day]
		if !ok {
			continue
		}
		if class == "logs" {
			days[i].LogsAccepted, days[i].LogsRejected = accepted, rejected
		} else {
			days[i].MetricsAccepted, days[i].MetricsRejected = accepted, rejected
		}
	}
	if err := rows.Err(); err != nil {
		serviceError(w, err)
		return
	}
	writeJSON(w, http.StatusOK, map[string]any{"days": days})
}
