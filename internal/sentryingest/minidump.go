package sentryingest

import (
	"bytes"
	"crypto/rand"
	"encoding/hex"
	"encoding/json"
	"errors"
	"io"
	"mime"
	"mime/multipart"
	"net/http"
	"strings"
	"time"

	"supermetry/internal/clientip"
	"supermetry/internal/dropstats"
)

// Limits of the /minidump/ form. The whole decoded body is capped at the
// largest dump plus the JSON event plus room for the other fields.
const (
	minidumpFormExtra   = 256 << 10
	maxMinidumpBody     = MaxMinidump + MaxEventBytes + minidumpFormExtra
	maxMinidumpFields   = 64 << 10 // read bytes of the allowlisted fields, apart from the sentry JSON
	maxMinidumpParts    = 100
	maxMinidumpFieldLen = 1 << 10
	minidumpFileField   = "upload_file_minidump"
	// maxMinidumpRequests bounds the /minidump/ bodies held in memory at once:
	// each can take up to twice the dump size while it is parsed.
	maxMinidumpRequests = 4
)

var minidumpSlots = make(chan struct{}, maxMinidumpRequests)

var errMinidumpForm = errors.New("malformed minidump form")

// minidumpForm is what the allowlisted parts of a Crashpad upload hold.
type minidumpForm struct {
	file   []byte
	sentry []byte            // the "sentry" JSON event
	fields map[string]string // sentry[...] and Crashpad annotations, by field name
}

// parseMinidumpForm reads the multipart body. Only the dump, the sentry event
// and a few allowlisted fields are read; every other part is skipped unread.
// A dump over MaxMinidump is ErrTooLarge.
func parseMinidumpForm(body []byte, boundary string) (minidumpForm, error) {
	f := minidumpForm{fields: map[string]string{}}
	mr := multipart.NewReader(bytes.NewReader(body), boundary)
	fieldBytes := 0
	for i := 0; ; i++ {
		part, err := mr.NextPart()
		if err == io.EOF {
			return f, nil
		}
		if err != nil || i >= maxMinidumpParts {
			return f, errMinidumpForm
		}
		name := part.FormName()
		switch {
		case name == minidumpFileField:
			if f.file != nil {
				continue
			}
			data, err := io.ReadAll(io.LimitReader(part, MaxMinidump+1))
			if err != nil {
				return f, errMinidumpForm
			}
			if len(data) > MaxMinidump {
				return f, ErrTooLarge
			}
			f.file = data
		case name == "sentry":
			data, err := io.ReadAll(io.LimitReader(part, MaxEventBytes+1))
			if err != nil {
				return f, errMinidumpForm
			}
			if len(data) <= MaxEventBytes {
				f.sentry = data
			}
		case minidumpFieldAllowed(name):
			data, err := io.ReadAll(io.LimitReader(part, maxMinidumpFieldLen))
			if err != nil {
				return f, errMinidumpForm
			}
			if fieldBytes += len(data); fieldBytes <= maxMinidumpFields {
				f.fields[name] = string(data)
			}
		}
	}
}

func minidumpFieldAllowed(name string) bool {
	switch name {
	case "prod", "ver", "ptype", "platform", "sentry[release]", "sentry[environment]", "sentry[dist]", "sentry[user][id]":
		return true
	}
	if k, ok := strings.CutPrefix(name, "sentry[tags]["); ok {
		k, ok = strings.CutSuffix(k, "]")
		return ok && k != "" && len(k) <= 64 && !strings.ContainsAny(k, "[]")
	}
	return false
}

// minidumpEvent builds the event JSON of an upload: the sentry JSON when sent,
// overlaid with the bracket fields and the Crashpad annotations.
func minidumpEvent(f minidumpForm, id string) ([]byte, error) {
	m := map[string]any{}
	if len(f.sentry) > 0 {
		if err := json.Unmarshal(f.sentry, &m); err != nil || m == nil {
			m = map[string]any{}
		}
	}
	m["event_id"] = id
	if _, ok := m["platform"]; !ok {
		m["platform"] = "native"
	}
	m["level"] = "fatal"
	set := func(key, field string) {
		if v := strings.TrimSpace(f.fields[field]); v != "" {
			m[key] = v
		}
	}
	set("release", "sentry[release]")
	set("environment", "sentry[environment]")
	set("dist", "sentry[dist]")
	if _, ok := m["release"]; !ok {
		set("release", "ver")
	}
	tags := tagPairs(m["tags"])
	for name, v := range f.fields {
		if k, ok := strings.CutPrefix(name, "sentry[tags]["); ok {
			tags[strings.TrimSuffix(k, "]")] = v
		}
	}
	if v := strings.TrimSpace(f.fields["ptype"]); v != "" {
		if _, ok := tags["process"]; !ok {
			tags["process"] = v
		}
	}
	if len(tags) > 0 {
		m["tags"] = tags
	}
	if v := strings.TrimSpace(f.fields["sentry[user][id]"]); v != "" {
		u, _ := m["user"].(map[string]any)
		if u == nil {
			u = map[string]any{}
		}
		u["id"] = v
		m["user"] = u
	}
	ctxs, _ := m["contexts"].(map[string]any)
	if ctxs == nil {
		ctxs = map[string]any{}
	}
	if v := strings.TrimSpace(f.fields["prod"]); v != "" {
		app, _ := ctxs["app"].(map[string]any)
		if app == nil {
			app = map[string]any{}
		}
		if _, ok := app["app_name"]; !ok {
			app["app_name"] = v
		}
		ctxs["app"] = app
	}
	if v := strings.TrimSpace(f.fields["platform"]); v != "" {
		if _, ok := ctxs["os"]; !ok {
			ctxs["os"] = map[string]any{"name": v}
		}
	}
	if len(ctxs) > 0 {
		m["contexts"] = ctxs
	}
	return json.Marshal(m)
}

