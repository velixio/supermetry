package api

import (
	"context"
	"encoding/base64"
	"encoding/hex"
	"fmt"
	"net/http"
	"net/http/httptest"
	"strings"
	"testing"
	"time"

	"supermetry/internal/appquery"
	"supermetry/internal/privacy"
	"supermetry/internal/sentryingest/ingesttest"
	"supermetry/internal/sites"
)

// An event that came through the Sentry front door with a user, an email in
// its text and tags outside the site's allowlist reaches the MCP tools with
// the scrubbed title only: no user hash, no raw email, no unlisted tag.
func TestMCPAppIssuesFromSentryEventsAreScrubbed(t *testing.T) {
	ingestAt := time.Date(2026, 11, 17, 13, 20, 0, 0, time.UTC)
	env := ingesttest.New(t, ingestAt, "feature")
	envelope := ingesttest.Envelope(t, "node/unhandled-exception", func(ev map[string]any) {
		ev["user"] = map[string]any{"id": "user-4711", "email": "jane.doe@example.test", "username": "janedoe"}
		ev["tags"] = map[string]any{"feature": "checkout", "customer_secret": "s3cr3t-value"}
		values := ev["exception"].(map[string]any)["values"].([]any)
		values[0].(map[string]any)["value"] = "payment failed for jane.doe@example.test"
	})
	if code := env.Post(t, envelope); code != http.StatusOK {
		t.Fatalf("status %d", code)
	}

	var issueID int64
	var userHash []byte
	if err := env.Pool.QueryRow(context.Background(), `select i.id, e.user_hash from app_issues i join app_events e on e.issue_id = i.id`).Scan(&issueID, &userHash); err != nil {
		t.Fatal(err)
	}
	if len(userHash) == 0 {
		t.Fatal("the event stored no user hash; the test would prove nothing")
	}

	now := ingestAt.Add(time.Hour)
	handler := NewMCPHandler(MCPDeps{
		TrackingMode: privacy.Identified, AppHealth: appquery.New(env.Pool, func() time.Time { return now }),
		Query: &mcpQueryStub{}, Sessions: mcpSessionStub{}, Now: func() time.Time { return now },
	})
	call := func(tool, arguments string) string {
		t.Helper()
		body := `{"jsonrpc":"2.0","id":1,"method":"tools/call","params":{"name":"` + tool + `","arguments":` + arguments + `}}`
		request := httptest.NewRequest(http.MethodPost, "http://supermetry.test/api/public/v1/mcp", strings.NewReader(body))
		site := env.Site
		site.OrgID = 7
		ctx := context.WithValue(request.Context(), principalContextKey{}, Principal{OrgID: 7, Scopes: []string{"stats:read"}})
		ctx = context.WithValue(ctx, registryContextKey{}, sites.Registry(mcpRegistry{site.ID: site}))
		response := httptest.NewRecorder()
		handler.ServeHTTP(response, request.WithContext(ctx))
		if response.Code != http.StatusOK || strings.Contains(response.Body.String(), `"error"`) {
			t.Fatalf("%s = %d %s", tool, response.Code, response.Body)
		}
		return response.Body.String()
	}

	outputs := map[string]string{
		"list_app_issues": call("list_app_issues", `{"site":42,"period":"7d"}`),
		"get_app_issue":   call("get_app_issue", fmt.Sprintf(`{"site":42,"issue_id":%d,"period":"7d"}`, issueID)),
	}
	for tool, out := range outputs {
		if !strings.Contains(out, "Error: payment failed for") {
			t.Errorf("%s lacks the scrubbed title: %s", tool, out)
		}
		for _, leak := range []string{
			"jane.doe", "janedoe", "user-4711", "user_hash", "customer_secret", "s3cr3t-value",
			hex.EncodeToString(userHash), base64.StdEncoding.EncodeToString(userHash),
		} {
			if strings.Contains(out, leak) {
				t.Errorf("%s leaks %q: %s", tool, leak, out)
			}
		}
	}
	if !strings.Contains(outputs["get_app_issue"], "checkout") {
		t.Errorf("allowlisted tag value missing from get_app_issue: %s", outputs["get_app_issue"])
	}
}
