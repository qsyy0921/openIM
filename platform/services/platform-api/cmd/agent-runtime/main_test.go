package main

import (
	"testing"

	"github.com/qsyy0921/openim/platform/services/platform-api/internal/toolruntime"
)

func TestDecodeKnowledgeResultRejectsNonSuccessfulToolState(t *testing.T) {
	for _, state := range []string{"prepared", "executing", "failed", "unknown", "waiting_approval"} {
		t.Run(state, func(t *testing.T) {
			if _, err := decodeKnowledgeResult(toolruntime.PreparedCall{State: state}, nil); err == nil {
				t.Fatalf("state %q was accepted", state)
			}
		})
	}
}

func TestDecodeKnowledgeResultMapsDeniedAccessToEmptyEvidence(t *testing.T) {
	items, err := decodeKnowledgeResult(toolruntime.PreparedCall{State: "denied"}, nil)
	if err != nil || items == nil || len(items) != 0 {
		t.Fatalf("items=%#v err=%v", items, err)
	}
}

func TestDecodeKnowledgeResultRejectsMissingSuccessfulPayload(t *testing.T) {
	if _, err := decodeKnowledgeResult(toolruntime.PreparedCall{State: "succeeded"}, nil); err == nil {
		t.Fatal("missing successful payload was accepted")
	}
}
