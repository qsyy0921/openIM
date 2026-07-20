package toolruntime

import (
	"bytes"
	"context"
	"crypto/sha256"
	"encoding/hex"
	"encoding/json"
	"errors"
	"fmt"
	"regexp"
	"time"

	"github.com/qsyy0921/openim/platform/services/platform-api/internal/agent"
	"github.com/qsyy0921/openim/platform/services/platform-api/internal/capability"
	"github.com/santhosh-tekuri/jsonschema/v6"
)

var callIDPattern = regexp.MustCompile(`^[a-zA-Z0-9][a-zA-Z0-9_-]{0,127}$`)

var (
	ErrToolCallFailed         = errors.New("tool call previously failed")
	ErrToolCallOutcomeUnknown = errors.New("tool call outcome is unknown")
)

type CallRequest struct {
	CallID      string
	OperationID string
	Arguments   map[string]any
}

type PreparedCall struct {
	ID, CallID, OperationID, State, PolicyReason, ApprovalID string
	Result                                                   json.RawMessage
}

type CallRecord struct {
	ID, TenantID, RunID, CallID, OperationID, State string
	Risk, LeaseIdentity                             string
	Arguments                                       map[string]any
}

type Ledger interface {
	MemberGrants(context.Context, string, string) ([]string, error)
	Prepare(context.Context, agent.ExecutionContext, capability.Descriptor, CallRequest, string, Decision, time.Time) (PreparedCall, error)
	Start(context.Context, PreparedCall) error
	Succeed(context.Context, PreparedCall, any) error
	Fail(context.Context, PreparedCall, string, bool) error
}

type Invoker interface {
	Invoke(context.Context, capability.Descriptor, map[string]any, string) (any, error)
}

type Service struct {
	ledger      Ledger
	invoker     Invoker
	policy      Policy
	approvalTTL time.Duration
}

func NewService(ledger Ledger, invoker Invoker, approvalTTL time.Duration) (*Service, error) {
	if ledger == nil || invoker == nil || approvalTTL <= 0 {
		return nil, errors.New("tool runtime dependencies and approval TTL are required")
	}
	return &Service{ledger: ledger, invoker: invoker, approvalTTL: approvalTTL}, nil
}

func (s *Service) Execute(ctx context.Context, snapshot capability.Snapshot, request CallRequest) (PreparedCall, any, error) {
	execution, err := agent.RequireExecutionContext(ctx)
	if err != nil {
		return PreparedCall{}, nil, err
	}
	if execution.CapabilitySnapshotID != snapshot.ID {
		return PreparedCall{}, nil, errors.New("tool execution snapshot does not match the Run")
	}
	if err := snapshot.Validate(); err != nil {
		return PreparedCall{}, nil, fmt.Errorf("validate pinned tool snapshot: %w", err)
	}
	if !callIDPattern.MatchString(request.CallID) || request.Arguments == nil {
		return PreparedCall{}, nil, errors.New("tool call ID or arguments are invalid")
	}
	descriptor, ok := snapshot.Tool(request.OperationID, execution.ExecutionPlane)
	if !ok {
		return PreparedCall{}, nil, errors.New("tool is not visible in the pinned execution plane")
	}
	if err := validateArguments(descriptor, request.Arguments); err != nil {
		return PreparedCall{}, nil, err
	}
	grants, err := s.ledger.MemberGrants(ctx, execution.TenantID, execution.MemberID)
	if err != nil {
		return PreparedCall{}, nil, fmt.Errorf("load tool permissions: %w", err)
	}
	decision := s.policy.Decide(execution, descriptor, grants)
	digest, err := argumentsDigest(request.Arguments)
	if err != nil {
		return PreparedCall{}, nil, err
	}
	prepared, err := s.ledger.Prepare(ctx, execution, descriptor, request, digest, decision, time.Now().Add(s.approvalTTL))
	if err != nil {
		return PreparedCall{}, nil, err
	}
	if decision.Outcome == "deny" {
		prepared.State = "denied"
		prepared.PolicyReason = decision.Reason
		prepared.Result = nil
		return prepared, nil, nil
	}
	if prepared.State == "succeeded" {
		var result any
		if len(prepared.Result) == 0 || string(prepared.Result) == "null" {
			return prepared, nil, errors.New("succeeded tool call has no replay result")
		}
		if err := json.Unmarshal(prepared.Result, &result); err != nil {
			return prepared, nil, fmt.Errorf("decode replayed tool result: %w", err)
		}
		return prepared, result, nil
	}
	switch prepared.State {
	case "denied", "waiting_approval":
		return prepared, nil, nil
	case "failed":
		return prepared, nil, ErrToolCallFailed
	case "unknown":
		return prepared, nil, ErrToolCallOutcomeUnknown
	case "prepared":
	case "executing":
		return prepared, nil, errors.New("tool call is already executing")
	default:
		return prepared, nil, fmt.Errorf("tool call has unsupported state %q", prepared.State)
	}
	if err := s.ledger.Start(ctx, prepared); err != nil {
		return prepared, nil, err
	}
	invokeCtx, cancel := context.WithTimeout(ctx, descriptor.Timeout)
	defer cancel()
	result, invokeErr := s.invoker.Invoke(invokeCtx, descriptor, request.Arguments, "tool:"+execution.RunID+":"+request.CallID)
	if invokeErr != nil {
		unknown := descriptor.Risk != "read"
		if err := s.ledger.Fail(ctx, prepared, classifyInvokeError(invokeErr), unknown); err != nil {
			return prepared, nil, err
		}
		return prepared, nil, invokeErr
	}
	if err := s.ledger.Succeed(ctx, prepared, result); err != nil {
		return prepared, nil, err
	}
	prepared.State = "succeeded"
	return prepared, result, nil
}

func validateArguments(descriptor capability.Descriptor, arguments map[string]any) error {
	decoder := json.NewDecoder(bytes.NewReader(descriptor.InputSchema))
	decoder.UseNumber()
	var document any
	if err := decoder.Decode(&document); err != nil {
		return fmt.Errorf("decode pinned tool input schema: %w", err)
	}
	compiler := jsonschema.NewCompiler()
	if err := compiler.AddResource("urn:openim:tool-input-schema", document); err != nil {
		return fmt.Errorf("load pinned tool input schema: %w", err)
	}
	schema, err := compiler.Compile("urn:openim:tool-input-schema")
	if err != nil {
		return fmt.Errorf("compile pinned tool input schema: %w", err)
	}
	if err := schema.Validate(arguments); err != nil {
		return fmt.Errorf("tool arguments violate pinned input schema: %w", err)
	}
	return nil
}

func argumentsDigest(arguments map[string]any) (string, error) {
	canonical, err := json.Marshal(arguments)
	if err != nil {
		return "", fmt.Errorf("encode tool arguments: %w", err)
	}
	digest := sha256.Sum256(canonical)
	return "sha256:" + hex.EncodeToString(digest[:]), nil
}

func classifyInvokeError(err error) string {
	if errors.Is(err, context.DeadlineExceeded) {
		return "tool_timeout"
	}
	if errors.Is(err, context.Canceled) {
		return "tool_canceled"
	}
	return "tool_error"
}
