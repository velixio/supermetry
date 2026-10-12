package testapi

import (
	"context"
	"fmt"
	"net/http"
	"testing"
)

// Member creates a non-owner organization member. A nil siteIDs slice grants
// all sites; an empty non-nil slice grants no sites.
func (e *Env) Member(t testing.TB, org Org, role string, siteIDs []int32, capabilities ...string) User {
	t.Helper()
	if capabilities == nil {
		capabilities = []string{}
	}
	n := sequence.Add(1)
	var userID int64
	if err := e.Pool.QueryRow(context.Background(), `
insert into users(email,name,password_hash)
values ($1,'API Matrix Member','hash') returning id`, fmt.Sprintf("api-matrix-member-%d@example.test", n)).Scan(&userID); err != nil {
		t.Fatalf("seed member user: %v", err)
	}
	if _, err := e.Pool.Exec(context.Background(), `
insert into memberships(org_id,user_id,role,site_ids,capabilities)
values ($1,$2,$3,$4,$5)`, org.ID, userID, role, siteIDs, capabilities); err != nil {
		t.Fatalf("seed membership: %v", err)
	}
	return User{ID: userID}
}

// SessionHeaders returns browser headers with a matching double-submit CSRF
// token. It is suitable for private write requests.
func SessionHeaders(session *http.Cookie) http.Header {
	const token = "api-matrix-csrf"
	headers := make(http.Header)
	headers.Set("Cookie", session.String()+"; supermetry_csrf="+token)
	headers.Set("X-CSRF-Token", token)
	return headers
}
