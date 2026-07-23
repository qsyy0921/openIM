package agent

import (
	"context"
	"crypto/sha256"
	"encoding/base32"
	"encoding/json"
	"errors"
	"fmt"
	"log/slog"
	"regexp"
	"strings"
	"time"

	"github.com/qsyy0921/openim/platform/services/platform-api/internal/capability"
)

type CandidateGenerator interface {
	Generate(ctx context.Context, run Run, version CatalogVersion, evidence []Evidence, memories []MemoryFact, toolResults []ToolResultContext) (Candidate, error)
}

type ToolPlanner interface {
	Plan(context.Context, Run, CatalogVersion, capability.Descriptor) (ToolPlan, error)
}

type OperationExecutor interface {
	SearchKnowledge(ctx context.Context, run Run, snapshot capability.Snapshot, query RetrievalQuery) ([]Evidence, error)
	ValidateKnowledgeCitations(ctx context.Context, run Run, answer string, evidence []Evidence) ([]Evidence, error)
	ExecuteTool(ctx context.Context, run Run, snapshot capability.Snapshot, call ToolCallRequest) (ToolExecution, error)
}

type ToolCallRequest struct {
	CallID, OperationID string
	Arguments           map[string]any
}

type ToolExecution struct {
	State, PolicyReason, ApprovalID string
	Result                          map[string]any
}

type MemoryContext interface {
	SearchPersonal(ctx context.Context, run Run, query string, limit int) ([]MemoryFact, error)
	SearchGroup(ctx context.Context, run Run, query string, limit int) ([]MemoryFact, error)
	RecordExposures(ctx context.Context, run Run, facts []MemoryFact, reason string) error
}

type IntentManager interface {
	EnsureIntent(ctx context.Context, request IntentRequest) (Intent, error)
}

type IntentRouter interface {
	Route(context.Context, Run, capability.Snapshot) (RouteResult, error)
}

type IntentRequest struct{ RunID, TenantID, MemberID, ActionType, Title string }
type Intent struct{ ID, Digest string }

type RetrievalQuery struct {
	TenantID string
	MemberID string
	Purpose  string
	Text     string
	Limit    int
}

type DeliveryManager interface {
	Prepare(ctx context.Context, request DeliveryRequest) error
}

type DeliveryRequest struct {
	RunID, LeaseToken, TenantID, Channel, TargetID, Content string
	SessionType                                             int32
	WaitingApproval                                         bool
}

type RuntimeStore interface {
	Claim(context.Context, time.Duration, int) (*Run, error)
	LoadCatalogVersion(context.Context, Run) (CatalogVersion, error)
	LoadCapabilitySnapshot(context.Context, Run) (capability.Snapshot, error)
	SaveRoute(context.Context, Run, RouteResult) error
	SaveToolPlan(context.Context, Run, ToolPlan) error
	SaveToolResult(context.Context, Run, any) error
	WaitForToolApproval(context.Context, Run, string) error
	SaveCandidate(context.Context, Run, Candidate, []Evidence) error
	FailOrRetry(context.Context, Run, string, int, time.Duration) error
	Fail(context.Context, Run, string) error
}

type Worker struct {
	store       RuntimeStore
	candidates  CandidateGenerator
	operations  OperationExecutor
	planner     ToolPlanner
	memory      MemoryContext
	intents     IntentManager
	router      IntentRouter
	deliveries  DeliveryManager
	poll        time.Duration
	lease       time.Duration
	maxAttempts int
}

func NewWorker(store RuntimeStore, candidates CandidateGenerator, planner ToolPlanner, operations OperationExecutor, memoryContext MemoryContext, intents IntentManager, router IntentRouter, deliveries DeliveryManager, poll, lease time.Duration, maxAttempts int) *Worker {
	return &Worker{store: store, candidates: candidates, planner: planner, operations: operations, memory: memoryContext, intents: intents, router: router, deliveries: deliveries, poll: poll, lease: lease, maxAttempts: maxAttempts}
}

func (w *Worker) Run(ctx context.Context) error {
	ticker := time.NewTicker(w.poll)
	defer ticker.Stop()
	for {
		if err := w.runOnce(ctx); err != nil {
			return err
		}
		select {
		case <-ctx.Done():
			return nil
		case <-ticker.C:
		}
	}
}

