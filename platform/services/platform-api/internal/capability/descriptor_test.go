package capability

import (
	"encoding/json"
	"testing"
	"time"
)

func TestSeedCapabilitySnapshotValidates(t *testing.T) {
	snapshot := Snapshot{
		ID:            "capability-v1:42f8f45ed2cb952a38df4decfb321434b615bdbecb6c159ef65bb3b5c8f7aa6b",
		SchemaVersion: 1,
		Payload:       json.RawMessage(`{"schema_version":1,"tools":[{"operation_id":"collaboration.ticket.create","version":"1"},{"operation_id":"enterprise.knowledge.search","version":"1"}]}`),
		Tools: []Descriptor{
			seedDescriptor("collaboration.ticket.create", "sha256:2d6102b1b02a9f47ec9801476a8e4a2a1427acc542b3c0ab74eb4d97cc26e166", `{"additionalProperties":false,"properties":{"title":{"maxLength":200,"minLength":1,"type":"string"}},"required":["title"],"type":"object"}`),
			seedDescriptor("enterprise.knowledge.search", "sha256:0c4e49dd7d99b838c21ef44a64454a8154a6123edb27b7adfc625a582890c3ae", `{"additionalProperties":false,"properties":{"limit":{"maximum":8,"minimum":1,"type":"integer"},"query":{"maxLength":2000,"minLength":1,"type":"string"}},"required":["query"],"type":"object"}`),
		},
	}
	if err := snapshot.Validate(); err != nil {
		t.Fatal(err)
	}
	if _, ok := snapshot.Tool("collaboration.ticket.create", "passive"); !ok {
		t.Fatal("pinned passive tool is not visible")
	}
	if _, ok := snapshot.Tool("collaboration.ticket.create", "admin"); ok {
		t.Fatal("passive tool leaked into admin execution plane")
	}
}

func TestSnapshotRejectsPayloadToolMismatch(t *testing.T) {
	snapshot := Snapshot{
		ID:            "capability-v1:4b333330137818f6f7e2ddf7a83c745ba434ff0e53f9cc23f131efc015628402",
		SchemaVersion: 1, Payload: json.RawMessage(`{"schema_version":1,"tools":[]}`),
		Tools: []Descriptor{seedDescriptor("enterprise.knowledge.search", "sha256:0c4e49dd7d99b838c21ef44a64454a8154a6123edb27b7adfc625a582890c3ae", `{"additionalProperties":false,"properties":{"limit":{"maximum":8,"minimum":1,"type":"integer"},"query":{"maxLength":2000,"minLength":1,"type":"string"}},"required":["query"],"type":"object"}`)},
	}
	if err := snapshot.Validate(); err == nil {
		t.Fatal("snapshot payload mismatch was accepted")
	}
}

func TestNormalizeToolRefsDoesNotMutateCallerInput(t *testing.T) {
	input := []ToolRef{{OperationID: " enterprise.knowledge.search ", Version: " 1 "}}
	got, err := normalizeToolRefs(input)
	if err != nil {
		t.Fatal(err)
	}
	if got[0].OperationID != "enterprise.knowledge.search" || got[0].Version != "1" {
		t.Fatalf("unexpected normalized tool reference: %#v", got[0])
	}
	if input[0].OperationID != " enterprise.knowledge.search " || input[0].Version != " 1 " {
		t.Fatal("normalization mutated caller-owned input")
	}
}

func TestDescriptorRejectsMalformedPermission(t *testing.T) {
	descriptor := seedDescriptor("enterprise.knowledge.search", "sha256:0c4e49dd7d99b838c21ef44a64454a8154a6123edb27b7adfc625a582890c3ae", `{"additionalProperties":false,"properties":{"limit":{"maximum":8,"minimum":1,"type":"integer"},"query":{"maxLength":2000,"minLength":1,"type":"string"}},"required":["query"],"type":"object"}`)
	descriptor.Permissions = []string{"knowledge read"}
	if err := descriptor.Validate(); err == nil {
		t.Fatal("malformed capability permission was accepted")
	}
}

func seedDescriptor(operationID, digest, schema string) Descriptor {
	return Descriptor{
		ID: "tool-1", OperationID: operationID, Version: "1", Name: operationID, Summary: "bounded tool",
		SourceType: "core", SourceID: "test", Risk: "read", Permissions: []string{"knowledge:read"},
		Idempotency: "native", RetrySemantics: "safe", Audience: "passive", Timeout: time.Second,
		ParameterTerms: []string{"query"}, Examples: []string{"find policy"}, OutputKinds: []string{"text"},
		InputSchema: json.RawMessage(schema), SchemaDigest: digest,
	}
}
