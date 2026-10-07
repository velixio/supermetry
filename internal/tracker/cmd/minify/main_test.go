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
	want := []byte("var answer=40+2;var label='two words';")
	if !bytes.Equal(got, want) {
		t.Fatalf("minify() = %q, want %q", got, want)
	}
}

func TestCheckedInMinifiedTrackerIsCurrent(t *testing.T) {
	t.Parallel()

	source, err := os.ReadFile("../../tracker.js")
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
