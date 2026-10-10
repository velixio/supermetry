package synth

import (
	"bytes"
	"compress/gzip"
	"encoding/json"
	"io"
	"testing"
	"time"
)

func TestDemoRecordingDecodes(t *testing.T) {
	start := time.Date(2026, 10, 10, 9, 30, 0, 0, time.UTC)
	visit := DemoVisit{
		Start: start,
		Host:  DemoHosts[0],
		Hits: []DemoHit{
			{Offset: 0, Kind: 0, Path: "/", Clicks: [][2]int{{320, 240}}},
			{Offset: 20 * time.Second, Kind: 2, Path: "/", ScrollPct: 80},
			{Offset: 45 * time.Second, Kind: 0, Path: "/pricing", Clicks: [][2]int{{420, 360}}},
		},
	}

	zr, err := gzip.NewReader(bytes.NewReader(DemoRecording(visit)))
	if err != nil {
		t.Fatal(err)
	}
	plain, err := io.ReadAll(zr)
	if err != nil {
		t.Fatal(err)
	}
	if err := zr.Close(); err != nil {
		t.Fatal(err)
	}

	type event struct {
		Type      int             `json:"type"`
		Timestamp int64           `json:"timestamp"`
		Data      json.RawMessage `json:"data"`
	}
	lines := bytes.Split(bytes.TrimSpace(plain), []byte("\n"))
	if len(lines) < 6 {
		t.Fatalf("recording has %d events, want at least 6", len(lines))
	}
	events := make([]event, len(lines))
	for i, line := range lines {
		if err := json.Unmarshal(line, &events[i]); err != nil {
			t.Fatalf("line %d: %v", i+1, err)
		}
		if len(events[i].Data) == 0 {
			t.Fatalf("line %d has no data", i+1)
		}
	}
	if events[0].Type != 4 || events[1].Type != 2 {
		t.Fatalf("first event types = %d, %d; want 4, 2", events[0].Type, events[1].Type)
	}
	var snapshot struct {
		Node struct {
			ID int `json:"id"`
		} `json:"node"`
	}
	if err := json.Unmarshal(events[1].Data, &snapshot); err != nil {
		t.Fatal(err)
	}
	if snapshot.Node.ID != 1 {
		t.Fatalf("document node id = %d, want 1", snapshot.Node.ID)
	}
	sources := map[int]bool{}
	for _, event := range events {
		if event.Type != 3 {
			continue
		}
		var incremental struct {
			Source int `json:"source"`
		}
		if err := json.Unmarshal(event.Data, &incremental); err != nil {
			t.Fatal(err)
		}
		sources[incremental.Source] = true
	}
	for _, source := range []int{1, 2, 3} {
		if !sources[source] {
			t.Errorf("incremental source %d missing", source)
		}
	}
	lo, hi := start.UnixMilli(), start.Add(45*time.Second).UnixMilli()
	for i, event := range events {
		if event.Timestamp < lo || event.Timestamp > hi {
			t.Errorf("event %d timestamp %d outside [%d,%d]", i, event.Timestamp, lo, hi)
		}
		if i > 0 && event.Timestamp < events[i-1].Timestamp {
			t.Errorf("timestamps decrease at event %d", i)
		}
	}
}

func TestDemoRecordingIncludesMouseMovementWithoutClick(t *testing.T) {
	visit := DemoVisit{
		Start:   time.Date(2026, 10, 10, 9, 30, 0, 0, time.UTC),
		ScreenW: 1440,
		Hits:    []DemoHit{{Kind: 0, Path: "/docs/api"}, {Offset: time.Second, Kind: 2, Path: "/docs/api", ScrollPct: 50}},
	}
	zr, err := gzip.NewReader(bytes.NewReader(DemoRecording(visit)))
	if err != nil {
		t.Fatal(err)
	}
	plain, err := io.ReadAll(zr)
	if err != nil {
		t.Fatal(err)
	}
	if err := zr.Close(); err != nil {
		t.Fatal(err)
	}
	for _, line := range bytes.Split(bytes.TrimSpace(plain), []byte("\n")) {
		var event struct {
			Type int `json:"type"`
			Data struct {
				Source int `json:"source"`
			} `json:"data"`
		}
		if err := json.Unmarshal(line, &event); err != nil {
			t.Fatal(err)
		}
		if event.Type == 3 && event.Data.Source == 1 {
			return
		}
	}
	t.Fatal("recording has no mouse movement")
}