func (w *Worker) runOnce(ctx context.Context) error {
	run, err := w.store.Claim(ctx, w.lease, w.maxAttempts)
	if err != nil || run == nil {
		return err
	}
	executionCtx, err := BindExecutionContext(ctx, run.ExecutionContext())
	if err != nil {
		if failErr := w.store.Fail(ctx, *run, err.Error()); failErr != nil {
			return failErr
		}
		return nil
	}
	version, err := w.store.LoadCatalogVersion(executionCtx, *run)
	if err != nil {
		if errors.Is(err, ErrInvalidCatalog) {
			if failErr := w.store.Fail(ctx, *run, err.Error()); failErr != nil {
				return failErr
			}
			slog.Warn("Agent Run rejected by pinned catalog version", "run_id", run.ID, "agent_id", run.AgentID, "agent_version_id", run.AgentVersionID, "error", err)
			return nil
		}
		return err
	}
	snapshot, err := w.store.LoadCapabilitySnapshot(executionCtx, *run)
	if err != nil {
		if failErr := w.store.Fail(executionCtx, *run, err.Error()); failErr != nil {
			return failErr
		}
		return nil
	}
	if run.RouteStatus == "" {
		route, err := w.router.Route(executionCtx, *run, snapshot)
		if err != nil {
			return w.retry(executionCtx, *run, fmt.Errorf("route Agent intent: %w", err), w.maxAttempts)
		}
		if err := route.Validate(snapshot, run.ExecutionPlane); err != nil {
			return w.retry(executionCtx, *run, fmt.Errorf("validate Agent intent route: %w", err), w.maxAttempts)
		}
		if err := w.store.SaveRoute(executionCtx, *run, route); err != nil {
			return err
		}
		slog.Info("Agent intent route persisted", "run_id", run.ID, "status", route.Status, "operation_id", route.OperationID)
		return nil
	}
	if run.CandidateText == "" {
		if run.RouteStatus == "clarify" {
			candidate := Candidate{Text: run.RouteClarification, Model: "runtime-policy", ProviderResponseID: "route-clarification:" + run.ID, GroundingStatus: GroundingNotApplicable}
			return w.store.SaveCandidate(executionCtx, *run, candidate, nil)
		}
		if run.RouteStatus == "no_tool" {
			memories, err := w.loadMemory(executionCtx, *run)
			if err != nil {
				return w.retry(executionCtx, *run, fmt.Errorf("retrieve personal memory: %w", err), version.Spec.MaxModelAttempts)
			}
			candidate, err := w.candidates.Generate(executionCtx, *run, version, nil, memories, nil)
			if err != nil {
				return w.retry(executionCtx, *run, err, version.Spec.MaxModelAttempts)
			}
			if len(candidate.CitationIDs) != 0 {
				return w.retry(executionCtx, *run, errors.New("general candidate cited evidence that was not provided"), version.Spec.MaxModelAttempts)
			}
			if candidate.GroundingStatus != GroundingNotApplicable {
				return w.retry(executionCtx, *run, errors.New("general candidate has an invalid grounding status"), version.Spec.MaxModelAttempts)
			}
			if err := validateActionCandidate(candidate.ActionIntent, version.Spec); err != nil {
				return w.retry(executionCtx, *run, err, version.Spec.MaxModelAttempts)
			}
			if err := w.memory.RecordExposures(executionCtx, *run, memories, "general_response"); err != nil {
				return w.retry(executionCtx, *run, fmt.Errorf("record personal memory exposure: %w", err), version.Spec.MaxModelAttempts)
			}
			return w.store.SaveCandidate(executionCtx, *run, candidate, nil)
		}
		if run.RouteOperationID == "collaboration.ticket.create" {
			title, err := extractTicketTitle(run.Prompt)
			if err != nil {
				candidate := Candidate{Text: err.Error(), Model: "runtime-policy", ProviderResponseID: "ticket-clarification:" + run.ID, GroundingStatus: GroundingNotApplicable}
				return w.store.SaveCandidate(executionCtx, *run, candidate, nil)
			}
			candidate := Candidate{
				Text:  "已生成待审批的工单动作，审批通过后才会写入协作系统。",
				Model: "runtime-policy", ProviderResponseID: "ticket-intent:" + run.ID,
				GroundingStatus: GroundingNotApplicable,
				ActionIntent:    &ActionIntentCandidate{Type: "create_ticket", Title: title},
			}
			if err := validateActionCandidate(candidate.ActionIntent, version.Spec); err != nil {
				return w.retry(executionCtx, *run, err, version.Spec.MaxModelAttempts)
			}
			return w.store.SaveCandidate(executionCtx, *run, candidate, nil)
		}
		if run.RouteOperationID != "enterprise.knowledge.search" {
			descriptor, visible := snapshot.Tool(run.RouteOperationID, run.ExecutionPlane)
			if !visible {
				return w.store.Fail(executionCtx, *run, "selected tool is not visible in the pinned execution plane")
			}
			if len(run.ToolArguments) == 0 {
				plan, err := w.planner.Plan(executionCtx, *run, version, descriptor)
				if err != nil {
					return w.retry(executionCtx, *run, fmt.Errorf("plan selected tool: %w", err), w.maxAttempts)
				}
				return w.store.SaveToolPlan(executionCtx, *run, plan)
			}
			if len(run.ToolResult) == 0 {
				var arguments map[string]any
				if err := json.Unmarshal(run.ToolArguments, &arguments); err != nil {
					return w.store.Fail(executionCtx, *run, "persisted tool arguments are invalid")
				}
				execution, err := w.operations.ExecuteTool(executionCtx, *run, snapshot, ToolCallRequest{
					CallID: "selected-operation", OperationID: run.RouteOperationID, Arguments: arguments,
				})
				if err != nil {
					return w.retry(executionCtx, *run, fmt.Errorf("execute selected tool: %w", err), w.maxAttempts)
				}
				if execution.State == "denied" {
					candidate := Candidate{
						Text:  "工具调用被策略拒绝：" + execution.PolicyReason,
						Model: "runtime-policy", ProviderResponseID: "tool-denied:" + run.ID,
						GroundingStatus: GroundingNotApplicable,
					}
					return w.store.SaveCandidate(executionCtx, *run, candidate, nil)
				}
				if execution.State == "waiting_approval" {
					if execution.ApprovalID == "" {
						return w.store.Fail(executionCtx, *run, "tool approval ID is missing")
					}
					return w.store.WaitForToolApproval(executionCtx, *run, execution.ApprovalID)
				}
				if execution.State != "succeeded" || execution.Result == nil {
					return w.store.Fail(executionCtx, *run, "selected read tool did not reach a terminal success state")
				}
				return w.store.SaveToolResult(executionCtx, *run, execution.Result)
			}
			var result map[string]any
			if err := json.Unmarshal(run.ToolResult, &result); err != nil || result == nil {
				return w.store.Fail(executionCtx, *run, "persisted tool result is invalid")
			}
			memories, err := w.loadMemory(executionCtx, *run)
			if err != nil {
				return w.retry(executionCtx, *run, fmt.Errorf("retrieve personal memory: %w", err), version.Spec.MaxModelAttempts)
			}
			candidate, err := w.candidates.Generate(executionCtx, *run, version, nil, memories, []ToolResultContext{{
				OperationID: run.RouteOperationID, Result: result,
			}})
			if err != nil {
				return w.retry(executionCtx, *run, err, version.Spec.MaxModelAttempts)
			}
			if len(candidate.CitationIDs) != 0 {
				return w.retry(executionCtx, *run, errors.New("tool result candidate cited enterprise evidence that was not provided"), version.Spec.MaxModelAttempts)
			}
			if candidate.GroundingStatus != GroundingNotApplicable {
				return w.retry(executionCtx, *run, errors.New("tool result candidate has an invalid grounding status"), version.Spec.MaxModelAttempts)
			}
			if err := w.memory.RecordExposures(executionCtx, *run, memories, "tool_response"); err != nil {
				return w.retry(executionCtx, *run, fmt.Errorf("record personal memory exposure: %w", err), version.Spec.MaxModelAttempts)
			}
			return w.store.SaveCandidate(executionCtx, *run, candidate, nil)
		}
		evidence, err := w.operations.SearchKnowledge(executionCtx, *run, snapshot, RetrievalQuery{
			TenantID: run.TenantID, MemberID: run.MemberID, Purpose: version.Spec.Retrieval.Purpose,
			Text: run.Prompt, Limit: version.Spec.Retrieval.Limit,
		})
		if err != nil {
			return w.retry(ctx, *run, fmt.Errorf("retrieve authorized knowledge: %w", err), version.Spec.MaxModelAttempts)
		}
		var candidate Candidate
		var cited []Evidence
		if len(evidence) == 0 {
			candidate = Candidate{Text: "在你当前有权访问的知识中未找到可引用的证据。", Model: "runtime-policy", ProviderResponseID: "no-evidence:" + run.ID, GroundingStatus: GroundingInsufficientEvidence}
		} else {
			candidate, err = w.candidates.Generate(executionCtx, *run, version, evidence, nil, nil)
			if err != nil {
				return w.retry(ctx, *run, err, version.Spec.MaxModelAttempts)
			}
			cited, err = validateKnowledgeCandidate(candidate, evidence)
			if err != nil {
				return w.retry(ctx, *run, err, version.Spec.MaxModelAttempts)
			}
			if len(cited) > 0 {
				cited, err = w.operations.ValidateKnowledgeCitations(executionCtx, *run, candidate.Text, cited)
				if err != nil {
					return w.retry(ctx, *run, fmt.Errorf("validate knowledge citations: %w", err), version.Spec.MaxModelAttempts)
				}
			}
			if err := validateActionCandidate(candidate.ActionIntent, version.Spec); err != nil {
				return w.retry(ctx, *run, err, version.Spec.MaxModelAttempts)
			}
		}
		if err := w.store.SaveCandidate(executionCtx, *run, candidate, cited); err != nil {
			return err
		}
		slog.Info("Agent candidate persisted", "run_id", run.ID, "agent_id", run.AgentID,
			"agent_version_id", run.AgentVersionID, "model", candidate.Model)
		return nil
	}

	target, err := deliveryTarget(*run)
	if err != nil {
		return w.retry(ctx, *run, err, w.maxAttempts)
	}
	reply := run.CandidateText
	waitingApproval := run.ActionType != ""
	if waitingApproval {
		intent, err := w.intents.EnsureIntent(executionCtx, IntentRequest{RunID: run.ID, TenantID: run.TenantID, MemberID: run.MemberID, ActionType: run.ActionType, Title: run.ActionTitle})
		if err != nil {
			return w.retry(ctx, *run, fmt.Errorf("materialize action intent: %w", err), w.maxAttempts)
		}
		reply += fmt.Sprintf("\n\n待审批动作：%s\n审批编号：%s\n摘要：%s", run.ActionType, intent.ID, intent.Digest)
	}
	if err := w.deliveries.Prepare(executionCtx, DeliveryRequest{
		RunID: run.ID, LeaseToken: run.LeaseToken, TenantID: run.TenantID, Channel: run.SourceChannel,
		TargetID: target, SessionType: run.SessionType, Content: reply, WaitingApproval: waitingApproval,
	}); err != nil {
		return err
	}
	slog.Info("Agent delivery prepared", "run_id", run.ID, "channel", run.SourceChannel, "waiting_approval", waitingApproval)
	return nil
}

