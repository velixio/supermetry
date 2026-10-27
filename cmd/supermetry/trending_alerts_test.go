package main

import (
	"context"
	"strings"
	"sync"
	"testing"
	"time"

	"supermetry/internal/alerts"
	"supermetry/internal/db"
	"supermetry/internal/ghstars"
	"supermetry/internal/ghtrending"
	platformcrypto "supermetry/internal/platform/crypto"
	"supermetry/internal/testpg"
)

type trendingFake struct {
	mu       sync.Mutex
	channels map[int64]alerts.DeliveryChannel
	sent     []string
}

func (f *trendingFake) DeliveryChannels(_ context.Context, ids []int64) ([]alerts.DeliveryChannel, error) {
	var out []alerts.DeliveryChannel
	for _, id := range ids {
		if c, ok := f.channels[id]; ok {
			out = append(out, c)
		}
	}
	return out, nil
}

func (f *trendingFake) DeliverMessage(_ context.Context, _ alerts.DeliveryChannel, kind, _, text string) error {
	f.mu.Lock()
	defer f.mu.Unlock()
	f.sent = append(f.sent, kind+": "+text)
	return nil
}

func (f *trendingFake) messages() []string {
	f.mu.Lock()
	defer f.mu.Unlock()
	return append([]string(nil), f.sent...)
}

func TestTrendingSightingsReachTheChannelOncePerEpisode(t *testing.T) {
	pool, _ := testpg.New(t)
	ctx := context.Background()
	if err := db.Migrate(ctx, pool); err != nil {
		t.Fatal(err)
	}
	box, err := platformcrypto.NewSecretBox(make([]byte, 32), "test/trending-alerts")
	if err != nil {
		t.Fatal(err)
	}
	clock := time.Date(2026, 10, 28, 9, 0, 0, 0, time.UTC)
	svc := ghstars.NewService(pool, box, "https://stats.example.test", func() time.Time { return clock })
	fake := &trendingFake{channels: map[int64]alerts.DeliveryChannel{}}
	svc.AlertChannels, svc.AlertSender = fake, fake

	var org int64
	if err := pool.QueryRow(ctx, `insert into orgs(name,slug) values ('acme','acme') returning id`).Scan(&org); err != nil {
		t.Fatal(err)
	}
	var channel int64
	if err := pool.QueryRow(ctx, `insert into notification_channels(org_id,kind,name,config) values ($1,'slack','ops','{}') returning id`, org).Scan(&channel); err != nil {
		t.Fatal(err)
	}
	fake.channels[channel] = alerts.DeliveryChannel{ID: channel, OrgID: org, Kind: alerts.ChannelSlack, Name: "ops", Enabled: true}
	addRepo := func(name string) int64 {
		var id int64
		err := pool.QueryRow(ctx, `insert into github_star_repos(org_id,public_id,repo,webhook_secret_enc) values ($1,$2,$3,'\x01') returning id`,
			org, strings.ReplaceAll(name, "/", "-"), name).Scan(&id)
		if err != nil {
			t.Fatal(err)
		}
		return id
	}
	watched, unconfigured := addRepo("acme/hot"), addRepo("acme/quiet")
	if _, err := svc.SaveAlertSettings(ctx, org, watched, ghstars.AlertSettings{
		Events:     map[ghstars.AlertKind]bool{ghstars.AlertTrendingEntered: true, ghstars.AlertTrendingLeft: true},
		ChannelIDs: []int64{channel},
	}, 0); err != nil {
		t.Fatal(err)
	}

	var notifier ghtrending.TrendingNotifier = trendingAlerts{alerts: svc}
	episode := ghtrending.Sighting{ID: 41, ListLanguage: "go", Since: "daily", FirstRank: 4, BestRank: 2, LastRank: 3}

	// Entered: delivered once, even if the watcher reports it again.
	notifier.Entered(ctx, watched, episode)
	notifier.Entered(ctx, watched, episode)
	got := fake.messages()
	if len(got) != 1 || !strings.HasPrefix(got[0], "github_star_trending_entered: ") ||
		!strings.Contains(got[0], "acme/hot is now trending on GitHub (go daily). Rank 4.") || !strings.Contains(got[0], "https://github.com/acme/hot") {
		t.Fatalf("after entered: %q", got)
	}

	// Left: its own kind, delivered once, reporting the best rank.
	clock = clock.Add(time.Hour)
	notifier.Left(ctx, watched, episode)
	notifier.Left(ctx, watched, episode)
	got = fake.messages()
	if len(got) != 2 || !strings.Contains(got[1], "acme/hot dropped off GitHub trending (go daily). Rank 2.") {
		t.Fatalf("after left: %q", got)
	}

	// The dedup key carries the episode: a later episode on the same list alerts again.
	clock = clock.Add(time.Hour)
	notifier.Entered(ctx, watched, ghtrending.Sighting{ID: 42, ListLanguage: "go", Since: "daily", FirstRank: 9})
	if got = fake.messages(); len(got) != 3 || !strings.Contains(got[2], "Rank 9.") {
		t.Fatalf("after second episode: %q", got)
	}
	var keys []string
	rows, err := pool.Query(ctx, `select key from github_star_alert_log where repo_id=$1 order by key`, watched)
	if err != nil {
		t.Fatal(err)
	}
	for rows.Next() {
		var k string
		if err := rows.Scan(&k); err != nil {
			t.Fatal(err)
		}
		keys = append(keys, k)
	}
	rows.Close()
	if len(keys) != 3 || !strings.Contains(strings.Join(keys, " "), "episode41") || !strings.Contains(strings.Join(keys, " "), "episode42") {
		t.Fatalf("log keys = %v", keys)
	}

	// A repository without alert settings sends nothing and records nothing.
	notifier.Entered(ctx, unconfigured, episode)
	notifier.Left(ctx, unconfigured, episode)
	if got = fake.messages(); len(got) != 3 {
		t.Fatalf("unconfigured repo sent: %q", got)
	}
	var logged int
	if err := pool.QueryRow(ctx, `select count(*) from github_star_alert_log where repo_id=$1`, unconfigured).Scan(&logged); err != nil || logged != 0 {
		t.Fatalf("unconfigured log rows = %d, %v", logged, err)
	}
}
