package ingest

import (
	"encoding/json"
	"errors"
	"fmt"
	"net/url"
	"strings"
	"time"

	"supermetry/bench/proto/internal/core"
	"supermetry/bench/proto/internal/enrich"
)

// Sites is an immutable lookup of sites by id and host.
type Sites struct {
	byID   map[int32]*core.Site
	byHost map[string]*core.Site
	list   []core.Site
}

// NewSites indexes sites.
func NewSites(list []core.Site) *Sites {
	s := &Sites{byID: map[int32]*core.Site{}, byHost: map[string]*core.Site{}, list: list}
	for i := range list {
		st := &s.list[i]
		s.byID[st.ID] = st
		s.byHost[strings.ToLower(st.Host)] = st
	}
	return s
}

// ByID returns a site or nil.
func (s *Sites) ByID(id int32) *core.Site { return s.byID[id] }

// ByHost returns a site or nil.
func (s *Sites) ByHost(h string) *core.Site {
	return s.byHost[strings.ToLower(strings.TrimPrefix(h, "www."))]
}

// All returns every site.
func (s *Sites) All() []core.Site { return s.list }

// Builder turns raw input into an Event: site, local day, salted visitor
// hash, source, enrichment. It never keeps ip or ua.
type Builder struct {
	Sites  *Sites
	Salts  *core.Salts
	Enrich *enrich.Real // nil: trust the record's parsed dimensions (bulk load)
}

// ErrBot marks events from bots (dropped, answered 202).
var ErrBot = errors.New("bot")

type input struct {
	site                      *core.Site
	ts                        time.Time
	name, path, ref           string
	utmS, utmM, utmC          string
	props                     json.RawMessage
	ip, ua                    string
	country, browser, os, dev string
}

func (b *Builder) build(in input) (*core.Event, error) {
	if in.name == "" {
		return nil, errors.New("missing event name")
	}
	if b.Enrich != nil {
		r := b.Enrich.Enrich(in.ip, in.ua)
		if r.Bot {
			return nil, ErrBot
		}
		in.country, in.browser, in.os, in.dev = r.Country, r.Browser, r.OS, r.Device
	}
	props, err := core.NormalizeProps(in.props)
	if err != nil {
		return nil, err
	}
	day := core.DayOf(in.ts, in.site.Loc)
	salt := b.Salts.Get(in.site.ID, day)
	path := in.path
	if path == "" {
		path = "/"
	}
	return &core.Event{
		SiteID: in.site.ID, Day: day, TS: in.ts.UnixMicro(),
		VisitorID: core.VisitorID(salt, in.site.ID, in.ip, in.ua),
		Name:      in.name, Path: path,
		Source:    core.Source(in.ref, in.utmS, in.site.Host),
		UTMSource: in.utmS, UTMMedium: in.utmM, UTMCampaign: in.utmC,
		Country: in.country, Browser: in.browser, OS: in.os, Device: in.dev,
		Props: props,
	}, nil
}

// FromRecord builds an event from an NDJSON record, using its ts.
func (b *Builder) FromRecord(r *core.Record) (*core.Event, error) {
	site := b.Sites.ByID(r.Site)
	if site == nil {
		return nil, fmt.Errorf("unknown site %d (import sites.json first)", r.Site)
	}
	return b.build(input{
		site: site, ts: r.TS, name: r.Name, path: r.Path, ref: r.Ref,
		utmS: r.UTMSource, utmM: r.UTMMedium, utmC: r.UTMCampaign, props: r.Props,
		ip: r.IP, ua: r.UA, country: r.Country, browser: r.Browser, os: r.OS, dev: r.Device,
	})
}

// Body is the Plausible-like JSON body of POST /api/event.
type Body struct {
	N string          `json:"n"` // event name
	U string          `json:"u"` // page URL
	D string          `json:"d"` // site domain
	R string          `json:"r"` // referrer
	P json.RawMessage `json:"p"` // props (object or JSON string)
}

// FromBody builds an event from an HTTP body, stamped with now.
func (b *Builder) FromBody(body *Body, ip, ua string, now time.Time) (*core.Event, error) {
	u, err := url.Parse(body.U)
	if err != nil || u.Host == "" {
		return nil, errors.New("bad url")
	}
	host := body.D
	if host == "" {
		host = u.Hostname()
	}
	site := b.Sites.ByHost(host)
	if site == nil {
		return nil, fmt.Errorf("unknown site %q", host)
	}
	q := u.Query()
	return b.build(input{
		site: site, ts: now, name: body.N, path: u.Path, ref: body.R,
		utmS: q.Get("utm_source"), utmM: q.Get("utm_medium"), utmC: q.Get("utm_campaign"),
		props: body.P, ip: ip, ua: ua,
	})
}
