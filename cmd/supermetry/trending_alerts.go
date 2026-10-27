package main

import (
	"context"

	"supermetry/internal/ghstars"
	"supermetry/internal/ghtrending"
)

// trendingAlerts connects the trending watcher to the star alerts: an episode
// opening or closing becomes a trending_entered or trending_left alert for the
// repository's organization, if that repository's owners turned it on.
type trendingAlerts struct{ alerts ghstars.StarAlerts }

var _ ghtrending.TrendingNotifier = trendingAlerts{}

func (t trendingAlerts) Entered(ctx context.Context, repoID int64, s ghtrending.Sighting) {
	t.alerts.TrendingEntered(ctx, repoID, ghstars.TrendingDetails{
		Episode: s.ID, Period: s.Since, Language: s.ListLanguage, Rank: s.FirstRank,
	})
}

func (t trendingAlerts) Left(ctx context.Context, repoID int64, s ghtrending.Sighting) {
	t.alerts.TrendingLeft(ctx, repoID, ghstars.TrendingDetails{
		Episode: s.ID, Period: s.Since, Language: s.ListLanguage, Rank: s.BestRank,
	})
}
