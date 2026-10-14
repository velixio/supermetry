package opencounters

import (
	"context"
	"fmt"
	"html/template"
	"net/http"
	"strings"
	"time"

	"github.com/jackc/pgx/v5/pgxpool"

	"supermetry/internal/pixels"
)

type DayCount struct {
	Day   string
	Views int64
}

type StatsFunc func(context.Context, string, string, string) ([]DayCount, error)

func PoolStats(pool *pgxpool.Pool) StatsFunc {
	return func(ctx context.Context, key, from, to string) ([]DayCount, error) {
		rows, err := pool.Query(ctx, `select v.day::text,v.views from open_counter_views_daily v
			join open_counters c on c.id=v.counter_id
			where c.key=$1 and v.day between $2::date and $3::date order by v.day`, key, from, to)
		if err != nil {
			return nil, err
		}
		defer rows.Close()
		var out []DayCount
		for rows.Next() {
			var day DayCount
			if err := rows.Scan(&day.Day, &day.Views); err != nil {
				return nil, err
			}
			out = append(out, day)
		}
		return out, rows.Err()
	}
}

func RenderSparkline(days []DayCount, width, height int) string {
	max := int64(1)
	for _, day := range days {
		if day.Views > max {
			max = day.Views
		}
	}
	points := make([]string, 0, len(days))
	for i, day := range days {
		x := 0
		if len(days) > 1 {
			x = i * width / (len(days) - 1)
		}
		y := height - int(day.Views*int64(height-2)/max) - 1
		points = append(points, fmt.Sprintf("%d,%d", x, y))
	}
	return fmt.Sprintf(`<svg xmlns="http://www.w3.org/2000/svg" width="%d" height="%d" viewBox="0 0 %d %d" role="img" aria-label="Views over the last 30 days"><polyline fill="none" stroke="#5b3cf0" stroke-width="2" points="%s"/></svg>`, width, height, width, height, strings.Join(points, " "))
}

var pageTmpl = template.Must(template.New("page").Parse(`<!doctype html>
<html lang="en"><head><meta charset="utf-8"><meta name="viewport" content="width=device-width,initial-scale=1">
<title>{{.Key}} views</title>
<style>body{font:16px/1.5 system-ui,sans-serif;max-width:40rem;margin:3rem auto;padding:0 16px;color:#1c1a2b;background:#fbfbfd}
@media (prefers-color-scheme:dark){body{color:#f4f3f8;background:#151320}}h1{font-size:1.25rem;word-break:break-all}.total{font-size:2.5rem;font-weight:700}small{opacity:.7}</style>
</head><body><h1>{{.Key}}</h1><p class="total">{{.Total}}</p><p><small>views, not unique visitors. Last 30 days:</small></p>
{{.Sparkline}}
<p><a href="{{.Homepage}}/?ref=counter">Track your own site with Supermetry</a></p></body></html>`))

func (h *Handler) servePage(w http.ResponseWriter, r *http.Request, key string) {
	if h.Stats == nil || h.Blocklist[key] {
		http.NotFound(w, r)
		return
	}
	today := h.Now().UTC()
	from, to := today.AddDate(0, 0, -29).Format(time.DateOnly), today.Format(time.DateOnly)
	days, err := h.Stats(r.Context(), key, from, to)
	if err != nil {
		http.Error(w, "temporarily unavailable", http.StatusServiceUnavailable)
		return
	}
	w.Header().Set("Content-Type", "text/html; charset=utf-8")
	w.Header().Set("Cache-Control", "public, max-age=300")
	w.Header().Set("Content-Security-Policy", "default-src 'none'; style-src 'unsafe-inline'; img-src data:")
	_ = pageTmpl.Execute(w, map[string]any{
		"Key": key, "Total": pixels.FormatCount(h.Recorder.Total(key)),
		"Sparkline": template.HTML(RenderSparkline(days, 300, 40)), // #nosec G203 -- constructed solely from integers.
		"Homepage":  strings.TrimRight(h.HomepageURL, "/"),
	})
}
