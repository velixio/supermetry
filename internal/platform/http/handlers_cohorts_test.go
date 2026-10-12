package platformhttp_test

import (
	"context"
	"encoding/json"
	"fmt"
	"net/http"
	"strings"
	"testing"

	"supermetry/internal/api"
)

const cohortBody = `{"name":"%s","definition":{"match":"all","window_days":30,"conditions":[{"type":"event","name":"signup","min_count":2}]}}`

func cohortURL(siteID int32) string { return fmt.Sprintf("/api/private/sites/%d/cohorts", siteID) }

func decodeCohort(t *testing.T, body string) api.Cohort {
	t.Helper()
	var cohort api.Cohort
	if err := json.NewDecoder(strings.NewReader(body)).Decode(&cohort); err != nil {
		t.Fatal(err)
	}
	return cohort
}

func TestCohortsCRUDDuplicateAndAudit(t *testing.T) {
	h := newSegmentHarness(t)
	ctx := context.Background()
	if _, err := h.pool.Pool.Exec(ctx, `delete from audit_log`); err != nil {
		t.Fatal(err)
	}
	created := decodeCohort(t, segmentRawRequest(t, h.handler, "owner", http.MethodPost, cohortURL(h.siteID), fmt.Sprintf(cohortBody, "  Power signups "), http.StatusCreated).Body.String())
	if created.Name != "Power signups" || created.SiteId != h.siteID || created.Definition.WindowDays != 30 || created.CreatedBy == nil || *created.CreatedBy != h.owner.UserID {
		t.Fatalf("created = %+v", created)
	}
	if got := created.Definition.Conditions[0]; got.MinCount == nil || *got.MinCount != 2 {
		t.Fatalf("stored condition = %+v", got)
	}

	var list api.CohortList
	if err := json.NewDecoder(segmentRawRequest(t, h.handler, "viewer", http.MethodGet, cohortURL(h.siteID), "", http.StatusOK).Body).Decode(&list); err != nil {
		t.Fatal(err)
	}
	if len(list.Data) != 1 || list.Data[0].Id != created.Id {
		t.Fatalf("list = %+v", list)
	}

	updated := decodeCohort(t, segmentRawRequest(t, h.handler, "editor", http.MethodPatch, fmt.Sprintf("/api/private/cohorts/%d", created.Id),
		`{"name":"Renamed","definition":{"match":"any","window_days":7,"conditions":[{"type":"page","path":"/pricing"}]}}`, http.StatusOK).Body.String())
	if updated.Name != "Renamed" || updated.Definition.Match != "any" || updated.Definition.WindowDays != 7 || !updated.UpdatedAt.After(created.UpdatedAt) {
		t.Fatalf("updated = %+v", updated)
	}

	first := decodeCohort(t, segmentRawRequest(t, h.handler, "owner", http.MethodPost, fmt.Sprintf("/api/private/cohorts/%d/duplicate", created.Id), "", http.StatusCreated).Body.String())
	second := decodeCohort(t, segmentRawRequest(t, h.handler, "owner", http.MethodPost, fmt.Sprintf("/api/private/cohorts/%d/duplicate", created.Id), "", http.StatusCreated).Body.String())
	if first.Name != "Renamed (copy)" || second.Name != "Renamed (copy 2)" || first.Id == created.Id || first.Definition.Match != "any" {
		t.Fatalf("copies = %q and %q", first.Name, second.Name)
	}
	for _, id := range []int64{created.Id, first.Id, second.Id} {
		segmentRawRequest(t, h.handler, "owner", http.MethodDelete, fmt.Sprintf("/api/private/cohorts/%d", id), "", http.StatusNoContent)
	}
	segmentRawRequest(t, h.handler, "owner", http.MethodDelete, fmt.Sprintf("/api/private/cohorts/%d", created.Id), "", http.StatusNotFound)

	rows, err := h.pool.Pool.Query(ctx, `select action from audit_log order by id`)
	if err != nil {
		t.Fatal(err)
	}
	defer rows.Close()
	var actions []string
	for rows.Next() {
		var action string
		if err := rows.Scan(&action); err != nil {
			t.Fatal(err)
		}
		actions = append(actions, action)
	}
	want := "[cohort.created cohort.updated cohort.duplicated cohort.duplicated cohort.deleted cohort.deleted cohort.deleted]"
	if fmt.Sprint(actions) != want {
		t.Fatalf("audit actions = %v, want %s", actions, want)
	}
}