func newEventID() string {
	var b [16]byte
	_, _ = rand.Read(b[:])
	return hex.EncodeToString(b[:])
}

func hyphenate(id string) string {
	if len(id) != 32 {
		return id
	}
	return id[:8] + "-" + id[8:12] + "-" + id[12:16] + "-" + id[16:20] + "-" + id[20:]
}

func (h *Handler) minidumpFail(w http.ResponseWriter, status int, detail string) {
	w.Header().Set("X-Sentry-Error", detail)
	w.Header().Set("Content-Type", "text/plain; charset=utf-8")
	w.WriteHeader(status)
	_, _ = io.WriteString(w, detail+"\n")
}

// serveMinidump handles POST /api/{id}/minidump/: a Crashpad-style multipart
// upload. It authenticates by DSN key before reading the body, caps the body
// before and after decompression, and sends the dump through the same
// symbolication stage as an envelope attachment. CORS is not offered.
func (h *Handler) serveMinidump(w http.ResponseWriter, r *http.Request, rt route) {
	hdr := w.Header()
	now := h.now()
	if r.Method != http.MethodPost {
		hdr.Set("Allow", "POST")
		h.minidumpFail(w, http.StatusMethodNotAllowed, "method not allowed")
		return
	}
	if !h.ips.allow(h.clientKey(r), now) {
		h.stats.Add("ip_rate_limited", 1)
		hdr.Set("Retry-After", "60")
		h.minidumpFail(w, http.StatusTooManyRequests, "rate limited")
		return
	}
	// A key travels in the query or a header; there is no envelope header to
	// carry one, so an unauthenticated body is never read.
	site, key, err := Authenticate(r, EnvelopeHeader{}, rt.siteID, h.sites, now)
	if err != nil {
		switch {
		case errors.Is(err, ErrVersion):
			h.minidumpFail(w, http.StatusBadRequest, "unsupported sentry_version")
		case errors.Is(err, ErrAuthMissing):
			h.minidumpFail(w, http.StatusUnauthorized, "missing DSN key")
		default:
			h.minidumpFail(w, http.StatusUnauthorized, "invalid DSN key")
		}
		return
	}
	mediaType, params, err := mime.ParseMediaType(r.Header.Get("Content-Type"))
	if err != nil || mediaType != "multipart/form-data" || params["boundary"] == "" {
		h.minidumpFail(w, http.StatusBadRequest, "expected multipart/form-data")
		return
	}
	select {
	case minidumpSlots <- struct{}{}:
	default:
		h.stats.Add("inflight_full", 1)
		hdr.Set("Retry-After", "5")
		h.minidumpFail(w, http.StatusServiceUnavailable, "too many requests in flight")
		return
	}
	form, err := func() (minidumpForm, error) {
		defer func() { <-minidumpSlots }()
		body, err := decodeBodyCaps(r, maxMinidumpBody, maxMinidumpBody)
		if err != nil {
			return minidumpForm{}, err
		}
		return parseMinidumpForm(body, params["boundary"])
	}()
	switch {
	case errors.Is(err, ErrTooLarge):
		h.stats.Add("minidump_too_large", 1)
		h.minidumpFail(w, http.StatusRequestEntityTooLarge, "minidump too large")
		return
	case err != nil:
		h.minidumpFail(w, http.StatusBadRequest, "malformed upload")
		return
	case form.file == nil:
		h.minidumpFail(w, http.StatusBadRequest, "missing "+minidumpFileField)
		return
	case !bytes.HasPrefix(form.file, []byte("MDMP")):
		h.stats.Add("minidump_invalid", 1)
		h.minidumpFail(w, http.StatusBadRequest, "not a minidump")
		return
	}

	id := newEventID()
	payload, err := minidumpEvent(form, id)
	if err != nil || len(payload) > MaxEventBytes {
		h.minidumpFail(w, http.StatusBadRequest, "invalid event")
		return
	}
	if !h.spike.take(site.ID, 1, now) {
		h.intake.Drop(site, now, dropstats.AppRateLimited)
		h.stats.Add("spike_protection", 1)
		hdr.Set("Retry-After", "60")
		h.minidumpFail(w, http.StatusTooManyRequests, "spike protection")
		return
	}
	req := RequestMeta{UserAgent: r.UserAgent(), SDKClient: collectAuth(r, EnvelopeHeader{}).client}
	if ip, ok := clientip.Resolve(r, h.trusted); ok {
		req.ClientIP = ip
		if h.geo != nil {
			req.Country = h.geo.Country(ip)
		}
	}
	env := Envelope{
		Header: EnvelopeHeader{EventID: id, SDK: SDKInfo{Name: "minidump.crashpad"}, SentAt: time.Time{}},
		Items: []Item{
			{Type: "event", ContentType: "application/json", Payload: payload},
			{Type: "attachment", AttachmentType: "event.minidump", Filename: "upload_file_minidump.dmp", Payload: form.file},
		},
	}
	form = minidumpForm{}
	out := h.process(site, key, env, nil, req, now)
	switch {
	case out.accepted == 0 && out.queueFull > 0:
		hdr.Set("Retry-After", "5")
		h.minidumpFail(w, http.StatusServiceUnavailable, "ingest queue full")
	case out.accepted == 0 && out.rateLimited > 0:
		hdr.Set("Retry-After", "60")
		h.minidumpFail(w, http.StatusTooManyRequests, "rate limited")
	default:
		hdr.Set("Content-Type", "text/plain; charset=utf-8")
		w.WriteHeader(http.StatusOK)
		_, _ = io.WriteString(w, hyphenate(id))
	}
}
