package appproduct_test

import (
	"context"
	"testing"

	"supermetry/internal/appproduct"
	"supermetry/internal/privacy"
)

func TestSessionInitBecomesAppOpen(t *testing.T) {
	pool := setupDB(t)
	r := newRig(t, pool, privacy.Identified, 100)
	out := r.adapter.AcceptSession(context.Background(), r.site, appproduct.SessionStart{
		Started: at("10-09", "09:00"), InstallID: "inst-1", Release: "2.0.0", Platform: "android",
		OSName: "Android", OSVersion: "14", Country: "DE",
	})
	if !out.Accepted() {
		t.Fatalf("outcome %+v", out)
	}
	r.stop()
	var kind int16
	var name, hostname, release string
	var visitor int64
	err := pool.QueryRow(context.Background(), `select e.kind, n.value, h.value, e.props->>'$release', e.visitor_id from events e
		join dict n on n.kind=7 and n.id=e.name_id join dict h on h.kind=16 and h.id=e.hostname_id
		where e.site_id=$1`, siteID).Scan(&kind, &name, &hostname, &release, &visitor)
	if err != nil {
		t.Fatal(err)
	}
	if kind != 1 || name != appproduct.AppOpenEvent || hostname != "android" || release != "2.0.0" {
		t.Errorf("kind=%d name=%q hostname=%q release=%q", kind, name, hostname, release)
	}
	if want := appproduct.VisitorID(secret, "inst-1"); visitor != want {
		t.Errorf("visitor %d, want %d", visitor, want)
	}
}

func TestSessionNoInstallStoresNothing(t *testing.T) {
	pool := setupDB(t)
	r := newRig(t, pool, privacy.Identified, 100)
	out := r.adapter.AcceptSession(context.Background(), r.site, appproduct.SessionStart{Started: at("10-09", "09:00"), Platform: "web"})
	if out.Accepted() {
		t.Fatalf("outcome %+v", out)
	}
	r.stop()
	var n int
	if err := pool.QueryRow(context.Background(), `select count(*) from events where site_id=$1`, siteID).Scan(&n); err != nil || n != 0 {
		t.Fatalf("rows %d err %v", n, err)
	}
}

func TestClientCannotSendAppOpen(t *testing.T) {
	pool := setupDB(t)
	r := newRig(t, pool, privacy.Identified, 100)
	out := r.adapter.Accept(context.Background(), r.site, appproduct.Hit{SiteID: siteID, TS: at("10-09", "09:00"), InstallID: "x", Name: appproduct.AppOpenEvent, Platform: "ios"})
	if out.Accepted() {
		t.Fatalf("reserved name accepted: %+v", out)
	}
}