func TestCohortValidationRejectsOutOfBoundsDefinitions(t *testing.T) {
	h := newSegmentHarness(t)
	conditions := strings.Repeat(`{"type":"event","name":"a"},`, 10) + `{"type":"event","name":"a"}`
	tests := map[string]string{
		"malformed JSON":      `{"name":`,
		"blank name":          `{"name":"  ","definition":{"match":"all","window_days":7,"conditions":[{"type":"event","name":"a"}]}}`,
		"name too long":       fmt.Sprintf(cohortBody, strings.Repeat("x", 81)),
		"window zero":         `{"name":"A","definition":{"match":"all","window_days":0,"conditions":[{"type":"event","name":"a"}]}}`,
		"window past limit":   `{"name":"A","definition":{"match":"all","window_days":91,"conditions":[{"type":"event","name":"a"}]}}`,
		"no conditions":       `{"name":"A","definition":{"match":"all","window_days":7,"conditions":[]}}`,
		"eleven conditions":   `{"name":"A","definition":{"match":"all","window_days":7,"conditions":[` + conditions + `]}}`,
		"unknown match":       `{"name":"A","definition":{"match":"none","window_days":7,"conditions":[{"type":"event","name":"a"}]}}`,
		"unknown type":        `{"name":"A","definition":{"match":"all","window_days":7,"conditions":[{"type":"country","name":"a"}]}}`,
		"unknown field":       `{"name":"A","definition":{"match":"all","window_days":7,"conditions":[{"type":"event","name":"a","sql":"1=1"}]}}`,
		"min count too large": `{"name":"A","definition":{"match":"all","window_days":7,"conditions":[{"type":"event","name":"a","min_count":1001}]}}`,
	}
	for name, body := range tests {
		t.Run(name, func(t *testing.T) {
			segmentRawRequest(t, h.handler, "owner", http.MethodPost, cohortURL(h.siteID), body, http.StatusBadRequest)
		})
	}
	segmentRawRequest(t, h.handler, "owner", http.MethodPost, cohortURL(h.siteID), fmt.Sprintf(cohortBody, "Same"), http.StatusCreated)
	segmentRawRequest(t, h.handler, "owner", http.MethodPost, cohortURL(h.siteID), fmt.Sprintf(cohortBody, "same"), http.StatusConflict)
}

func TestCohortAuthorizationAndIsolation(t *testing.T) {
	h := newSegmentHarness(t)
	created := decodeCohort(t, segmentRawRequest(t, h.handler, "owner", http.MethodPost, cohortURL(h.siteID), fmt.Sprintf(cohortBody, "Saved"), http.StatusCreated).Body.String())
	path := fmt.Sprintf("/api/private/cohorts/%d", created.Id)

	// Viewers read but never write.
	segmentRawRequest(t, h.handler, "viewer", http.MethodGet, cohortURL(h.siteID), "", http.StatusOK)
	segmentRawRequest(t, h.handler, "viewer", http.MethodPost, cohortURL(h.siteID), fmt.Sprintf(cohortBody, "No"), http.StatusForbidden)
	segmentRawRequest(t, h.handler, "viewer", http.MethodPatch, path, fmt.Sprintf(cohortBody, "No"), http.StatusForbidden)
	segmentRawRequest(t, h.handler, "viewer", http.MethodDelete, path, "", http.StatusForbidden)
	segmentRawRequest(t, h.handler, "viewer", http.MethodPost, path+"/duplicate", "", http.StatusForbidden)
	// Another organization cannot see or touch it.
	segmentRawRequest(t, h.handler, "outside", http.MethodGet, cohortURL(h.siteID), "", http.StatusNotFound)
	segmentRawRequest(t, h.handler, "outside", http.MethodPatch, path, fmt.Sprintf(cohortBody, "No"), http.StatusNotFound)
	segmentRawRequest(t, h.handler, "outside", http.MethodDelete, path, "", http.StatusNotFound)
	segmentRawRequest(t, h.handler, "outside", http.MethodPost, path+"/duplicate", "", http.StatusNotFound)
	// Unknown id.
	segmentRawRequest(t, h.handler, "owner", http.MethodPatch, "/api/private/cohorts/99999", fmt.Sprintf(cohortBody, "No"), http.StatusNotFound)
}

func TestCohortsAreCappedPerSite(t *testing.T) {
	h := newSegmentHarness(t)
	ctx := context.Background()
	if _, err := h.pool.Pool.Exec(ctx, `insert into cohorts(site_id,name,definition)
		select $1,'bulk '||g,'{"match":"all","window_days":7,"conditions":[{"type":"event","name":"a"}]}' from generate_series(1,100) g`, h.siteID); err != nil {
		t.Fatal(err)
	}
	segmentRawRequest(t, h.handler, "owner", http.MethodPost, cohortURL(h.siteID), fmt.Sprintf(cohortBody, "One too many"), http.StatusConflict)
}