func (w *Worker) loadMemory(ctx context.Context, run Run) ([]MemoryFact, error) {
	if w.memory == nil {
		return nil, errors.New("personal memory context is not configured")
	}
	personalLimit := 5
	if run.SessionType == 2 {
		personalLimit = 4
	}
	personal, err := w.memory.SearchPersonal(ctx, run, run.Prompt, personalLimit)
	if err != nil {
		return nil, err
	}
	if run.SessionType != 2 {
		return personal, nil
	}
	group, err := w.memory.SearchGroup(ctx, run, run.Prompt, 4)
	if err != nil {
		return nil, err
	}
	return append(personal, group...), nil
}

func extractTicketTitle(content string) (string, error) {
	trimmed := strings.TrimSpace(content)
	for _, prefix := range []string{"创建工单：", "创建工单:"} {
		if strings.HasPrefix(trimmed, prefix) {
			title := strings.TrimSpace(strings.TrimPrefix(trimmed, prefix))
			if title == "" || len([]byte(title)) > 200 {
				return "", errors.New("请使用“创建工单：<1 到 200 字节标题>”补充有效标题。")
			}
			return title, nil
		}
	}
	return "", errors.New("创建工单需要明确命令，请使用“创建工单：<标题>”。")
}

func validateActionCandidate(intent *ActionIntentCandidate, spec AgentSpec) error {
	if intent == nil {
		return nil
	}
	intent.Title = strings.TrimSpace(intent.Title)
	if intent.Type != "create_ticket" {
		return fmt.Errorf("unsupported action intent %q", intent.Type)
	}
	if !spec.AllowsAction(intent.Type) {
		return fmt.Errorf("Agent version does not allow action intent %q", intent.Type)
	}
	if len(intent.Title) < 1 || len(intent.Title) > 200 {
		return errors.New("ticket title must be between 1 and 200 bytes")
	}
	return nil
}

