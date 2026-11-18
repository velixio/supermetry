package imports

import (
	"context"
	"testing"
	"time"
)

func totals(t *testing.T, day string, visitors, visits, pageviews int64) DayTotals {
	t.Helper()
	return DayTotals{Day: date(t, day), Visitors: visitors, Visits: visits, Pageviews: pageviews}
}

func TestCompareMatchesDaysAndComputesDifferences(t *testing.T) {
	imported := []DayTotals{
		totals(t, "2026-03-02", 200, 220, 500),
		totals(t, "2026-03-01", 100, 110, 400),
		totals(t, "2026-03-03", 0, 0, 0),
		totals(t, "2026-03-04", 50, 60, 80),
	}
	native := []DayTotals{
		totals(t, "2026-03-01", 95, 100, 420),
		totals(t, "2026-03-02", 210, 230, 450),
		totals(t, "2026-03-03", 7, 8, 9),
		totals(t, "2026-03-09", 1, 1, 1), // no imported side: not reported
	}
	got := Compare(imported, native)
	if len(got) != 4 {
		t.Fatalf("days = %d, want 4 (every imported day)", len(got))
	}
	if got[0].Day != "2026-03-01" || got[3].Day != "2026-03-04" {
		t.Fatalf("days not sorted: %+v", got)
	}
	first := got[0]
	if first.Native == nil || first.Imported.Visitors != 100 || first.Native.Visitors != 95 {
		t.Fatalf("first = %+v", first)
	}
	// (95 - 100) / 100 = -5%; (420 - 400) / 400 = +5%.
	if first.VisitorsDiffPct == nil || *first.VisitorsDiffPct != -5 || first.PageviewsDiffPct == nil || *first.PageviewsDiffPct != 5 {
		t.Fatalf("diffs = %v %v", first.VisitorsDiffPct, first.PageviewsDiffPct)
	}
	// An imported zero has no meaningful percentage.
	if got[2].Native == nil || got[2].VisitorsDiffPct != nil {
		t.Fatalf("zero baseline = %+v", got[2])
	}
	// No collected data on that day: the native side is absent, not zero.
	if got[3].Native != nil || got[3].VisitorsDiffPct != nil {
		t.Fatalf("native-less day = %+v", got[3])
	}
}

func TestImportedDailyNewestImportWinsAndFiltersByImport(t *testing.T) {
	store, siteID, userID := newImportStore(t)
	ctx := context.Background()
	complete := func(visitors int64, days ...string) int64 {
		created, err := store.Create(ctx, CreateInput{SiteID: siteID, Source: SourcePlausible, FileName: "x", CreatedBy: userID})
		if err != nil {
			t.Fatal(err)
		}
		if err := store.SetRunning(ctx, created.ID); err != nil {
			t.Fatal(err)
		}
		agg := newAggregate()
		for _, day := range days {
			agg.Days[date(t, day)] = Metrics{Visitors: visitors, Visits: visitors + 1, Pageviews: visitors * 3}
		}
		if err := store.Complete(ctx, created.ID, agg, ParseStats{Rows: int64(len(days))}); err != nil {
			t.Fatal(err)
		}
		time.Sleep(5 * time.Millisecond)
		return created.ID
	}
	older := complete(10, "2026-03-01", "2026-03-02")
	newer := complete(20, "2026-03-02", "2026-03-03")
	// A failed import never counts.
	failed, _ := store.Create(ctx, CreateInput{SiteID: siteID, Source: SourcePlausible, FileName: "f", CreatedBy: userID})
	_ = store.SetFailed(ctx, failed.ID, context.Canceled)

	from, to := date(t, "2026-03-01"), date(t, "2026-03-31")
	all, err := store.ImportedDaily(ctx, siteID, from, to, 0)
	if err != nil {
		t.Fatal(err)
	}
	if len(all) != 3 || all[0].Visitors != 10 || all[1].Visitors != 20 || all[2].Visitors != 20 || all[1].Pageviews != 60 {
		t.Fatalf("newest wins = %+v", all)
	}
	only, err := store.ImportedDaily(ctx, siteID, from, to, older)
	if err != nil || len(only) != 2 || only[1].Visitors != 10 {
		t.Fatalf("older only = %+v err=%v", only, err)
	}
	narrow, err := store.ImportedDaily(ctx, siteID, date(t, "2026-03-03"), date(t, "2026-03-03"), newer)
	if err != nil || len(narrow) != 1 || narrow[0].Day != date(t, "2026-03-03") {
		t.Fatalf("narrow = %+v err=%v", narrow, err)
	}
}
