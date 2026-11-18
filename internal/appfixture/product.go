package appfixture

import (
	"context"
	"encoding/json"
	"fmt"
	"testing"
	"time"

	"github.com/jackc/pgx/v5/pgxpool"

	"supermetry/internal/appproduct"
	"supermetry/internal/db"
	"supermetry/internal/dict"
	"supermetry/internal/testpg"
)

// ProductSecret is the hash secret of the product fixture's site.
var ProductSecret = []byte("0123456789abcdef0123456789abcdef")

// ProductHit is one screen view (Screen) or custom event (Event) of an install.
type ProductHit struct {
	Install      string
	At           time.Time
	Screen       string // "/home"
	Event        string // "purchase"
	Release      string
	Platform     string
	Country      string
	OS           string
	Device       int16
	EngagementMS int
}

// Product is a migrated database with one app site (id 7, retention 90, hash
// secret ProductSecret) that holds product hits in the web tables.
type Product struct {
	Pool   *pgxpool.Pool
	SiteID int32
	Loc    *time.Location
	// Now is the dataset's clock, 2026-10-09T12:00Z.
	Now time.Time

	sessions map[int64]*productSession
}

type productSession struct {
	visitor     int64
	start, last time.Time
	screens     int
	events      int
	engagement  int
	country     string
	osID        int32
	device      int16
	hostnameID  int32
}

// NewProduct builds the database and the empty site in the time zone zone.
func NewProduct(t testing.TB, zone string) *Product {
	t.Helper()
	pool, _ := testpg.New(t)
	ctx := context.Background()
	if err := db.Migrate(ctx, pool); err != nil {
		t.Fatal(err)
	}
	loc, err := time.LoadLocation(zone)
	if err != nil {
		t.Fatal(err)
	}
	if _, err := pool.Exec(ctx, `insert into orgs(name,slug) values('Org','org')`); err != nil {
		t.Fatal(err)
	}
	if _, err := pool.Exec(ctx, `insert into sites(id,org_id,public_id,name,timezone,retention_class,kind) overriding system value
values(7,1,'appprod00001','App',$1,90,'app')`, zone); err != nil {
		t.Fatal(err)
	}
	if _, err := pool.Exec(ctx, `insert into app_hash_secrets(site_id,secret) values (7,$1)`, ProductSecret); err != nil {
		t.Fatal(err)
	}
	if err := db.EnsurePartitions(ctx, pool, time.Date(2026, 9, 25, 0, 0, 0, 0, time.UTC), time.Date(2026, 11, 5, 0, 0, 0, 0, time.UTC)); err != nil {
		t.Fatal(err)
	}
	return &Product{Pool: pool, SiteID: 7, Loc: loc, Now: time.Date(2026, 10, 9, 12, 0, 0, 0, time.UTC), sessions: map[int64]*productSession{}}
}

// Visitor is the install's visitor id, as the product adapter derives it.
func (p *Product) Visitor(install string) int64 {
	return appproduct.VisitorID(ProductSecret, install)
}

func (p *Product) dictID(t testing.TB, kind dict.Kind, value string) int32 {
	t.Helper()
	if value == "" {
		return 0
	}
	var id int32
	if err := p.Pool.QueryRow(context.Background(), `insert into dict(kind,value) values($1,$2)
on conflict (kind,value) do update set value = excluded.value returning id`, int16(kind), value).Scan(&id); err != nil {
		t.Fatal(err)
	}
	return id
}

