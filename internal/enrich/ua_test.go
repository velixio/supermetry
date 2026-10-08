package enrich

import "testing"

func TestVersionLabels(t *testing.T) {
	browsers := []struct {
		name    string
		browser string
		version string
		want    string
	}{
		{name: "chrome", browser: "Chrome", version: "126.0.6478.127", want: "Chrome 126"},
		{name: "firefox", browser: "Firefox", version: "131.0", want: "Firefox 131"},
		{name: "missing version", browser: "Chrome", version: "", want: ""},
		{name: "missing browser", browser: "", version: "126.0", want: ""},
		{name: "nonnumeric version", browser: "Edge", version: "beta", want: ""},
	}
	for _, tt := range browsers {
		t.Run("browser "+tt.name, func(t *testing.T) {
			if got := browserVersionLabel(tt.browser, tt.version); got != tt.want {
				t.Errorf("browserVersionLabel(%q,%q) = %q, want %q", tt.browser, tt.version, got, tt.want)
			}
		})
	}

	systems := []struct {
		name    string
		os      string
		version string
		want    string
	}{
		{name: "windows 10 or 11", os: "Windows", version: "10.0", want: "Windows 10/11"},
		{name: "windows 7", os: "Windows", version: "6.1", want: "Windows 7"},
		{name: "windows 8.1", os: "Windows", version: "6.3", want: "Windows 8.1"},
		{name: "frozen macOS", os: "macOS", version: "10_15_7", want: "macOS 10.15"},
		{name: "modern macOS", os: "macOS", version: "14.5", want: "macOS 14"},
		{name: "android", os: "Android", version: "14", want: "Android 14"},
		{name: "iOS", os: "iOS", version: "17.5.1", want: "iOS 17"},
		{name: "linux architecture is not version", os: "Linux", version: "x86_64", want: ""},
		{name: "missing linux version", os: "Linux", version: "", want: ""},
		{name: "unsupported windows", os: "Windows", version: "5.1", want: ""},
	}
	for _, tt := range systems {
		t.Run("OS "+tt.name, func(t *testing.T) {
			if got := osVersionLabel(tt.os, tt.version); got != tt.want {
				t.Errorf("osVersionLabel(%q,%q) = %q, want %q", tt.os, tt.version, got, tt.want)
			}
		})
	}
}
