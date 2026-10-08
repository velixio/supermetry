package main

import (
	"bytes"
	"os"
	"testing"
)

func TestMinify(t *testing.T) {
	t.Parallel()

	got, err := minify([]byte("var answer = 40 + 2; // comment\nvar label = 'two words';"))
	if err != nil {
		t.Fatal(err)
	}
	want := []byte(`var answer=40+2,label="two words"`)
	if !bytes.Equal(got, want) {
		t.Fatalf("minify() = %q, want %q", got, want)
	}
}

func TestStripFull(t *testing.T) {
	t.Parallel()

	source := []byte("a;/*@full*/b;/*@end*/c;")
	tests := []struct {
		name string
		keep bool
		want string
	}{
		{name: "core removes block", keep: false, want: "a;c;"},
		{name: "full removes markers", keep: true, want: "a;b;c;"},
	}
	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			t.Parallel()

			got, err := stripFull(source, tt.keep)
			if err != nil {
				t.Fatal(err)
			}
			if string(got) != tt.want {
				t.Fatalf("stripFull() = %q, want %q", got, tt.want)
			}
		})
	}
}

func TestStripFullRejectsBadMarkers(t *testing.T) {
	t.Parallel()

	tests := []struct {
		name   string
		source string
	}{
		{name: "unclosed full block", source: "/*@full*/a"},
		{name: "stray end marker", source: "a/*@end*/"},
		{name: "nested full block", source: "/*@full*//*@full*/a/*@end*//*@end*/"},
	}
	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			t.Parallel()

			if _, err := stripFull([]byte(tt.source), false); err == nil {
				t.Errorf("stripFull(%q) returned nil error", tt.source)
			}
		})
	}
}

func TestCheckedInMinifiedTrackerIsCurrent(t *testing.T) {
	t.Parallel()

	source, err := os.ReadFile("../../tracker.js")
	if err != nil {
		t.Fatal(err)
	}
	source, err = stripFull(source, false)
	if err != nil {
		t.Fatal(err)
	}
	got, err := minify(source)
	if err != nil {
		t.Fatal(err)
	}
	want, err := os.ReadFile("../../tracker.min.js")
	if err != nil {
		t.Fatal(err)
	}
	want = bytes.TrimSuffix(want, []byte{'\n'})
	if !bytes.Equal(got, want) {
		t.Fatal("tracker.min.js is stale; run go generate ./internal/tracker")
	}
}

func TestCheckedInMinifiedFullTrackerIsCurrent(t *testing.T) {
	t.Parallel()

	core, err := os.ReadFile("../../tracker.js")
	if err != nil {
		t.Fatal(err)
	}
	core, err = stripFull(core, true)
	if err != nil {
		t.Fatal(err)
	}
	extensions, err := os.ReadFile("../../tracker.extensions.js")
	if err != nil {
		t.Fatal(err)
	}
	got, err := minify(append(core, extensions...))
	if err != nil {
		t.Fatal(err)
	}
	want, err := os.ReadFile("../../tracker.full.min.js")
	if err != nil {
		t.Fatal(err)
	}
	want = bytes.TrimSuffix(want, []byte{'\n'})
	if !bytes.Equal(got, want) {
		t.Fatal("tracker.full.min.js is stale; run go generate ./internal/tracker")
	}
}
