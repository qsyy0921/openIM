package observe

import (
	"bytes"
	"encoding/json"
	"testing"
)

func TestCanonicalObjectIsDeterministic(t *testing.T) {
	left, err := canonicalObject([]byte(`{"z":1,"a":{"y":2,"b":3}}`))
	if err != nil {
		t.Fatal(err)
	}
	right, err := canonicalObject([]byte(`{"a":{"b":3,"y":2},"z":1}`))
	if err != nil {
		t.Fatal(err)
	}
	if !bytes.Equal(left, right) {
		t.Fatalf("canonical JSON differs: %s != %s", left, right)
	}
}

func TestReplayToolCallContainsDigestButNotRawArguments(t *testing.T) {
	bundle := Bundle{
		SchemaVersion: 1,
		ToolCalls: []ToolCall{{
			CallID: "selected-operation", OperationID: "calendar.event.create",
			ArgumentsDigest: "sha256:aaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaa",
		}},
		Lifecycle: []LifecycleEvent{}, Citations: []Citation{}, MemoryExposures: []MemoryExposure{},
	}
	encoded, err := json.Marshal(bundle)
	if err != nil {
		t.Fatal(err)
	}
	if bytes.Contains(encoded, []byte(`"arguments":`)) || !bytes.Contains(encoded, []byte(`"arguments_digest":`)) {
		t.Fatalf("replay tool redaction is invalid: %s", encoded)
	}
}

func TestReplayChecksumDetectsMutation(t *testing.T) {
	bundle := Bundle{
		SchemaVersion: 1,
		Run:           RunSnapshot{ID: "run-1", State: "completed", CandidateText: "answer"},
		Lifecycle:     []LifecycleEvent{}, ToolCalls: []ToolCall{}, Citations: []Citation{}, MemoryExposures: []MemoryExposure{},
	}
	checksum, err := replayChecksum(bundle)
	if err != nil {
		t.Fatal(err)
	}
	bundle.Checksum = checksum
	valid, err := VerifyChecksum(bundle)
	if err != nil || !valid {
		t.Fatalf("expected valid replay checksum, valid=%v err=%v", valid, err)
	}
	bundle.Run.CandidateText = "changed"
	valid, err = VerifyChecksum(bundle)
	if err != nil {
		t.Fatal(err)
	}
	if valid {
		t.Fatal("mutated replay bundle retained a valid checksum")
	}
}
