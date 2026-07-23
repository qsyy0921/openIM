package agent

import (
	"context"
	"errors"
	"strings"
)

type ExecutionContext struct {
	RunID, TraceID, TenantID, MemberID                               string
	SourceChannel, ConversationID, ExecutionPlane                    string
	AgentID, AgentVersionID, AgentSpecChecksum, CapabilitySnapshotID string
}

func (c ExecutionContext) Validate() error {
	values := []string{
		c.RunID, c.TraceID, c.TenantID, c.MemberID, c.SourceChannel,
		c.ConversationID, c.ExecutionPlane, c.AgentID, c.AgentVersionID, c.AgentSpecChecksum, c.CapabilitySnapshotID,
	}
	for _, value := range values {
		if value == "" || value != strings.TrimSpace(value) {
			return errors.New("Agent execution context contains an empty or untrimmed identity")
		}
	}
	switch c.ExecutionPlane {
	case "passive", "proactive_source", "internal", "admin":
		return nil
	default:
		return errors.New("Agent execution context has an unsupported execution plane")
	}
}

type executionContextKey struct{}

func BindExecutionContext(ctx context.Context, execution ExecutionContext) (context.Context, error) {
	if err := execution.Validate(); err != nil {
		return nil, err
	}
	return context.WithValue(ctx, executionContextKey{}, execution), nil
}

func RequireExecutionContext(ctx context.Context) (ExecutionContext, error) {
	execution, ok := ctx.Value(executionContextKey{}).(ExecutionContext)
	if !ok {
		return ExecutionContext{}, errors.New("Agent execution context is not bound")
	}
	return execution, nil
}

func (r Run) ExecutionContext() ExecutionContext {
	return ExecutionContext{
		RunID: r.ID, TraceID: r.TraceID, TenantID: r.TenantID, MemberID: r.MemberID,
		SourceChannel: r.SourceChannel, ConversationID: r.ConversationID, ExecutionPlane: r.ExecutionPlane,
		AgentID: r.AgentID, AgentVersionID: r.AgentVersionID, AgentSpecChecksum: r.AgentSpecChecksum,
		CapabilitySnapshotID: r.CapabilitySnapshotID,
	}
}
