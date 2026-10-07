// Package rec decodes the canonical gen NDJSON stream (bench/CONTRACT.md) in
// parallel while preserving input order. Throwaway benchmark code.
package rec

import (
	"bufio"
	"encoding/json"
	"io"
	"runtime"
	"strings"
	"time"
)

// Record is one canonical event. vk/sk are ground-truth keys; incumbent loaders
// that write storage directly may use sk to build session rows (CONTRACT).
type Record struct {
	TS          string            `json:"ts"`
	Site        int               `json:"site"`
	Host        string            `json:"host"`
	Path        string            `json:"path"`
	Ref         string            `json:"ref"`
	UTMSource   string            `json:"utm_source"`
	UTMMedium   string            `json:"utm_medium"`
	UTMCampaign string            `json:"utm_campaign"`
	Name        string            `json:"name"`
	Props       map[string]string `json:"props"`
	IP          string            `json:"ip"`
	UA          string            `json:"ua"`
	ScreenW     int               `json:"screen_w"`
	Country     string            `json:"country"`
	Browser     string            `json:"browser"`
	OS          string            `json:"os"`
	Device      string            `json:"device"`
	VK          string            `json:"vk"`
	SK          string            `json:"sk"`

	Time time.Time `json:"-"` // parsed TS
	Seq  int64     `json:"-"` // 0-based line number in the stream
}

// IsPageview reports whether the record is a pageview.
func (r *Record) IsPageview() bool { return r.Name == "pageview" }

// RefHost returns the referrer's host ("" if none) and path/query.
func (r *Record) RefParts() (host, path, query string) {
	return SplitURL(r.Ref)
}

// SplitURL splits an absolute http(s) URL into host, path and query.
func SplitURL(u string) (host, path, query string) {
	if u == "" {
		return "", "", ""
	}
	if i := strings.Index(u, "://"); i >= 0 {
		u = u[i+3:]
	}
	j := strings.IndexAny(u, "/?#")
	if j < 0 {
		return u, "", ""
	}
	host, rest := u[:j], u[j:]
	if k := strings.IndexByte(rest, '#'); k >= 0 {
		rest = rest[:k]
	}
	if k := strings.IndexByte(rest, '?'); k >= 0 {
		return host, rest[:k], rest[k+1:]
	}
	return host, rest, ""
}

type chunk struct {
	lines [][]byte
	base  int64
	out   chan []Record
}

// Decode reads NDJSON from r and calls fn with consecutive batches of records,
// in input order, from a single goroutine. workers <= 0 means GOMAXPROCS-1.
func Decode(r io.Reader, workers int, fn func([]Record) error) error {
	if workers <= 0 {
		workers = max(1, runtime.GOMAXPROCS(0)-1)
	}
	const per = 4096
	work := make(chan *chunk, workers*2)
	order := make(chan *chunk, workers*4)
	errc := make(chan error, 1)
	for range workers {
		go func() {
			for c := range work {
				recs := make([]Record, len(c.lines))
				for i, l := range c.lines {
					if err := json.Unmarshal(l, &recs[i]); err != nil {
						select {
						case errc <- err:
						default:
						}
						recs = recs[:i]
						break
					}
					t, err := time.Parse(time.RFC3339Nano, recs[i].TS)
					if err != nil {
						select {
						case errc <- err:
						default:
						}
					}
					recs[i].Time = t
					recs[i].Seq = c.base + int64(i)
				}
				c.out <- recs
			}
		}()
	}
	go func() {
		defer close(work)
		defer close(order)
		br := bufio.NewReaderSize(r, 1<<20)
		var seq int64
		for {
			c := &chunk{base: seq, out: make(chan []Record, 1)}
			for len(c.lines) < per {
				l, err := br.ReadBytes('\n')
				if len(l) > 1 {
					c.lines = append(c.lines, l)
				}
				if err != nil {
					break
				}
			}
			if len(c.lines) == 0 {
				return
			}
			seq += int64(len(c.lines))
			order <- c
			work <- c
			if len(c.lines) < per {
				return
			}
		}
	}()
	for c := range order {
		recs := <-c.out
		select {
		case err := <-errc:
			return err
		default:
		}
		if err := fn(recs); err != nil {
			// drain so producers exit
			go func() {
				for c := range order {
					<-c.out
				}
			}()
			return err
		}
	}
	select {
	case err := <-errc:
		return err
	default:
	}
	return nil
}
