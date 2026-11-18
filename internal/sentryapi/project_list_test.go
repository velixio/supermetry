package sentryapi

import (
	"encoding/json"
	"slices"
	"testing"
)

func TestProjectListAcceptsNumbersAndStrings(t *testing.T) {
	var got struct {
		Projects projectList `json:"projects"`
	}
	if err := json.Unmarshal([]byte(`{"projects":[5,"appsite00020",7.0]}`), &got); err != nil {
		t.Fatal(err)
	}
	if want := []string{"5", "appsite00020", "7"}; !slices.Equal(got.Projects, want) {
		t.Fatalf("projects = %v, want %v", got.Projects, want)
	}
	for _, bad := range []string{`{"projects":[true]}`, `{"projects":[{"a":1}]}`, `{"projects":[1.5]}`, `{"projects":"x"}`} {
		if err := json.Unmarshal([]byte(bad), &got); err == nil {
			t.Fatalf("%s: want an error", bad)
		}
	}
}

// sentry-cli sends numeric project ids when SENTRY_PROJECT is a number:
// {"version":"1.0.0","projects":[5]}.
func TestReleaseAndDeployAcceptNumericProjects(t *testing.T) {
	e := newEnv(t, nil)
	full := e.svc(t, e.owner, []string{Scope}, nil)
	if s, _ := e.json(t, "POST", relBase, full, map[string]any{"version": "1.0", "projects": []int{10, 20}}); s != 201 {
		t.Fatalf("create release with numeric projects = %d", s)
	}
	s, body := e.json(t, "POST", relBase+"1.0/deploys/", full, map[string]any{"environment": "production", "projects": []int{10}})
	if s != 201 {
		t.Fatalf("deploy with numeric projects = %d", s)
	}
	if got := e.deploySites(t, body["id"]); !slices.Equal(got, []int32{10}) {
		t.Fatalf("site_ids = %v, want [10]", got)
	}
	if s, _ := e.json(t, "PUT", relBase+"1.0/", full, map[string]any{"projects": []int{10}, "ref": "abc"}); s != 200 {
		t.Fatalf("update with numeric projects = %d", s)
	}
}
