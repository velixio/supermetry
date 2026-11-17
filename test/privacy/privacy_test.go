package privacy_test

import (
	"context"
	"regexp"
	"strings"
	"testing"

	"supermetry/internal/testenv"
)

func TestNoIPOrUAPersistedAnywhere(t *testing.T) {
	env := testenv.Start(t)
	ips, userAgents := testenv.ReplayTinyCollect(t, env)
	env.CloseAll(t)
	env.SIGTERM(t)

	dump := testenv.PgDumpSchema(t, env)
	logs := env.Logs()
	for _, secret := range append(ips, userAgents...) {
		if strings.Contains(dump, secret) || strings.Contains(logs, secret) {
			t.Fatalf("persisted: %q", secret[:min(len(secret), 40)])
		}
	}
}

var (
	emailRe    = regexp.MustCompile(`[A-Za-z0-9._%+-]+@[A-Za-z0-9-]+\.[A-Za-z]{2,}`)
	ipv4Re     = regexp.MustCompile(`\b(?:\d{1,3}\.){3}\d{1,3}\b`)
	homePathRe = regexp.MustCompile(`/home/[A-Za-z0-9._-]+|/Users/[A-Za-z0-9._ -]+|[A-Za-z]:\\Users\\`)
)

// dirtyEnvelope is an event that carries every kind of private data the
// scrubber must remove: an email, a client address, a home path, and cookie
// and authorization values.
const dirtyEnvelope = `{"event_id":"aaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaa","sent_at":"2026-11-17T13:59:00.000Z"}
{"type":"event"}
{"event_id":"aaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaa","timestamp":1794923940,"platform":"node","level":"error","message":"failed for you@example.com","user":{"id":"u-1","email":"you@example.com","ip_address":"203.0.113.77","username":"jane"},"request":{"url":"https://app.example.test/a","headers":{"Cookie":"session=SECRETCOOKIEVALUE","Authorization":"Bearer SECRETBEARERTOKEN"},"cookies":{"session":"SECRETCOOKIEVALUE"}},"exception":{"values":[{"type":"Error","value":"boom at /home/jane/project/app.js","stacktrace":{"frames":[{"filename":"/home/jane/project/app.js","function":"run","lineno":3}]}}]}}
`

// TestNoPrivateDataInStoredAppBodies posts every recorded Sentry SDK envelope,
// plus one deliberately dirty event, to the real envelope endpoint of a running
// server, then scans a dump of the database (which includes app_event_bodies)
// and the server logs for emails, IP addresses, home paths and cookie or
// authorization values.
func TestNoPrivateDataInStoredAppBodies(t *testing.T) {
	env := testenv.StartWithApp(t)
	fixtures := testenv.LoadAppFixtures(t)
	fixtures = append(fixtures, testenv.AppFixture{
		Name: "dirty", Raw: []byte(dirtyEnvelope), Method: "POST", Path: "/api/42/envelope/",
		Headers: map[string]string{"Content-Type": "application/x-sentry-envelope", "User-Agent": "sentry.javascript.node/11.0.0"},
	})
	if got := testenv.PostAppFixtures(t, env, fixtures); got != len(fixtures) {
		t.Fatalf("accepted %d of %d envelopes", got, len(fixtures))
	}
	env.CloseAll(t) // drains the app body and event writers
	env.SIGTERM(t)

	var bodies int
	if err := env.Pool().QueryRow(context.Background(), `select count(*) from app_event_bodies`).Scan(&bodies); err != nil || bodies < 15 {
		t.Fatalf("stored bodies = %d (%v); the scan would prove nothing", bodies, err)
	}
	var dirty string // the scrubbed body of the dirty event
	if err := env.Pool().QueryRow(context.Background(), `select body::text from app_event_bodies where event_id = 'aaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaa'`).Scan(&dirty); err != nil {
		t.Fatalf("the dirty event was not stored, so the scrubber was not exercised: %v", err)
	}

	dump := testenv.PgDumpSchema(t, env)
	if !strings.Contains(dump, "app_event_bodies") || !strings.Contains(dump, "aaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaa") {
		t.Fatal("the dump does not contain the stored bodies")
	}
	for name, text := range map[string]string{"database dump": dump, "logs": env.Logs()} {
		for _, secret := range []string{"SECRETCOOKIEVALUE", "SECRETBEARERTOKEN", "203.0.113.77", "you@example.com", "/home/jane"} {
			if strings.Contains(text, secret) {
				t.Errorf("%s contains %q", name, secret)
			}
		}
		if m := homePathRe.FindString(text); m != "" {
			t.Errorf("%s contains a home path %q", name, m)
		}
		for _, m := range emailRe.FindAllString(text, -1) {
			if strings.HasSuffix(m, ".invalid") { // the reserved placeholder the migrations seed
				continue
			}
			t.Errorf("%s contains an email-like value %q", name, m)
		}
		for _, m := range ipv4Re.FindAllString(text, -1) {
			t.Errorf("%s contains an address-like value %q", name, m)
		}
	}
}
