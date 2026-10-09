package ingest_test

import (
	"context"
	"net/http"
	"net/http/httptest"
	"strings"
	"testing"

	"panel/internal/privacy"
)

func TestAnonymousRejectsForgedIdentityAndProperties(t *testing.T) {
	f := newHandlerFixture(t, 32, nil)
	seedProfileSite(t, f.pool)
	setProfilesEnabled(t, f, true)
	f.h.TrackingMode = privacy.Anonymous
	// Exercise intake independently of final persistence defense.
	f.writer.SetTrackingPolicy(privacy.Identified, f.h.Sites)
	for _, body := range []string{
		`{"site":"abc123","name":"identify","url":"https://example.com/","profile_id":"forged","traits":{"name":"person"}}`,
		`{"site":"abc123","name":"purchase","url":"https://example.com/?utm_source=person-secret&utm_campaign=person-secret#person-secret","referrer":"https://ref.example/?person-secret=yes#person-secret","profile_id":"forged","props":{"email":"person-secret"}}`,
	} {
		if rr := post(t, f.h, body, testUA, "198.51.100.7:5555"); rr.Code != http.StatusAccepted {
			t.Fatalf("status=%d", rr.Code)
		}
	}
	f.stop(t)
	var profiles, events, linked, props, secrets int
	if err := f.pool.QueryRow(context.Background(), `select (select count(*) from profiles), count(*), count(profile_id), count(props), (select count(*) from dict where value like '%person-secret%') from events`).Scan(&profiles, &events, &linked, &props, &secrets); err != nil {
		t.Fatal(err)
	}
	if profiles != 0 || events != 1 || linked != 0 || props != 0 || secrets != 0 {
		t.Fatalf("profiles=%d events=%d linked=%d props=%d secret dimensions=%d", profiles, events, linked, props, secrets)
	}
}

func TestPrivacySignalsCannotBeBypassedByHeatmap(t *testing.T) {
	for _, header := range []string{"DNT", "Sec-GPC"} {
		t.Run(header, func(t *testing.T) {
			f := newHandlerFixture(t, 32, nil)
			setHeatmapsEnabled(t, f, true)
			setRespectPrivacySignals(t, f, false)
			req := httptest.NewRequest(http.MethodPost, "/api/event", strings.NewReader(`{"site":"abc123","name":"heatmap","url":"https://example.com/","doc_h":2000,"vw":1000,"clicks":[[100,100]]}`))
			req.Header.Set("User-Agent", testUA)
			req.Header.Set(header, "1")
			req.RemoteAddr = "198.51.100.7:5555"
			rr := httptest.NewRecorder()
			f.h.ServeHTTP(rr, req)
			if rr.Code != http.StatusAccepted {
				t.Fatalf("status=%d", rr.Code)
			}
			f.stop(t)
			var cells int
			if err := f.pool.QueryRow(context.Background(), `select count(*) from heatmap_cells`).Scan(&cells); err != nil {
				t.Fatal(err)
			}
			if cells != 0 {
				t.Fatalf("signal uploaded %d heatmap cells", cells)
			}
		})
	}
}
