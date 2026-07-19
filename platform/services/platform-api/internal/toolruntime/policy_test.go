package toolruntime

import (
	"testing"

	"github.com/qsyy0921/openim/platform/services/platform-api/internal/agent"
	"github.com/qsyy0921/openim/platform/services/platform-api/internal/capability"
)

func TestPolicySeparatesReadApprovalAndProactivePlanes(t *testing.T) {
	execution := agent.ExecutionContext{ExecutionPlane: "passive"}
	read := capability.Descriptor{Audience: "passive", Risk: "read", Permissions: []string{"knowledge:read"}}
	if got := (Policy{}).Decide(execution, read, []string{"knowledge:read"}); got.Outcome != "allow" {
		t.Fatalf("read decision = %#v", got)
	}
	write := capability.Descriptor{
		Audience: "passive", Risk: "write", Permissions: []string{"ticket:create"},
		Idempotency: "keyed", RetrySemantics: "reconcile_first",
	}
	if got := (Policy{}).Decide(execution, write, []string{"ticket:create"}); got.Outcome != "require_approval" {
		t.Fatalf("write decision = %#v", got)
	}
	write.Idempotency = "unknown"
	if got := (Policy{}).Decide(execution, write, []string{"ticket:create"}); got.Outcome != "deny" {
		t.Fatalf("unknown idempotency decision = %#v", got)
	}
	read.Audience = "proactive_source"
	read.RetrySemantics = "safe"
	execution.ExecutionPlane = "proactive_source"
	if got := (Policy{}).Decide(execution, read, []string{"knowledge:read"}); got.Outcome != "allow" {
		t.Fatalf("proactive read decision = %#v", got)
	}
	read.Risk = "external_side_effect"
	if got := (Policy{}).Decide(execution, read, []string{"knowledge:read"}); got.Outcome != "deny" {
		t.Fatalf("proactive side effect decision = %#v", got)
	}
}

func TestPolicyDeniesMissingPermissionAndAudienceMismatch(t *testing.T) {
	descriptor := capability.Descriptor{Audience: "passive", Risk: "read", Permissions: []string{"knowledge:read"}}
	if got := (Policy{}).Decide(agent.ExecutionContext{ExecutionPlane: "passive"}, descriptor, nil); got.Reason != "permission_missing" {
		t.Fatalf("missing permission = %#v", got)
	}
	if got := (Policy{}).Decide(agent.ExecutionContext{ExecutionPlane: "admin"}, descriptor, []string{"knowledge:read"}); got.Reason != "execution_plane_mismatch" {
		t.Fatalf("audience mismatch = %#v", got)
	}
}
