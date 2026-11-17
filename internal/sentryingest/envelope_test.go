package sentryingest

import (
	"bytes"
	"errors"
	"fmt"
	"os"
	"path/filepath"
	"strings"
	"testing"
)

func parse(t *testing.T, in string) Envelope {
	t.Helper()
	env, err := ParseEnvelope(strings.NewReader(in), Limits{})
	if err != nil {
		t.Fatalf("ParseEnvelope: %v", err)
	}
	return env
}

func TestParseEnvelopeHeaderAndItems(t *testing.T) {
	env := parse(t, `{"event_id":"abc","dsn":"https://0123456789abcdef0123456789abcdef@h/42","sent_at":"2026-11-17T13:11:27.301Z","sdk":{"name":"sentry.go","version":"0.50.0"}}
{"type":"event"}
{"message":"hi"}
{"type":"session"}
{"sid":"x"}
`)
	h := env.Header
	if h.EventID != "abc" || h.SDK.Name != "sentry.go" || h.SentAt.IsZero() || !strings.Contains(h.DSN, "@h/42") {
		t.Fatalf("header = %+v", h)
	}
	if len(env.Items) != 2 || env.Items[0].Type != "event" || string(env.Items[0].Payload) != `{"message":"hi"}` || string(env.Items[1].Payload) != `{"sid":"x"}` {
		t.Fatalf("items = %+v", env.Items)
	}
}

func TestParseEnvelopeLengthPrefixedBinaryPayload(t *testing.T) {
	payload := "MDMP\n\x00\x01\n{\"type\":\"event\"}\n"
	in := fmt.Sprintf("{}\n{\"type\":\"attachment\",\"length\":%d,\"attachment_type\":\"event.minidump\",\"filename\":\"a.dmp\"}\n%s\n{\"type\":\"event\"}\n{\"a\":1}\n", len(payload), payload)
	env := parse(t, in)
	if len(env.Items) != 2 {
		t.Fatalf("items = %d: %+v", len(env.Items), env.Items)
	}
	a := env.Items[0]
	if string(a.Payload) != payload || a.AttachmentType != "event.minidump" || a.Filename != "a.dmp" {
		t.Fatalf("attachment = %+v", a)
	}
	if env.Items[1].Type != "event" || string(env.Items[1].Payload) != `{"a":1}` {
		t.Fatalf("second item = %+v", env.Items[1])
	}
}

func TestParseEnvelopeTrailingItemWithoutLengthOrFinalNewline(t *testing.T) {
	env := parse(t, "{}\n{\"type\":\"event\"}\n{\"a\":1}\n{\"type\":\"client_report\"}\n{\"discarded_events\":[]}")
	if len(env.Items) != 2 || string(env.Items[1].Payload) != `{"discarded_events":[]}` {
		t.Fatalf("items = %+v", env.Items)
	}
	// Length-prefixed last item with no newline after it.
	env = parse(t, "{}\n{\"type\":\"event\",\"length\":7}\n{\"a\":1}")
	if len(env.Items) != 1 || string(env.Items[0].Payload) != `{"a":1}` {
		t.Fatalf("items = %+v", env.Items)
	}
	// Header only, no trailing newline.
	if env := parse(t, `{"event_id":"x"}`); len(env.Items) != 0 || env.Header.EventID != "x" {
		t.Fatalf("header-only = %+v", env)
	}
}

func TestParseEnvelopeToleratesBlankLinesAndCRLF(t *testing.T) {
	env := parse(t, "{}\r\n\r\n{\"type\":\"event\"}\r\n{\"a\":1}\r\n\r\n")
	if len(env.Items) != 1 || string(env.Items[0].Payload) != `{"a":1}` {
		t.Fatalf("items = %+v", env.Items)
	}
}

func TestParseEnvelopeKeepsUnknownItemTypes(t *testing.T) {
	env := parse(t, "{}\n{\"type\":\"made_up\"}\n{}\n{\"type\":\"event\"}\n{}\n")
	if len(env.Items) != 2 || env.Items[0].Type != "made_up" {
		t.Fatalf("items = %+v", env.Items)
	}
}

func TestParseEnvelopeMaxItems(t *testing.T) {
	build := func(n int) string {
		var b strings.Builder
		b.WriteString("{}\n")
		for i := 0; i < n; i++ {
			b.WriteString("{\"type\":\"event\"}\n{}\n")
		}
		return b.String()
	}
	if _, err := ParseEnvelope(strings.NewReader(build(500)), Limits{}); err != nil {
		t.Fatalf("500 items: %v", err)
	}
	if _, err := ParseEnvelope(strings.NewReader(build(501)), Limits{}); !errors.Is(err, ErrTooManyItem) {
		t.Fatalf("501 items: %v", err)
	}
}

func TestParseEnvelopeMalformed(t *testing.T) {
	for name, in := range map[string]string{
		"empty":                ``,
		"header not json":      "nope\n",
		"header not an object": "[]\n",
		"item header not json": "{}\nnope\n{}\n",
		"length past the body": "{}\n{\"type\":\"event\",\"length\":99}\n{}\n",
		"negative length":      "{}\n{\"type\":\"event\",\"length\":-1}\n{}\n",
		"event_id wrong type":  "{\"event_id\":5}\n",
		"item type wrong type": "{}\n{\"type\":5}\n{}\n",
	} {
		if _, err := ParseEnvelope(strings.NewReader(in), Limits{}); !errors.Is(err, ErrMalformed) {
			t.Errorf("%s: err = %v, want malformed", name, err)
		}
	}
}

func TestParseEnvelopeTooLarge(t *testing.T) {
	big := "{}\n{\"type\":\"event\"}\n" + strings.Repeat("x", 2000) + "\n"
	if _, err := ParseEnvelope(strings.NewReader(big), Limits{MaxBytes: 1000}); !errors.Is(err, ErrTooLarge) {
		t.Fatalf("err = %v", err)
	}
}

func fixtureFiles(t testing.TB) []string {
	t.Helper()
	files, err := filepath.Glob("testdata/*/*.envelope")
	if err != nil || len(files) == 0 {
		t.Fatalf("no fixtures: %v", err)
	}
	return files
}

func FuzzParseEnvelope(f *testing.F) {
	for _, file := range fixtureFiles(f) {
		raw, err := os.ReadFile(file)
		if err != nil {
			f.Fatal(err)
		}
		f.Add(raw)
	}
	f.Add([]byte("{}\n{\"type\":\"event\",\"length\":3}\nabc"))
	f.Add([]byte("{\"event_id\":\"x\"}\n{\"type\":\"attachment\",\"length\":0}\n\n"))
	f.Fuzz(func(t *testing.T, data []byte) {
		env, err := ParseEnvelope(bytes.NewReader(data), Limits{MaxBytes: 1 << 20})
		if err != nil {
			return
		}
		if len(env.Items) > MaxItems {
			t.Fatalf("%d items", len(env.Items))
		}
		total := 0
		for _, it := range env.Items {
			total += len(it.Payload)
		}
		if total > len(data) {
			t.Fatalf("payloads (%d) exceed the input (%d)", total, len(data))
		}
	})
}
