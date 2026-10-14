package installer

import (
	"bytes"
	"context"
	"errors"
	"strings"
	"testing"
)

func TestSuggestBaseURL(t *testing.T) {
	t.Parallel()
	lanIP := func() (string, error) { return "192.168.1.20", nil }
	noIP := func() (string, error) { return "", errors.New("no network") }
	tests := []struct {
		name    string
		listen  string
		localIP func() (string, error)
		want    string
	}{
		{name: "loopback keeps the listen host", listen: "127.0.0.1:8787", localIP: lanIP, want: "http://127.0.0.1:8787"},
		{name: "all interfaces uses the detected address", listen: "0.0.0.0:9000", localIP: lanIP, want: "http://192.168.1.20:9000"},
		{name: "all interfaces without an address falls back to loopback", listen: "0.0.0.0:8787", localIP: noIP, want: "http://127.0.0.1:8787"},
		{name: "a specific address is used as is", listen: "10.0.0.5:8787", localIP: lanIP, want: "http://10.0.0.5:8787"},
	}
	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			t.Parallel()
			if got := suggestBaseURL(tt.listen, tt.localIP); got != tt.want {
				t.Fatalf("suggestBaseURL(%q) = %q, want %q", tt.listen, got, tt.want)
			}
		})
	}
}

func TestRunAsksListenFirstAndSuggestsDetectedURL(t *testing.T) {
	t.Parallel()
	var output bytes.Buffer
	// Listen on all interfaces, then stop at the URL prompt (end of input).
	p := NewPrompter(strings.NewReader("0.0.0.0:9000\n"), &output)
	sys := System{
		GOOS:    "darwin",
		LocalIP: func() (string, error) { return "192.168.1.20", nil },
	}
	_ = Run(context.Background(), Options{Mode: "binary"}, p, sys)
	got := output.String()
	listenAt := strings.Index(got, "Listen address")
	urlAt := strings.Index(got, "Public URL [http://192.168.1.20:9000]")
	if listenAt < 0 || urlAt < 0 || listenAt > urlAt {
		t.Fatalf("want the listen prompt before a prefilled public URL, got:\n%s", got)
	}
}
