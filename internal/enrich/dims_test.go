package enrich

import (
	"strings"
	"testing"
)

func TestScreenBucket(t *testing.T) {
	tests := []struct {
		name  string
		width int
		want  int16
	}{
		{name: "negative", width: -5, want: 0},
		{name: "zero", width: 0, want: 0},
		{name: "smallest mobile", width: 1, want: 1},
		{name: "largest mobile", width: 767, want: 1},
		{name: "smallest tablet", width: 768, want: 2},
		{name: "largest tablet", width: 1023, want: 2},
		{name: "smallest laptop", width: 1024, want: 3},
		{name: "largest laptop", width: 1439, want: 3},
		{name: "smallest desktop", width: 1440, want: 4},
		{name: "largest desktop", width: 1919, want: 4},
		{name: "smallest large", width: 1920, want: 5},
		{name: "large without int16 overflow", width: 40000, want: 5},
	}
	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			if got := ScreenBucket(tt.width); got != tt.want {
				t.Errorf("ScreenBucket(%d) = %d, want %d", tt.width, got, tt.want)
			}
		})
	}
}

func TestPrimaryLanguage(t *testing.T) {
	tests := []struct {
		name   string
		header string
		want   string
	}{
		{name: "weighted list", header: "en-US,en;q=0.9", want: "en"},
		{name: "uppercase", header: "EN-gb", want: "en"},
		{name: "script and region", header: "zh-Hant-TW,zh;q=0.8", want: "zh"},
		{name: "three letter language", header: "fil-PH", want: "fil"},
		{name: "higher quality wins", header: "fr;q=0.5, de-CH", want: "de"},
		{name: "tie keeps order", header: "fr;q=0.5,de;q=0.5", want: "fr"},
		{name: "underscore", header: "en_US", want: "en"},
		{name: "wildcard", header: "*", want: ""},
		{name: "wildcard does not hide language", header: "*;q=1,es;q=0.1", want: "es"},
		{name: "zero quality", header: "en;q=0", want: ""},
		{name: "empty", header: "", want: ""},
		{name: "private use", header: "x-klingon", want: ""},
		{name: "numeric", header: "12-34", want: ""},
		{name: "long primary", header: "engl-US", want: ""},
		{name: "invalid quality", header: "en;q=abc", want: ""},
		{name: "bounded entries", header: strings.Repeat(",", 5000), want: ""},
	}
	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			if got := PrimaryLanguage(tt.header); got != tt.want {
				t.Errorf("PrimaryLanguage(%q) = %q, want %q", tt.header, got, tt.want)
			}
		})
	}
}

func TestHostLabel(t *testing.T) {
	tests := []struct {
		name string
		host string
		want string
	}{
		{name: "port and case", host: "WWW.Example.COM:8443", want: "www.example.com"},
		{name: "trailing dot", host: "docs.example.com.", want: "docs.example.com"},
		{name: "plain", host: "example.com", want: "example.com"},
		{name: "empty", host: "", want: ""},
	}
	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			if got := HostLabel(tt.host); got != tt.want {
				t.Errorf("HostLabel(%q) = %q, want %q", tt.host, got, tt.want)
			}
		})
	}
}
