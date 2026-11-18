package otlpingest

import (
	"crypto/sha256"
	"encoding/binary"
	"encoding/hex"
	"strconv"
	"strings"

	"supermetry/internal/appingest"
	"supermetry/internal/obslogs"
	"supermetry/internal/sentryingest"
)

const (
	// maxLogIssuesPerRequest bounds the issue events one logs request can
	// create; the rest are counted as log_issue_capped.
	maxLogIssuesPerRequest = 100

	severityError = 17 // OTLP SEVERITY_NUMBER_ERROR
	severityFatal = 21 // OTLP SEVERITY_NUMBER_FATAL
)

// logIssueEvents turns the error-level records of one request into app events,
// so error logs group into issues like span exceptions. A record qualifies at
// severity ERROR or above, or when it carries exception.type or
// exception.message and no severity. At most limit events are returned; the
// number of qualifying records beyond that is capped.
func logIssueEvents(records []obslogs.Record, limit int) (events []appingest.Event, capped int) {
	for _, r := range records {
		a := r.Attributes
		attrType, _ := a["exception.type"].(string)
		attrMsg, _ := a["exception.message"].(string)
		if r.Severity < severityError && !(r.Severity == 0 && (attrType != "" || attrMsg != "")) {
			continue
		}
		if len(events) >= limit {
			capped++
			continue
		}
		typ := clip(attrType, 64)
		msg := attrMsg
		if msg == "" {
			msg = firstLine(r.Body)
		}
		msg = sentryingest.ScrubText(clip(msg, 1000))
		title := msg
		if typ != "" {
			title = typ + ": " + msg
		}
		if title == "" || title == typ+": " {
			title = typ
		}
		if title == "" {
			title = "Error log"
		}
		title = clip(title, 200)
		level := "error"
		if r.Severity >= severityFatal {
			level = "fatal"
		}
		stack, _ := a["exception.stacktrace"].(string)
		ev := appingest.Event{
			TS:          r.TS,
			Kind:        "error",
			Level:       level,
			Handled:     true,
			Release:     r.Release,
			Environment: r.Environment,
			ErrorType:   typ,
			Frames:      appingest.ScrubFrames(firstLines(stack, maxStackLines)),
			Transaction: r.Service,
			TraceID:     append([]byte(nil), r.TraceID...),
			SpanID:      append([]byte(nil), r.SpanID...),
			SDKName:     "otlp-logs",
			Title:       title,
			Occurrences: 1,
		}
		body := appingest.Body{Message: msg}
		if typ != "" {
			body.Exceptions = []appingest.Exception{{Type: typ, Value: msg}}
		}
		ev.Fingerprint, ev.GroupingVersion = appingest.GroupingKey(ev, body, nil)
		sum := sha256.Sum256([]byte(title))
		ev.MessageHash = sum[:]
		if typ != "" {
			ev.ID = exceptionEventID(r.TraceID, r.SpanID, typ, msg)
		}
		if ev.ID == "" {
			h := sha256.New()
			h.Write([]byte("log\x00"))
			var n [8]byte
			binary.BigEndian.PutUint64(n[:], uint64(r.TS.UnixNano()))
			h.Write(n[:])
			h.Write([]byte(r.Service + "\x00" + strconv.Itoa(int(r.Severity)) + "\x00" + r.Body))
			ev.ID = hex.EncodeToString(h.Sum(nil)[:16])
		}
		events = append(events, ev)
	}
	return events, capped
}

// exceptionEventID identifies one exception on one span, so the span's
// exception event and a log record carrying the same exception and ids share
// an event ID and the intake keeps one occurrence. It is empty when either id
// is missing.
func exceptionEventID(traceID, spanID []byte, typ, msg string) string {
	if len(traceID) == 0 || len(spanID) == 0 {
		return ""
	}
	h := sha256.New()
	h.Write(traceID)
	h.Write([]byte{0})
	h.Write(spanID)
	h.Write([]byte{0})
	h.Write([]byte(typ))
	h.Write([]byte{0})
	h.Write([]byte(msg))
	return hex.EncodeToString(h.Sum(nil)[:16])
}

func firstLine(s string) string {
	if i := strings.IndexByte(s, '\n'); i >= 0 {
		return s[:i]
	}
	return s
}
