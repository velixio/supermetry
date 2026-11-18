package otlpingest

import (
	"bytes"
	"compress/gzip"
	"os"
	"testing"
)

// The log decoders face the public internet: any byte string must return an
// error or a request that NormaliseLogs survives, never panic.

func FuzzDecodeLogs(f *testing.F) {
	f.Add(marshalLogs(f, javaAgentLogs()), false)
	f.Add([]byte{}, false)
	f.Add([]byte{0x0a, 0xff, 0xff, 0xff, 0xff, 0x0f}, false) // huge length
	f.Add([]byte{0x0a, 0x05, 0x01}, false)                   // truncated
	f.Add([]byte(`{}`), true)
	f.Add([]byte(`{"resourceLogs":[{"scopeLogs":[{"logRecords":[{"severityNumber":"x","timeUnixNano":"zz"}]}]}]}`), true)
	f.Add([]byte(`{"resourceLogs":[{"scopeLogs":[{"logRecords":[{"body":{"arrayValue":{"values":[{"kvlistValue":{"values":[{"key":"k","value":{"doubleValue":"NaN"}}]}}]}},"traceId":"zz","spanId":"00"}]}]}]}`), true)
	for _, name := range []string{"java_agent.pb", "python_sdk.json", "node_sdk.json"} {
		if b, err := os.ReadFile("testdata/logs/" + name); err == nil {
			f.Add(b, name != "java_agent.pb")
		}
	}
	f.Fuzz(func(t *testing.T, b []byte, asJSON bool) {
		ct := ctProto
		if asJSON {
			ct = ctJSON
		}
		req, err := DecodeLogs(b, ct)
		if err != nil {
			return
		}
		CountLogRecords(req)
		NormaliseLogs(req, SiteContext{ID: 1, HashSecret: logSecret}, testNow)
	})
}

// A gzip bomb is refused with 413 and a truncated gzip with 400, before any
// decode.
func TestLogsBombsAndTruncation(t *testing.T) {
	r := newLogRig(t, 0)
	var bomb bytes.Buffer
	zw := gzip.NewWriter(&bomb)
	_, _ = zw.Write(make([]byte, MaxDecompressedBytes+1))
	_ = zw.Close()
	if w := r.post(bomb.Bytes(), ctProto, map[string]string{"Content-Encoding": "gzip"}); w.Code != 413 {
		t.Fatalf("bomb: %d, want 413", w.Code)
	}
	var ok bytes.Buffer
	zw = gzip.NewWriter(&ok)
	_, _ = zw.Write(marshalLogs(t, nRecords(50, "t")))
	_ = zw.Close()
	if w := r.post(ok.Bytes()[:ok.Len()/2], ctProto, map[string]string{"Content-Encoding": "gzip"}); w.Code != 400 {
		t.Fatalf("truncated gzip: %d, want 400", w.Code)
	}
	if w := r.post([]byte{0x0a, 0xff, 0xff, 0xff, 0xff, 0x0f}, ctProto, nil); w.Code != 400 {
		t.Fatalf("huge length: %d, want 400", w.Code)
	}
	if r.sink.count() != 0 {
		t.Fatal("stored records from a bad body")
	}
}
