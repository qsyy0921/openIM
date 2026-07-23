package runtimecontrol

import (
	"context"
	"testing"
)

func TestSetRejectsUnknownComponentBeforeDatabaseAccess(t *testing.T) {
	store := &Store{}
	_, err := store.Set(context.Background(), "tenant", "actor", "shell_execution", true, "incident", 0)
	if err == nil {
		t.Fatal("unknown runtime component was accepted")
	}
}

func TestSetRejectsMissingAuditedReasonBeforeDatabaseAccess(t *testing.T) {
	store := &Store{}
	_, err := store.Set(context.Background(), "tenant", "actor", "agent_execution", true, "", 0)
	if err == nil {
		t.Fatal("runtime control without an audited reason was accepted")
	}
}
