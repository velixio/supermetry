package sentryingest

import (
	"crypto/sha256"
	"encoding/hex"
	"strconv"
	"strings"
	"time"

	"supermetry/internal/appingest"
	"supermetry/internal/sites"
)

// sessionStatuses are the aggregate counters of a Sentry sessions item, in
// the order rows are produced.
var sessionStatuses = []string{"exited", "errored", "crashed", "abnormal"}

// ParseSession turns a `session` or `sessions` item payload into kind=session
// rows. A session update yields one row when it carries init=true (later
// updates of the same session add nothing in 1a). A sessions aggregate yields
// one row per status with a non-zero count, Occurrences set to that count,
// never one row per session. A crashed or abnormal status sets ExitReason.
// Row ids are digests of the payload and status, so a retried envelope
// deduplicates.
func ParseSession(raw []byte, ctx EventContext, site sites.Site, now time.Time) ([]appingest.Event, error) {
	m, err := decodeObject(raw)
	if err != nil {
		return nil, err
	}
	attrs := asMap(m["attrs"])
	base := appingest.Event{
		Kind:        "session",
		Release:     clip(str(attrs["release"]), 64),
		Environment: clip(str(attrs["environment"]), 32),
		Occurrences: 1,
	}
	ua := ctx.UserAgent
	if ua == "" {
		ua = str(attrs["user_agent"])
	}
	client := parseUA(ua)
	osName := ""
	if client != nil {
		osName = client.OS
		base.OSVersion = clip(client.OSVersion, 32)
	}
	base.Platform = platformOf(osName, "", client != nil, ctx.SDKName)
	digest := sha256.Sum256(raw)
	id := func(suffix string) string {
		sum := sha256.Sum256([]byte(hex.EncodeToString(digest[:]) + "|" + suffix))
		return hex.EncodeToString(sum[:16])
	}

	if aggs := asList(m["aggregates"]); aggs != nil || m["aggregates"] != nil {
		var out []appingest.Event
		for i, a := range aggs {
			am := asMap(a)
			if am == nil {
				continue
			}
			ts := sessionTime(am["started"], ctx, site, now)
			for _, status := range sessionStatuses {
				n := asInt(am[status])
				if n < 1 {
					continue
				}
				e := base
				e.ID = id(strconv.Itoa(i) + "|" + status)
				e.TS = ts
				e.Occurrences = min(n, 1_000_000)
				e.ExitReason = exitReasonOf(status)
				out = append(out, e)
			}
		}
		return out, nil
	}

	if init, _ := m["init"].(bool); !init {
		return nil, nil
	}
	sid := clip(str(m["sid"]), 64)
	if sid == "" {
		return nil, ErrInvalid
	}
	e := base
	e.ID = id("session|" + sid)
	e.TS = sessionTime(firstNonNil(m["started"], m["timestamp"]), ctx, site, now)
	e.SessionID = sid
	e.InstallID = clip(userIDString(m["did"]), 128)
	e.ExitReason = exitReasonOf(str(m["status"]))
	return []appingest.Event{e}, nil
}

func firstNonNil(vals ...any) any {
	for _, v := range vals {
		if v != nil {
			return v
		}
	}
	return nil
}

func exitReasonOf(status string) string {
	switch status {
	case "crashed", "abnormal":
		return status
	}
	return ""
}

// sessionTime is the session start with the same skew and future handling as
// events; an out-of-range value falls back to now. Age is left to the intake.
func sessionTime(v any, ctx EventContext, site sites.Site, now time.Time) time.Time {
	ts, ok := parseTime(v)
	if !ok {
		return now
	}
	if !ctx.SentAt.IsZero() {
		if skew := now.Sub(ctx.SentAt); skew > skewCorrectAfter || skew < -skewCorrectAfter {
			ts = ts.Add(skew)
		}
	}
	if ts.After(now.Add(futureSkew)) {
		return now
	}
	return ts
}

// ParseClientReport reads the discarded-event counts of a client report.
// Entries with a non-positive quantity are skipped; counts of the same reason
// and category are summed.
func ParseClientReport(raw []byte) ([]ClientReportCount, error) {
	m, err := decodeObject(raw)
	if err != nil {
		return nil, err
	}
	type key struct{ reason, category string }
	sums := map[key]int64{}
	var order []key
	for _, d := range asList(m["discarded_events"]) {
		dm := asMap(d)
		if dm == nil {
			continue
		}
		q := int64(asInt(dm["quantity"]))
		if q < 1 {
			continue
		}
		k := key{token(str(dm["reason"]), 64), token(str(dm["category"]), 32)}
		if k.reason == "" || k.category == "" {
			continue
		}
		if _, seen := sums[k]; !seen {
			order = append(order, k)
		}
		sums[k] = min(sums[k]+q, 1_000_000_000)
	}
	out := make([]ClientReportCount, 0, len(order))
	for _, k := range order {
		out = append(out, ClientReportCount{Reason: k.reason, Category: k.category, Quantity: sums[k]})
	}
	return out, nil
}

func token(s string, limit int) string {
	return clip(reToken.ReplaceAllString(strings.TrimSpace(s), "_"), limit)
}