// Add inserts the hits as events and keeps one session per install per local
// day, rewritten after every call, so a late hit can join a day added before.
func (p *Product) Add(t testing.TB, hits ...ProductHit) {
	t.Helper()
	ctx := context.Background()
	touched := map[int64]bool{}
	for _, h := range hits {
		visitor := p.Visitor(h.Install)
		local := h.At.In(p.Loc)
		localDay := time.Date(local.Year(), local.Month(), local.Day(), 0, 0, 0, 0, time.UTC)
		sid := visitor ^ (localDay.Unix() / 86400)
		kind := int16(0)
		path, name := int32(0), int32(0)
		if h.Event != "" {
			kind = 1
			name = p.dictID(t, dict.EventName, h.Event)
		} else {
			path = p.dictID(t, dict.Path, h.Screen)
		}
		osID := p.dictID(t, dict.OS, h.OS)
		hostID := p.dictID(t, dict.Hostname, h.Platform)
		props := map[string]string{}
		if h.Release != "" {
			props["$release"] = h.Release
		}
		raw, err := json.Marshal(props)
		if err != nil {
			t.Fatal(err)
		}
		if _, err := p.Pool.Exec(ctx, `insert into events(site_id,retention_class,local_day,ts,visitor_id,session_id,kind,name_id,path_id,
country,os_id,device,engagement_ms,hostname_id,props) values($1,90,$2,$3,$4,$5,$6,$7,$8,$9,$10,$11,$12,$13,$14)`,
			p.SiteID, localDay, h.At, visitor, sid, kind, name, path, h.Country, osID, h.Device, h.EngagementMS, hostID, raw); err != nil {
			t.Fatalf("insert hit %+v: %v", h, err)
		}
		s := p.sessions[sid]
		if s == nil {
			s = &productSession{visitor: visitor, start: h.At, last: h.At}
			p.sessions[sid] = s
		}
		if h.At.Before(s.start) {
			s.start = h.At
		}
		if h.At.After(s.last) {
			s.last = h.At
		}
		if kind == 0 {
			s.screens++
		} else {
			s.events++
		}
		s.engagement += h.EngagementMS
		s.country, s.osID, s.device, s.hostnameID = h.Country, osID, h.Device, hostID
		touched[sid] = true
	}
	for sid := range touched {
		s := p.sessions[sid]
		local := s.start.In(p.Loc)
		localDay := time.Date(local.Year(), local.Month(), local.Day(), 0, 0, 0, 0, time.UTC)
		if _, err := p.Pool.Exec(ctx, `delete from sessions where site_id = $1 and session_id = $2`, p.SiteID, sid); err != nil {
			t.Fatal(err)
		}
		if _, err := p.Pool.Exec(ctx, `insert into sessions(site_id,retention_class,session_id,visitor_id,start,last,local_day,country,os_id,device,
pageviews,events,engagement_ms,bounce,hostname_id) values($1,90,$2,$3,$4,$5,$6,$7,$8,$9,$10,$11,$12,$13,$14)`,
			p.SiteID, sid, s.visitor, s.start, s.last, localDay, s.country, s.osID, s.device,
			s.screens, s.events, s.engagement, s.screens+s.events <= 1, s.hostnameID); err != nil {
			t.Fatal(err)
		}
	}
}

// at builds a UTC time on 2026-10-DD at HH:MM.
func at(day, hour, minute int) time.Time {
	return time.Date(2026, 10, day, hour, minute, 0, 0, time.UTC)
}

// AddDataset inserts the plan's hand-computed product dataset (phase 3,
// installs A and B on ios, C and D on android; release 1.0.0 except A on 10-08
// and 10-09; DE for A and B, US for C and D). The late hits of D (sent 10-09,
// made 10-06) are inserted only when late is true.
func (p *Product) AddDataset(t testing.TB, late bool) {
	t.Helper()
	hit := func(install string, d, h, m int, screen, event string) ProductHit {
		x := ProductHit{Install: install, At: at(d, h, m), Screen: screen, Event: event, Release: "1.0.0"}
		switch install {
		case "A", "B":
			x.Platform, x.Country, x.OS, x.Device = "ios", "DE", "iOS", 1
		default:
			x.Platform, x.Country, x.OS, x.Device = "android", "US", "Android", 1
		}
		if install == "A" && d >= 8 {
			x.Release = "1.1.0"
		}
		return x
	}
	p.Add(t,
		hit("A", 6, 10, 0, "/home", ""), hit("A", 6, 10, 1, "/paywall", ""), hit("B", 6, 11, 0, "/home", ""),
		hit("A", 7, 10, 0, "/home", ""), hit("A", 7, 10, 2, "", "purchase"),
		hit("C", 7, 12, 0, "/home", ""), hit("C", 7, 12, 1, "/paywall", ""), hit("C", 7, 12, 2, "", "purchase"),
		hit("A", 8, 10, 0, "/home", ""), hit("B", 8, 11, 0, "/home", ""),
		hit("A", 9, 9, 0, "/home", ""),
	)
	if late {
		p.Add(t, hit("D", 6, 18, 0, "/home", ""), hit("D", 6, 18, 1, "/paywall", ""), hit("D", 6, 18, 2, "", "purchase"))
	}
}

// Day parses YYYY-MM-DD.
func Day(s string) time.Time {
	d, err := time.Parse(time.DateOnly, s)
	if err != nil {
		panic(fmt.Sprintf("bad day %q", s))
	}
	return d
}
