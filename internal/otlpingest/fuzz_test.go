package otlpingest

import (
	"os"
	"testing"
)

// The decoders face the public internet: any byte string must return an
// error or a request that Convert survives, never panic.

func FuzzDecodeProto(f *testing.F) {
	f.Add(marshalRequest(goHTTP()))
	f.Add(marshalRequest(nodeExpress()))
	f.Add([]byte{})
	f.Add([]byte{0x0a, 0xff, 0xff, 0xff, 0xff, 0x0f})
	if b, err := os.ReadFile("testdata/go-http.pb"); err == nil {
		f.Add(b)
	}
	f.Fuzz(func(t *testing.T, b []byte) {
		req, err := DecodeProto(b)
		if err != nil {
			return
		}
		Convert(req, testSite(), testNow)
	})
}

func FuzzDecodeJSON(f *testing.F) {
	f.Add(jsonOf(goHTTP()))
	f.Add(jsonOf(nodeExpress()))
	f.Add([]byte(`{}`))
	f.Add([]byte(`{"resourceSpans":[{"scopeSpans":[{"spans":[{"traceId":"zz","kind":"SPAN_KIND_SERVER","startTimeUnixNano":"-1","status":{"code":"STATUS_CODE_ERROR"}}]}]}]}`))
	if b, err := os.ReadFile("testdata/python-flask.json"); err == nil {
		f.Add(b)
	}
	f.Fuzz(func(t *testing.T, b []byte) {
		req, err := DecodeJSON(b)
		if err != nil {
			return
		}
		Convert(req, testSite(), testNow)
	})
}

func FuzzParameterise(f *testing.F) {
	f.Add("select * from t where a = 'x' and b = 1")
	f.Add("https://u:p@h/a/1?q#f")
	f.Add("/*")
	f.Add("'")
	f.Fuzz(func(t *testing.T, s string) {
		if out := parameteriseSQL(s); len(out) > 1000 {
			t.Fatal("too long")
		}
		parameteriseName(s)
		parameteriseURL(s)
	})
}