func validateCitations(candidate Candidate, evidence []Evidence) ([]Evidence, error) {
	if len(candidate.CitationIDs) == 0 {
		return nil, errors.New("candidate contains no citations")
	}
	allowed := make(map[string]Evidence, len(evidence))
	for _, item := range evidence {
		allowed[item.CitationID] = item
	}
	seen := make(map[string]struct{})
	result := make([]Evidence, 0, len(candidate.CitationIDs))
	for _, id := range candidate.CitationIDs {
		item, ok := allowed[id]
		if !ok {
			return nil, fmt.Errorf("candidate cites unauthorized evidence %q", id)
		}
		if !strings.Contains(candidate.Text, "["+id+"]") {
			return nil, fmt.Errorf("candidate citation %q is not present in answer text", id)
		}
		if _, duplicate := seen[id]; duplicate {
			continue
		}
		seen[id] = struct{}{}
		result = append(result, item)
	}
	for _, id := range citationPattern.FindAllString(candidate.Text, -1) {
		id = strings.TrimSuffix(strings.TrimPrefix(id, "["), "]")
		if _, ok := seen[id]; !ok {
			return nil, fmt.Errorf("candidate text contains undeclared citation %q", id)
		}
	}
	return result, nil
}

var citationPattern = regexp.MustCompile(`\[C[0-9]+\]`)

