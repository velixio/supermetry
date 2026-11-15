package appingest

import (
	"strings"
	"testing"
)

func decode(t *testing.T, raw string) map[string]any {
	t.Helper()
	m, err := decodeObject([]byte(raw))
	if err != nil {
		t.Fatal(err)
	}
	return m
}

func TestParseEventAcceptsFullEvent(t *testing.T) {
	e, err := ParseEvent(decode(t, `{
		"id":"ev-1","ts":1760000000000,"kind":"crash","level":"fatal","handled":false,
		"release":"1.2.3","build":"abc","environment":"production","platform":"linux","os_version":"6.1",
		"arch":"x64","device_class":"cuda","runtime":"python","error_type":"RuntimeError",
		"exit_reason":"oom","signal":"SIGKILL","exit_code":137,
		"frames":"/home/bob/app/a.py:3 f\nb.py:4 g","fingerprint":"AB12","occurrences":3,
		"install_id":"inst-1","session_id":"sess-1",
		"tags":{"stage":"load","n":7,"ok":true,"f":1.5}}`))
	if err != nil {
		t.Fatal(err)
	}
	if e.ID != "ev-1" || e.TS.UnixMilli() != 1760000000000 || e.Kind != "crash" || e.Level != "fatal" || e.Handled {
		t.Fatalf("core fields: %+v", e)
	}
	if e.Frames != "~/app/a.py:3 f\nb.py:4 g" {
		t.Fatalf("frames not scrubbed: %q", e.Frames)
	}
	if e.Fingerprint != "ab12" || e.Occurrences != 3 || e.ExitCode == nil || *e.ExitCode != 137 {
		t.Fatalf("fingerprint/occurrences/exit: %+v", e)
	}
	if e.InstallID != "inst-1" || e.SessionID != "sess-1" || e.ExitReason != "oom" || e.Signal != "SIGKILL" {
		t.Fatalf("ids: %+v", e)
	}
	want := map[string]string{"stage": "load", "n": "7", "ok": "true", "f": "1.5"}
	for k, v := range want {
		if e.Tags[k] != v {
			t.Errorf("tag %s = %q, want %q", k, e.Tags[k], v)
		}
	}
}

func TestParseEventDefaults(t *testing.T) {
	e, err := ParseEvent(decode(t, `{"id":"x","kind":"error"}`))
	if err != nil {
		t.Fatal(err)
	}
	if !e.TS.IsZero() || e.Level != "error" || e.Occurrences != 1 || e.Tags == nil {
		t.Fatalf("defaults: %+v", e)
	}
	c, err := ParseEvent(decode(t, `{"id":"x","kind":"crash"}`))
	if err != nil || c.Level != "fatal" {
		t.Fatalf("crash default level: %+v %v", c, err)
	}
	if e.Fingerprint == "" || len(e.Fingerprint) != 64 {
		t.Fatalf("fingerprint must be computed: %q", e.Fingerprint)
	}
}

func TestParseEventRejects(t *testing.T) {
	long := func(n int) string { return strings.Repeat("a", n) }
	cases := map[string]string{
		"missing id":          `{"kind":"error"}`,
		"empty id":            `{"id":"","kind":"error"}`,
		"id too long":         `{"id":"` + long(65) + `","kind":"error"}`,
		"id wrong type":       `{"id":5,"kind":"error"}`,
		"missing kind":        `{"id":"x"}`,
		"unknown kind":        `{"id":"x","kind":"boom"}`,
		"unknown level":       `{"id":"x","kind":"error","level":"loud"}`,
		"unknown platform":    `{"id":"x","kind":"error","platform":"beos"}`,
		"unknown arch":        `{"id":"x","kind":"error","arch":"mips"}`,
		"ts as string":        `{"id":"x","kind":"error","ts":"123"}`,
		"ts fractional":       `{"id":"x","kind":"error","ts":1.5}`,
		"handled as string":   `{"id":"x","kind":"error","handled":"yes"}`,
		"release too long":    `{"id":"x","kind":"error","release":"` + long(65) + `"}`,
		"build too long":      `{"id":"x","kind":"error","build":"` + long(65) + `"}`,
		"environment long":    `{"id":"x","kind":"error","environment":"` + long(33) + `"}`,
		"os_version long":     `{"id":"x","kind":"error","os_version":"` + long(33) + `"}`,
		"device_class long":   `{"id":"x","kind":"error","device_class":"` + long(33) + `"}`,
		"runtime long":        `{"id":"x","kind":"error","runtime":"` + long(33) + `"}`,
		"error_type long":     `{"id":"x","kind":"error","error_type":"` + long(65) + `"}`,
		"exit_code string":    `{"id":"x","kind":"error","exit_code":"1"}`,
		"exit_code huge":      `{"id":"x","kind":"error","exit_code":99999999999}`,
		"occurrences zero":    `{"id":"x","kind":"error","occurrences":0}`,
		"occurrences huge":    `{"id":"x","kind":"error","occurrences":2000000}`,
		"install_id long":     `{"id":"x","kind":"error","install_id":"` + long(129) + `"}`,
		"session_id long":     `{"id":"x","kind":"error","session_id":"` + long(65) + `"}`,
		"frames wrong type":   `{"id":"x","kind":"error","frames":["a"]}`,
		"tags not object":     `{"id":"x","kind":"error","tags":[1]}`,
		"tag value too long":  `{"id":"x","kind":"error","tags":{"k":"` + long(201) + `"}}`,
		"tag key too long":    `{"id":"x","kind":"error","tags":{"` + long(65) + `":"v"}}`,
		"tag empty key":       `{"id":"x","kind":"error","tags":{"":"v"}}`,
		"tag value null":      `{"id":"x","kind":"error","tags":{"k":null}}`,
		"tag value object":    `{"id":"x","kind":"error","tags":{"k":{}}}`,
		"too many tags":       `{"id":"x","kind":"error","tags":{` + manyTags(21) + `}}`,
		"nul byte":            `{"id":"x","kind":"error","release":"a\u0000b"}`,
		"exit_reason bad":     `{"id":"x","kind":"error","exit_reason":"has space"}`,
		"level wrong type": `{"id":"x","kind":"error","level":5}`,
	}
	for name, raw := range cases {
		t.Run(name, func(t *testing.T) {
			if _, err := ParseEvent(decode(t, raw)); err == nil {
				t.Fatal("expected an error")
			}
		})
	}
}

func manyTags(n int) string {
	parts := make([]string, n)
	for i := range parts {
		parts[i] = `"k` + strings.Repeat("x", i) + `":"v"`
	}
	return strings.Join(parts, ",")
}

func TestParseEventLimitsAtBoundary(t *testing.T) {
	long := func(n int) string { return strings.Repeat("a", n) }
	raw := `{"id":"` + long(64) + `","kind":"error","release":"` + long(64) + `","environment":"` + long(32) +
		`","install_id":"` + long(128) + `","session_id":"` + long(64) + `","tags":{"` + long(64) + `":"` + long(200) + `"}}`
	if _, err := ParseEvent(decode(t, raw)); err != nil {
		t.Fatal(err)
	}
}

func TestParseEventIgnoresUnknownFieldsAndBadFingerprintFallsBack(t *testing.T) {
	e, err := ParseEvent(decode(t, `{"id":"x","kind":"error","mystery":[1,2],"fingerprint":"zz-not-hex","error_type":"E"}`))
	if err != nil {
		t.Fatal(err)
	}
	if len(e.Fingerprint) != 64 || e.Fingerprint == "zz-not-hex" {
		t.Fatalf("fingerprint = %q", e.Fingerprint)
	}
}
