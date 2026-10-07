package config

import (
	"testing"
	"time"
)

func env(m map[string]string) func(string) string {
	return func(k string) string { return m[k] }
}

func TestLoadDefaultsAndRequired(t *testing.T) {
	_, err := Load(env(map[string]string{}))
	if err == nil {
		t.Fatal("want error for missing PANEL_DATABASE_URL and PANEL_BASE_URL")
	}
	c, err := Load(env(map[string]string{"PANEL_DATABASE_URL": "postgres://x", "PANEL_BASE_URL": "https://a.example"}))
	if err != nil {
		t.Fatal(err)
	}
	if c.Listen != ":8080" || c.EventPath != "/api/event" || c.QueueSize != 65536 || c.FlushEvery != time.Second || c.QueryTimeout != 10*time.Second {
		t.Fatalf("bad defaults: %+v", c)
	}
}

func TestLoadRejectsBadValues(t *testing.T) {
	base := map[string]string{"PANEL_DATABASE_URL": "postgres://x", "PANEL_BASE_URL": "https://a.example"}
	for k, v := range map[string]string{"PANEL_QUEUE_SIZE": "0", "PANEL_EVENT_PATH": "no-slash", "PANEL_FLUSH_EVERY": "-1s", "PANEL_BASE_URL": "ftp://x"} {
		m := map[string]string{}
		for a, b := range base {
			m[a] = b
		}
		m[k] = v
		if _, err := Load(env(m)); err == nil {
			t.Errorf("%s=%q accepted", k, v)
		}
	}
}
