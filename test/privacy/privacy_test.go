package privacy_test

import (
	"strings"
	"testing"

	"panel/internal/testenv"
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
