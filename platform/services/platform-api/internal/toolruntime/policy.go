package toolruntime

import (
	"slices"

	"github.com/qsyy0921/openim/platform/services/platform-api/internal/agent"
	"github.com/qsyy0921/openim/platform/services/platform-api/internal/capability"
)

type Decision struct {
	Outcome string
	Reason  string
}

type Policy struct{}

func (Policy) Decide(execution agent.ExecutionContext, descriptor capability.Descriptor, grants []string) Decision {
	if descriptor.Audience != execution.ExecutionPlane {
		return Decision{Outcome: "deny", Reason: "execution_plane_mismatch"}
	}
	for _, permission := range descriptor.Permissions {
		if !slices.Contains(grants, permission) {
			return Decision{Outcome: "deny", Reason: "permission_missing"}
		}
	}
	if execution.ExecutionPlane == "proactive_source" && (descriptor.Risk != "read" || descriptor.RetrySemantics != "safe") {
		return Decision{Outcome: "deny", Reason: "proactive_side_effect_forbidden"}
	}
	switch descriptor.Risk {
	case "read":
		return Decision{Outcome: "allow", Reason: "authorized_read"}
	case "write", "external_side_effect", "privileged":
		if descriptor.Idempotency == "none" || descriptor.Idempotency == "unknown" {
			return Decision{Outcome: "deny", Reason: "side_effect_idempotency_unsupported"}
		}
		return Decision{Outcome: "require_approval", Reason: "side_effect_requires_approval"}
	default:
		return Decision{Outcome: "deny", Reason: "unsupported_risk"}
	}
}
