package sentryingest

import (
	"errors"
	"time"

	"supermetry/internal/dropstats"
	"supermetry/internal/salts"
	"supermetry/internal/sites"
	"supermetry/internal/spans"
)

type spanParser func(raw []byte, ctx EventContext, site sites.Site, now time.Time, st *SpanStats) ([]spans.Span, error)

// ingestSpans parses one transaction or span container item and hands its
// spans to the writer. dedupID, when set, is the envelope event id of a
// transaction: a retry of the same transaction is stored once. Spans are not
// events: they never touch the spike guard or the per-key event budget.
func (h *Handler) ingestSpans(out *outcome, site sites.Site, it Item, ctx EventContext, now time.Time, dedupID string, parse spanParser) {
	if len(it.Payload) > MaxEventBytes {
		h.stats.Add("too_large", 1)
		h.intake.Drop(site, now, dropstats.AppInvalid)
		return
	}
	dedupKey := ""
	if dedupID != "" {
		dedupKey = "tx:" + dedupID
		if h.intake.Seen(site.ID, dedupKey, now) {
			h.intake.Drop(site, now, dropstats.AppDuplicate)
			h.stats.Add("transaction_duplicate", 1)
			return
		}
	}
	forget := func() {
		if dedupKey != "" {
			h.intake.Forget(site.ID, dedupKey)
		}
	}
	var st SpanStats
	batch, err := parse(it.Payload, ctx, site, now, &st)
	h.countSpanStats(&st)
	if err != nil {
		forget()
		if errors.Is(err, ErrTooOld) {
			h.intake.Drop(site, now, dropstats.TooOld)
			h.stats.Add("span_too_old", 1)
		} else {
			h.intake.Drop(site, now, dropstats.AppInvalid)
			h.stats.Add("span_item_invalid", 1)
		}
		return
	}
	if h.names != nil {
		day := salts.LocalDay(now, site.TZ)
		for i := range batch {
			if !batch[i].IsSegment {
				continue
			}
			name, over := h.names.Admit(site.ID, day, batch[i].Name)
			batch[i].Name = name
			if over {
				h.stats.Add("transaction_name_overflow", 1)
			}
		}
	}
	n := h.budget.Take(site.ID, len(batch), now)
	if n < len(batch) {
		dropped := len(batch) - n
		h.stats.Add("span_rate_limited", uint64(dropped))
		spans.HealthStats{}.Add("span_rate_limited", dropped)
		out.spanLimited++
		if n == 0 {
			forget()
			out.rateLimited++
			return
		}
		batch = batch[:n]
	}
	if !h.spans.Enqueue(batch) {
		forget()
		out.queueFull++
		return
	}
	out.accepted++
}

func (h *Handler) countSpanStats(st *SpanStats) {
	h.stats.Add("span_clamped", uint64(st.Clamped))
	h.stats.Add("span_truncated", uint64(st.Truncated))
	h.stats.Add("span_invalid", uint64(st.Invalid))
}