func validateKnowledgeCandidate(candidate Candidate, evidence []Evidence) ([]Evidence, error) {
	switch candidate.GroundingStatus {
	case GroundingGrounded:
		return validateCitations(candidate, evidence)
	case GroundingInsufficientEvidence:
		if len(candidate.CitationIDs) == 0 {
			if citationPattern.MatchString(candidate.Text) {
				return nil, errors.New("insufficient-evidence candidate contains undeclared citations")
			}
			return nil, nil
		}
		return validateCitations(candidate, evidence)
	default:
		return nil, fmt.Errorf("knowledge candidate has invalid grounding status %q", candidate.GroundingStatus)
	}
}

func (w *Worker) retry(ctx context.Context, run Run, failure error, maxAttempts int) error {
	delay := time.Second << min(run.Attempts-1, 5)
	if err := w.store.FailOrRetry(ctx, run, failure.Error(), maxAttempts, delay); err != nil {
		return err
	}
	slog.Warn("Agent Run attempt failed", "run_id", run.ID, "attempt", run.Attempts, "error", failure)
	return nil
}

func deliveryTarget(run Run) (string, error) {
	switch run.SourceChannel {
	case "openim":
		if run.SessionType == 1 && run.SenderID != "" {
			return run.SenderID, nil
		}
		if run.SessionType == 2 && strings.HasPrefix(run.ConversationID, "sg_") && len(run.ConversationID) > 3 {
			return strings.TrimPrefix(run.ConversationID, "sg_"), nil
		}
	case "telegram":
		if (run.SessionType == 1 || run.SessionType == 2) && strings.HasPrefix(run.ConversationID, "tg_") && len(run.ConversationID) > 3 {
			return strings.TrimPrefix(run.ConversationID, "tg_"), nil
		}
	}
	return "", errors.New("channel delivery target is invalid")
}

func BotUserID(tenantID string) string {
	digest := sha256.Sum256([]byte("agent-bot\x00" + tenantID))
	encoded := base32.StdEncoding.WithPadding(base32.NoPadding).EncodeToString(digest[:20])
	return "agent_" + strings.ToLower(encoded)
}
