package platformhttp_test

import (
	"context"
	"encoding/json"
	"net/http"
	"net/http/httptest"
	"strings"
	"testing"

	"supermetry/internal/alerts"
)

type alertsFake struct {
	channels map[int64]alerts.DeliveryChannel
	sent     []int64
}

func (f *alertsFake) DeliveryChannels(_ context.Context, ids []int64) ([]alerts.DeliveryChannel, error) {
	var out []alerts.DeliveryChannel
	for _, id := range ids {
		if c, ok := f.channels[id]; ok {
			out = append(out, c)
		}
	}
	return out, nil
}

func (f *alertsFake) DeliverMessage(_ context.Context, c alerts.DeliveryChannel, _, _, _ string) error {
	f.sent = append(f.sent, c.ID)
	return nil
}

// alertsEnv adds one channel per organization to a stars environment.
func newAlertsEnv(t *testing.T) (starsEnv, *alertsFake, int64, int64) {
	t.Helper()
	env := newStarsEnv(t, true)
	fake := &alertsFake{channels: map[int64]alerts.DeliveryChannel{}}
	env.svc.AlertChannels, env.svc.AlertSender = fake, fake
	add := func(org int64, name string) int64 {
		var id int64
		if err := env.pool.QueryRow(context.Background(),
			`insert into notification_channels(org_id,kind,name,config) values ($1,'slack',$2,'{}') returning id`, org, name).Scan(&id); err != nil {
			t.Fatal(err)
		}
		fake.channels[id] = alerts.DeliveryChannel{ID: id, OrgID: org, Kind: alerts.ChannelSlack, Name: name, Enabled: true}
		return id
	}
	return env, fake, add(env.org, "mine"), add(env.otherOrg, "theirs")
}

func TestStarAlertRoutesEnforceRolesAndOrganizations(t *testing.T) {
	env, _, mine, _ := newAlertsEnv(t)
	repo := jsonNumber(env.repo.ID)
	path := env.base + "/repos/" + repo + "/alerts"
	body := `{"events":{"star_spike":true},"channel_ids":[` + jsonNumber(mine) + `]}`
	tests := []struct {
		actor, method, path, body string
		want                      int
	}{
		{"owner", http.MethodGet, path, "", http.StatusOK},
		{"admin", http.MethodGet, path, "", http.StatusOK},
		{"editor", http.MethodGet, path, "", http.StatusOK},
		{"viewer", http.MethodGet, path, "", http.StatusOK},
		{"owner2", http.MethodGet, path, "", http.StatusNotFound},
		{"root", http.MethodGet, path, "", http.StatusForbidden},
		{"owner", http.MethodPut, path, body, http.StatusOK},
		{"admin", http.MethodPut, path, body, http.StatusOK},
		{"editor", http.MethodPut, path, body, http.StatusForbidden},
		{"viewer", http.MethodPut, path, body, http.StatusForbidden},
		{"owner2", http.MethodPut, path, body, http.StatusNotFound},
		{"root", http.MethodPut, path, body, http.StatusForbidden},
		{"owner", http.MethodPost, path + "/test", "", http.StatusOK},
		{"editor", http.MethodPost, path + "/test", "", http.StatusForbidden},
		{"viewer", http.MethodPost, path + "/test", "", http.StatusForbidden},
		{"owner2", http.MethodPost, path + "/test", "", http.StatusNotFound},
		// Another organization's repository is invisible through our path.
		{"owner", http.MethodGet, env.base + "/repos/" + jsonNumber(env.otherRepo.ID) + "/alerts", "", http.StatusNotFound},
		{"owner", http.MethodPut, env.base + "/repos/" + jsonNumber(env.otherRepo.ID) + "/alerts", body, http.StatusNotFound},
		{"owner", http.MethodGet, env.base + "/repos/x/alerts", "", http.StatusBadRequest},
	}
	for _, tt := range tests {
		t.Run(tt.actor+" "+tt.method+" "+strings.TrimPrefix(tt.path, env.base), func(t *testing.T) {
			r := starsRequest(env.handler, tt.method, tt.path, tt.actor, tt.body)
			if r.Code != tt.want {
				t.Fatalf("status = %d body=%s, want %d", r.Code, r.Body.String(), tt.want)
			}
		})
	}
}

func TestStarAlertSettingsRoundTripValidationAndTest(t *testing.T) {
	env, fake, mine, theirs := newAlertsEnv(t)
	path := env.base + "/repos/" + jsonNumber(env.repo.ID) + "/alerts"

	r := starsRequest(env.handler, http.MethodGet, path, "owner", "")
	var got struct {
		Events     map[string]bool         `json:"events"`
		ChannelIDs []int64                 `json:"channel_ids"`
		EventKinds []struct{ Kind string } `json:"event_kinds"`
	}
	if err := json.Unmarshal(r.Body.Bytes(), &got); err != nil || len(got.EventKinds) != 5 || len(got.ChannelIDs) != 0 {
		t.Fatalf("default settings = %s, %v", r.Body.String(), err)
	}
	for k, on := range got.Events {
		if on {
			t.Fatalf("%s defaults to on", k)
		}
	}

	save := func(body string) *httptest.ResponseRecorder {
		return starsRequest(env.handler, http.MethodPut, path, "owner", body)
	}
	if r := save(`{"events":{"star_spike":true},"channel_ids":[` + jsonNumber(theirs) + `]}`); r.Code != http.StatusBadRequest {
		t.Fatalf("foreign channel: status %d %s", r.Code, r.Body.String())
	}
	if r := save(`{"events":{"nope":true},"channel_ids":[]}`); r.Code != http.StatusBadRequest {
		t.Fatalf("unknown event: status %d", r.Code)
	}
	if r := save(`{"events":{"star_spike":true},"channel_ids":[` + jsonNumber(mine) + `],"extra":1}`); r.Code != http.StatusBadRequest {
		t.Fatalf("unknown field: status %d", r.Code)
	}
	if r := save(`{"events":{"star_spike":true,"star_milestone":true},"channel_ids":[` + jsonNumber(mine) + `]}`); r.Code != http.StatusOK {
		t.Fatalf("save: status %d %s", r.Code, r.Body.String())
	}
	r = starsRequest(env.handler, http.MethodGet, path, "viewer", "")
	if err := json.Unmarshal(r.Body.Bytes(), &got); err != nil || !got.Events["star_spike"] || !got.Events["star_milestone"] || got.Events["unstar_surge"] || len(got.ChannelIDs) != 1 || got.ChannelIDs[0] != mine {
		t.Fatalf("saved settings = %s, %v", r.Body.String(), err)
	}

	r = starsRequest(env.handler, http.MethodPost, path+"/test", "admin", "")
	if r.Code != http.StatusOK || !strings.Contains(r.Body.String(), `"ok":true`) || len(fake.sent) != 1 || fake.sent[0] != mine {
		t.Fatalf("test alert: status %d %s sent=%v", r.Code, r.Body.String(), fake.sent)
	}
	if r := starsRequest(env.handler, http.MethodPost, path+"/test", "admin", ""); r.Code != http.StatusTooManyRequests {
		t.Fatalf("second test alert: status %d, want 429", r.Code)
	}

	var audits int
	if err := env.pool.QueryRow(context.Background(), `select count(*) from audit_log where org_id=$1 and action='github_stars.alerts_updated'`, env.org).Scan(&audits); err != nil || audits != 1 {
		t.Fatalf("audit rows = %d, %v", audits, err)
	}
}
