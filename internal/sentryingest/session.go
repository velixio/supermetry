package sentryingest

import (
	"crypto/sha256"
	"encoding/hex"
	"encoding/json"
	"strconv"
	"strings"
	"time"

	"supermetry/internal/appingest"
	"supermetry/internal/appsessions"
	"supermetry/internal/sites"
)

// sessionStatuses are the aggregate counters of a Sentry sessions item, in
// the order rows are produced.
var sessionStatuses = []string{"exited", "errored", "crashed", "abnormal"}

// ParseSession turns a `session` or `sessions` item payload into kind=session
// rows and session-state updates. A single session update always yields one
// appsessions.Update (every update, not only init) and, when it carries
// init=true, the v1 kind=session row the fallback path counts. A sessions
// aggregate yields one row per status with a non-zero count, Occurrences set
// to that count, never one row per session, and no Update. A crashed or
// abnormal status sets ExitReason. Row ids are digests of the payload and
// status, so a retried envelope deduplicates.
//
// The Update carries HMACs of sid and did under the app's hash secret, the
// same MAC the event writer applies. An app that has no secret yet (its first
// event has not been written) gets no Update: nothing can be hashed, and the
// v1 row still counts the session.
func ParseSession(raw []byte, ctx EventContext, site sites.Site, now time.Time) ([]appingest.Event, []appsessions.Update, error) {
	m, err := decodeObject(raw)
	if err != nil {
		return nil, nil, err
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
		return out, nil, nil
	}

	sid := clip(str(m["sid"]), 64)
	if sid == "" {
		return nil, nil, ErrInvalid
	}
	init, _ := m["init"].(bool)
	started := sessionTime(firstNonNil(m["started"], m["timestamp"]), ctx, site, now)
	did := clip(userIDString(m["did"]), 128)

	var events []appingest.Event
	if init {
		e := base
		e.ID = id("session|" + sid)
		e.TS = started
		e.SessionID = sid
		e.InstallID = did
		e.ExitReason = exitReasonOf(str(m["status"]))
		events = []appingest.Event{e}
	}

	var secret []byte
	if site.App != nil {
		secret = site.App.HashSecret
	}
	sessionHash := HashUser(secret, sid)
	if sessionHash == nil {
		return events, nil, nil
	}
	u := appsessions.Update{
		SiteID: site.ID, SessionHash: sessionHash, InstallHash: HashUser(secret, did), Started: started,
		Release: base.Release, Environment: base.Environment, Platform: base.Platform,
		Status: sessionStatus(str(m["status"])), Errors: min(max(asInt(m["errors"]), 0), 1_000_000), Init: init,
	}
	if d, ok := seconds(m["duration"]); ok && d >= 0 && d < 1e9 {
		ms := int64(d * 1000)
		u.DurationMS = &ms
	}
	return events, []appsessions.Update{u}, nil
}

func seconds(v any) (float64, bool) {
	switch t := v.(type) {
	case json.Number:
		f, err := t.Float64()
		return f, err == nil
	case float64:
		return t, true
	}
	return 0, false
}

// sessionStatus maps an SDK status onto the stored set. `unhandled` (newer
// SDKs) is a crash; anything unknown counts as ok, the lowest rank.
func sessionStatus(s string) string {
	switch s {
	case "exited", "errored", "abnormal", "crashed":
		return s
	case "unhandled":
		return "crashed"
	}
	return "ok"
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
