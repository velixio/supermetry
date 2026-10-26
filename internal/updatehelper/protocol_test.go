package updatehelper

import (
	"bytes"
	"encoding/binary"
	"testing"
)

func TestProtocolRoundTrip(t *testing.T) {
	t.Parallel()

	want := Request{Version: ProtocolVersion, Operation: OperationStart, JobID: "42", TargetVersion: "v1.2.3", Nonce: "AAAAAAAAAAAAAAAAAAAAAAAAAAAAAAAAAAAAAAAAAAA"}
	var wire bytes.Buffer
	if err := WriteRequest(&wire, want); err != nil {
		t.Fatalf("WriteRequest: %v", err)
	}
	got, err := ReadRequest(&wire)
	if err != nil {
		t.Fatalf("ReadRequest: %v", err)
	}
	if got != want {
		t.Fatalf("request = %#v, want %#v", got, want)
	}
}

func TestStatusRequestRoundTrip(t *testing.T) {
	t.Parallel()
	want := Request{Version: ProtocolVersion, Operation: OperationStatus, JobID: "42"}
	var wire bytes.Buffer
	if err := WriteRequest(&wire, want); err != nil {
		t.Fatal(err)
	}
	got, err := ReadRequest(&wire)
	if err != nil || got != want {
		t.Fatalf("request=%#v err=%v", got, err)
	}
}

func TestResponseRoundTrip(t *testing.T) {
	t.Parallel()
	want := Response{Version: ProtocolVersion, JobID: "42", State: StateFailed, ErrorCode: "step_failed"}
	var wire bytes.Buffer
	if err := WriteResponse(&wire, want); err != nil {
		t.Fatalf("WriteResponse: %v", err)
	}
	got, err := ReadResponse(&wire)
	if err != nil {
		t.Fatalf("ReadResponse: %v", err)
	}
	if got != want {
		t.Fatalf("response = %#v, want %#v", got, want)
	}
}

func TestReadRequestRejectsInvalidInput(t *testing.T) {
	t.Parallel()

	tests := []struct {
		name string
		json string
	}{
		{name: "unknown field", json: `{"version":1,"operation":"start","job_id":"42","target_version":"v1.2.3","nonce":"AAAAAAAAAAAAAAAAAAAAAAAAAAAAAAAAAAAAAAAAAAA","url":"https://supermetry.com"}`},
		{name: "duplicate field", json: `{"version":1,"operation":"start","job_id":"42","job_id":"43","target_version":"v1.2.3","nonce":"AAAAAAAAAAAAAAAAAAAAAAAAAAAAAAAAAAAAAAAAAAA"}`},
		{name: "wrong field case", json: `{"Version":1,"job_id":"42","target_version":"v1.2.3","nonce":"AAAAAAAAAAAAAAAAAAAAAAAAAAAAAAAAAAAAAAAAAAA"}`},
		{name: "wrong protocol", json: `{"version":2,"operation":"start","job_id":"42","target_version":"v1.2.3","nonce":"AAAAAAAAAAAAAAAAAAAAAAAAAAAAAAAAAAAAAAAAAAA"}`},
		{name: "prerelease", json: `{"version":1,"operation":"start","job_id":"42","target_version":"v1.2.3-rc.1","nonce":"AAAAAAAAAAAAAAAAAAAAAAAAAAAAAAAAAAAAAAAAAAA"}`},
		{name: "build metadata", json: `{"version":1,"operation":"start","job_id":"42","target_version":"v1.2.3+build","nonce":"AAAAAAAAAAAAAAAAAAAAAAAAAAAAAAAAAAAAAAAAAAA"}`},
		{name: "unpadded version", json: `{"version":1,"operation":"start","job_id":"42","target_version":"1.2.3","nonce":"AAAAAAAAAAAAAAAAAAAAAAAAAAAAAAAAAAAAAAAAAAA"}`},
		{name: "path-like job", json: `{"version":1,"operation":"start","job_id":"../escape","target_version":"v1.2.3","nonce":"AAAAAAAAAAAAAAAAAAAAAAAAAAAAAAAAAAAAAAAAAAA"}`},
		{name: "zero job", json: `{"version":1,"operation":"start","job_id":"0","target_version":"v1.2.3","nonce":"AAAAAAAAAAAAAAAAAAAAAAAAAAAAAAAAAAAAAAAAAAA"}`},
		{name: "leading-zero job", json: `{"version":1,"operation":"start","job_id":"042","target_version":"v1.2.3","nonce":"AAAAAAAAAAAAAAAAAAAAAAAAAAAAAAAAAAAAAAAAAAA"}`},
		{name: "bigint overflow", json: `{"version":1,"operation":"start","job_id":"9223372036854775808","target_version":"v1.2.3","nonce":"AAAAAAAAAAAAAAAAAAAAAAAAAAAAAAAAAAAAAAAAAAA"}`},
		{name: "short nonce", json: `{"version":1,"operation":"start","job_id":"42","target_version":"v1.2.3","nonce":"short"}`},
		{name: "noncanonical nonce", json: `{"version":1,"operation":"start","job_id":"42","target_version":"v1.2.3","nonce":"___________________________________________"}`},
		{name: "trailing json", json: `{"version":1,"operation":"start","job_id":"42","target_version":"v1.2.3","nonce":"AAAAAAAAAAAAAAAAAAAAAAAAAAAAAAAAAAAAAAAAAAA"}{}`},
	}
	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			var wire bytes.Buffer
			_ = binary.Write(&wire, binary.BigEndian, uint32(len(tt.json)))
			wire.WriteString(tt.json)
			if _, err := ReadRequest(&wire); err == nil {
				t.Fatal("ReadRequest accepted invalid request")
			}
		})
	}
}

func TestReadRequestRejectsOversizedFrameBeforeReadingBody(t *testing.T) {
	t.Parallel()
	var wire bytes.Buffer
	_ = binary.Write(&wire, binary.BigEndian, uint32(MaxFrameSize+1))
	if _, err := ReadRequest(&wire); err == nil {
		t.Fatal("ReadRequest accepted oversized frame")
	}
}
