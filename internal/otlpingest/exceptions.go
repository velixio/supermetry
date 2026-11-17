package otlpingest

import (
	"crypto/sha256"
	"encoding/binary"
	"encoding/hex"
	"strings"
	"time"

	tracepb "go.opentelemetry.io/proto/otlp/trace/v1"

	"supermetry/internal/appingest"
	"supermetry/internal/sentryingest"
	"supermetry/internal/spans"
)

const maxStackLines = 50

// exceptionEvents turns the "exception" events of a span into app events so
// they group into issues like Sentry errors. At most limit are returned. The
// message and stack are scrubbed; the grouping key is the exception type plus
// a digit-normalised message template.
func exceptionEvents(sp *tracepb.Span, res resourceInfo, s spans.Span, limit int) []appingest.Event {
	var out []appingest.Event
	for i, ev := range sp.Events {
		if len(out) >= limit || i >= maxEventsPerSpan {
			break
		}
		if ev == nil || ev.Name != "exception" {
			continue
		}
		a := indexAttrs(ev.Attributes)
		typ := clip(a.str("exception.type"), 64)
		msg := sentryingest.ScrubText(clip(a.str("exception.message"), 1000))
		if typ == "" && msg == "" {
			continue
		}
		if typ == "" {
			typ = "Error"
		}
		title := typ
		if msg != "" {
			title = clip(typ+": "+msg, 200)
		}
		e := appingest.Event{
			ID:          eventID(s, i),
			TS:          s.Start,
			Kind:        "error",
			Level:       "error",
			Handled:     true,
			Release:     s.Release,
			Environment: s.Environment,
			Platform:    res.eventPlatform(),
			ErrorType:   typ,
			Frames:      appingest.ScrubFrames(firstLines(a.str("exception.stacktrace"), maxStackLines)),
			Transaction: s.Name,
			TraceID:     append([]byte(nil), s.TraceID[:]...),
			SpanID:      append([]byte(nil), s.SpanID[:]...),
			SDKName:     "opentelemetry",
			Title:       title,
			Occurrences: 1,
		}
		if ev.TimeUnixNano > 0 && ev.TimeUnixNano <= 1<<63-1 {
			if t := time.Unix(0, int64(ev.TimeUnixNano)).UTC(); !t.Before(s.Start) {
				e.TS = t
			}
		}
		body := appingest.Body{Exceptions: []appingest.Exception{{Type: typ, Value: msg}}, Message: msg}
		e.Fingerprint, e.GroupingVersion = appingest.GroupingKey(e, body, nil)
		sum := sha256.Sum256([]byte(title))
		e.MessageHash = sum[:]
		out = append(out, e)
	}
	return out
}

// eventID is stable for one exception event of one span, so a re-sent request
// is deduplicated by the intake.
func eventID(s spans.Span, index int) string {
	h := sha256.New()
	h.Write(s.TraceID[:])
	h.Write(s.SpanID[:])
	var n [8]byte
	binary.BigEndian.PutUint64(n[:], uint64(index))
	h.Write(n[:])
	return hex.EncodeToString(h.Sum(nil)[:16])
}

func firstLines(s string, n int) string {
	if n <= 0 || s == "" {
		return ""
	}
	idx := 0
	for i := 0; i < n; i++ {
		j := strings.IndexByte(s[idx:], '\n')
		if j < 0 {
			return s
		}
		idx += j + 1
	}
	return s[:idx]
}
