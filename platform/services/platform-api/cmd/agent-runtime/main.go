package main

import (
	"context"
	"encoding/json"
	"errors"
	"fmt"
	"log/slog"
	"os"
	"os/signal"
	"strconv"
	"strings"
	"syscall"

	"github.com/IBM/sarama"
	"github.com/jackc/pgx/v5/pgxpool"
	"github.com/qsyy0921/openim/platform/services/platform-api/internal/action"
	"github.com/qsyy0921/openim/platform/services/platform-api/internal/agent"
	"github.com/qsyy0921/openim/platform/services/platform-api/internal/capability"
	"github.com/qsyy0921/openim/platform/services/platform-api/internal/delegation"
	"github.com/qsyy0921/openim/platform/services/platform-api/internal/delivery"
	"github.com/qsyy0921/openim/platform/services/platform-api/internal/mcp"
	"github.com/qsyy0921/openim/platform/services/platform-api/internal/memory"
	"github.com/qsyy0921/openim/platform/services/platform-api/internal/openim"
	"github.com/qsyy0921/openim/platform/services/platform-api/internal/remotea2a"
	"github.com/qsyy0921/openim/platform/services/platform-api/internal/retrieval"
	"github.com/qsyy0921/openim/platform/services/platform-api/internal/telegram"
	"github.com/qsyy0921/openim/platform/services/platform-api/internal/toolruntime"
)

func main() {
	if err := run(); err != nil {
		slog.Error("agent-runtime stopped", "error", err)
		os.Exit(1)
	}
}

func run() error {
	cfg, err := agent.LoadConfig()
	if err != nil {
		return err
	}
	startup, cancel := context.WithTimeout(context.Background(), cfg.DependencyTimeout)
	defer cancel()
	pool, err := pgxpool.New(startup, cfg.DatabaseURL)
	if err != nil {
		return err
	}
	defer pool.Close()
	if err := pool.Ping(startup); err != nil {
		return err
	}
	group, err := sarama.NewConsumerGroup(cfg.Brokers, cfg.ConsumerGroup, cfg.SaramaConfig())
	if err != nil {
		return err
	}
	defer group.Close()
	store := agent.NewStore(pool)
	consumer := agent.NewConsumer(group, cfg.EventTopic, store)
	candidates := agent.NewCandidateClient(cfg.IntelligenceURL, cfg.DependencyTimeout)
	embeddingClient, err := retrieval.NewHTTPEmbeddingClient(
		cfg.RetrievalIntelligenceURL,
		cfg.DependencyTimeout,
		cfg.RetrievalModelRevision,
		cfg.RetrievalDimension,
	)
	if err != nil {
		return err
	}
	reranker, err := retrieval.NewHTTPReranker(
		cfg.RetrievalIntelligenceURL, cfg.DependencyTimeout,
		cfg.RetrievalRerankerModel, cfg.RetrievalRerankerRevision,
	)
	if err != nil {
		return err
	}
	retriever, err := retrieval.NewStore(pool, embeddingClient, reranker, retrieval.Config{
		ModelRevision:      cfg.RetrievalModelRevision,
		ProjectionRevision: cfg.RetrievalProjectionRevision,
		Dimension:          cfg.RetrievalDimension,
		DenseMinSimilarity: cfg.RetrievalDenseMinSimilarity, MaxCandidates: cfg.RetrievalMaxCandidates,
		RerankerModel: cfg.RetrievalRerankerModel, RerankerRevision: cfg.RetrievalRerankerRevision,
		HNSWEFSearch: cfg.RetrievalHNSWEFSearch,
	})
	if err != nil {
		return err
	}
	actions := action.NewStore(pool)
	mcpSupervisor, err := mcp.NewSupervisor(mcp.NewPostgresRegistry(pool), mcp.DefaultConnector, cfg.MCPReconcileInterval)
	if err != nil {
		return err
	}
	var remoteStore *remotea2a.Store
	if len(cfg.A2AAllowedHosts) > 0 {
		remoteClient, err := remotea2a.NewClient(remotea2a.Config{
			AllowedHosts: cfg.A2AAllowedHosts, AllowedPrivateCIDRs: cfg.A2AAllowedPrivateCIDRs, Timeout: cfg.A2ATimeout,
		}, os.LookupEnv)
		if err != nil {
			return err
		}
		remoteStore = remotea2a.NewStore(pool, remoteClient)
	}
	toolService, err := toolruntime.NewService(toolruntime.NewStore(pool), coreToolInvoker{
		retrieval: retriever, mcp: mcpSupervisor, delegations: delegation.NewStore(pool), remoteAgents: remoteStore,
	}, cfg.ToolApprovalTTL)
	if err != nil {
		return err
	}
	router := agent.NewRouterClient(cfg.IntelligenceURL, cfg.DependencyTimeout)
	openIMClient := openim.NewClient(openim.Config{
		BaseURL: cfg.OpenIMAPIURL, Secret: cfg.OpenIMSecret,
		AdminUser: cfg.OpenIMAdminUserID, Timeout: cfg.DependencyTimeout,
	})
	worker := agent.NewWorker(store, candidates, agent.NewToolPlannerClient(cfg.IntelligenceURL, cfg.DependencyTimeout), operationBridge{service: toolService, retrieval: retriever}, memoryBridge{
		store: memory.NewStore(pool), openIM: openIMClient, telegram: telegram.NewStore(pool),
	}, actionBridge{store: actions}, router, deliveryBridge{store: delivery.NewStore(pool)}, cfg.Poll, cfg.Lease, cfg.MaxAttempts)
	ctx, stop := signal.NotifyContext(context.Background(), os.Interrupt, syscall.SIGTERM)
	defer stop()
	errs := make(chan error, 3)
	go func() { errs <- consumer.Run(ctx) }()
	go func() { errs <- worker.Run(ctx) }()
	go func() { errs <- mcpSupervisor.Run(ctx) }()
	slog.Info("agent-runtime started", "event_topic", cfg.EventTopic, "consumer_group", cfg.ConsumerGroup)
	select {
	case <-ctx.Done():
		return nil
	case err := <-errs:
		stop()
		if err == nil || errors.Is(err, context.Canceled) {
			return nil
		}
		return err
	}
}

type actionBridge struct{ store *action.Store }

func (b actionBridge) EnsureIntent(ctx context.Context, request agent.IntentRequest) (agent.Intent, error) {
	result, err := b.store.EnsureIntent(ctx, action.IntentRequest{RunID: request.RunID, TenantID: request.TenantID, MemberID: request.MemberID, ActionType: request.ActionType, Title: request.Title})
	return agent.Intent{ID: result.ID, Digest: result.Digest}, err
}

type operationBridge struct {
	service   *toolruntime.Service
	retrieval *retrieval.Store
}

type memoryBridge struct {
	store    *memory.Store
	openIM   *openim.Client
	telegram *telegram.Store
}

type deliveryBridge struct{ store *delivery.Store }

func (b deliveryBridge) Prepare(ctx context.Context, request agent.DeliveryRequest) error {
	return b.store.Prepare(ctx, delivery.PrepareRequest{
		RunID: request.RunID, LeaseToken: request.LeaseToken, TenantID: request.TenantID,
		Channel: request.Channel, TargetID: request.TargetID, SessionType: request.SessionType,
		Content: request.Content, WaitingApproval: request.WaitingApproval,
	})
}

func (b operationBridge) SearchKnowledge(ctx context.Context, _ agent.Run, snapshot capability.Snapshot, query agent.RetrievalQuery) ([]agent.Evidence, error) {
	prepared, raw, err := b.service.Execute(ctx, snapshot, toolruntime.CallRequest{
		CallID: "knowledge-search", OperationID: "enterprise.knowledge.search",
		Arguments: map[string]any{"query": query.Text, "limit": query.Limit},
	})
	if err != nil {
		return nil, err
	}
	return decodeKnowledgeResult(prepared, raw)
}

func (b operationBridge) ValidateKnowledgeCitations(ctx context.Context, run agent.Run, answer string, evidence []agent.Evidence) ([]agent.Evidence, error) {
	if b.retrieval == nil {
		return nil, errors.New("enterprise knowledge citation validator is not configured")
	}
	items := make([]retrieval.Evidence, len(evidence))
	for index, item := range evidence {
		items[index] = retrieval.Evidence{
			CitationID: item.CitationID, DocumentID: item.DocumentID,
			VersionID: item.VersionID, ChunkID: item.ChunkID, Title: item.Title,
			SourceURI: item.SourceURI, Checksum: item.Checksum, Content: item.Content,
		}
	}
	validated, err := b.retrieval.ValidateCitations(ctx, retrieval.Query{
		TenantID: run.TenantID, MemberID: run.MemberID,
		Purpose: "agent_answer", Text: run.Prompt, Limit: len(evidence),
	}, answer, items)
	if err != nil {
		return nil, err
	}
	result := make([]agent.Evidence, len(validated))
	for index, item := range validated {
		result[index] = agent.Evidence{
			CitationID: item.CitationID, DocumentID: item.DocumentID,
			VersionID: item.VersionID, ChunkID: item.ChunkID, Title: item.Title,
			SourceURI: item.SourceURI, Checksum: item.Checksum, Content: item.Content,
		}
	}
	return result, nil
}

func decodeKnowledgeResult(prepared toolruntime.PreparedCall, raw any) ([]agent.Evidence, error) {
	if prepared.State == "denied" {
		return []agent.Evidence{}, nil
	}
	if prepared.State != "succeeded" {
		return nil, fmt.Errorf("enterprise knowledge tool did not succeed: state=%s", prepared.State)
	}
	if raw == nil {
		return nil, errors.New("enterprise knowledge tool succeeded without a result")
	}
	encoded, err := json.Marshal(raw)
	if err != nil {
		return nil, fmt.Errorf("encode enterprise knowledge tool result: %w", err)
	}
	var items []retrieval.Evidence
	if err := json.Unmarshal(encoded, &items); err != nil {
		return nil, fmt.Errorf("decode enterprise knowledge tool result: %w", err)
	}
	result := make([]agent.Evidence, len(items))
	for i, item := range items {
		result[i] = agent.Evidence{CitationID: item.CitationID, DocumentID: item.DocumentID, VersionID: item.VersionID,
			ChunkID: item.ChunkID, Title: item.Title, SourceURI: item.SourceURI, Checksum: item.Checksum, Content: item.Content}
	}
	return result, nil
}

func (b operationBridge) ExecuteTool(ctx context.Context, _ agent.Run, snapshot capability.Snapshot, call agent.ToolCallRequest) (agent.ToolExecution, error) {
	prepared, value, err := b.service.Execute(ctx, snapshot, toolruntime.CallRequest{
		CallID: call.CallID, OperationID: call.OperationID, Arguments: call.Arguments,
	})
	if err != nil {
		return agent.ToolExecution{}, err
	}
	execution := agent.ToolExecution{
		State: prepared.State, PolicyReason: prepared.PolicyReason, ApprovalID: prepared.ApprovalID,
	}
	if value != nil {
		encoded, err := json.Marshal(value)
		if err != nil {
			return agent.ToolExecution{}, err
		}
		if err := json.Unmarshal(encoded, &execution.Result); err != nil || execution.Result == nil {
			return agent.ToolExecution{}, errors.New("tool result must be a JSON object")
		}
	}
	return execution, nil
}

func (b memoryBridge) SearchPersonal(ctx context.Context, run agent.Run, query string, limit int) ([]agent.MemoryFact, error) {
	facts, err := b.store.SearchPersonal(ctx, run.TenantID, run.MemberID, query, limit)
	if err != nil {
		return nil, err
	}
	result := make([]agent.MemoryFact, len(facts))
	for index, fact := range facts {
		result[index] = agent.MemoryFact{ID: fact.ID, Category: fact.Category, Checksum: fact.Checksum, Content: fact.Content}
	}
	return result, nil
}

func (b memoryBridge) SearchGroup(ctx context.Context, run agent.Run, query string, limit int) ([]agent.MemoryFact, error) {
	if run.SessionType != 2 {
		return nil, errors.New("group memory requires a group conversation")
	}
	switch run.SourceChannel {
	case "openim":
		groupID, ok := strings.CutPrefix(run.ConversationID, "sg_")
		if !ok || groupID == "" || b.openIM == nil {
			return nil, errors.New("OpenIM group memory context is invalid")
		}
		member, err := b.openIM.IsGroupMember(ctx, groupID, run.SenderID)
		if err != nil {
			return nil, fmt.Errorf("authorize OpenIM group memory: %w", err)
		}
		if !member {
			return nil, errors.New("OpenIM group memory access is unauthorized")
		}
	case "telegram":
		chatRaw, chatOK := strings.CutPrefix(run.ConversationID, "tg_")
		userRaw, userOK := strings.CutPrefix(run.SenderID, "telegram:")
		chatID, chatErr := strconv.ParseInt(chatRaw, 10, 64)
		userID, userErr := strconv.ParseInt(userRaw, 10, 64)
		if !chatOK || !userOK || chatErr != nil || userErr != nil || b.telegram == nil {
			return nil, errors.New("Telegram group memory context is invalid")
		}
		binding, err := b.telegram.ResolveBinding(ctx, userID, chatID)
		if err != nil {
			return nil, fmt.Errorf("authorize Telegram group memory: %w", err)
		}
		if binding.TenantID != run.TenantID || binding.MemberID != run.MemberID || binding.SessionType != 2 {
			return nil, errors.New("Telegram group memory access is unauthorized")
		}
	default:
		return nil, errors.New("group memory source channel is unsupported")
	}
	facts, err := b.store.SearchGroup(ctx, memory.Scope{
		Type: "group", TenantID: run.TenantID, SourceChannel: run.SourceChannel, ConversationID: run.ConversationID,
	}, query, limit)
	if err != nil {
		return nil, err
	}
	result := make([]agent.MemoryFact, len(facts))
	for index, fact := range facts {
		result[index] = agent.MemoryFact{ID: fact.ID, Category: fact.Category, Checksum: fact.Checksum, Content: fact.Content}
	}
	return result, nil
}

func (b memoryBridge) RecordExposures(ctx context.Context, run agent.Run, facts []agent.MemoryFact, reason string) error {
	items := make([]memory.Fact, len(facts))
	for index, fact := range facts {
		items[index] = memory.Fact{ID: fact.ID, Category: fact.Category, Checksum: fact.Checksum, Content: fact.Content}
	}
	return b.store.RecordExposures(ctx, run.TenantID, run.ID, items, reason)
}

type coreToolInvoker struct {
	retrieval    *retrieval.Store
	mcp          *mcp.Supervisor
	delegations  *delegation.Store
	remoteAgents *remotea2a.Store
}

func (i coreToolInvoker) Invoke(ctx context.Context, descriptor capability.Descriptor, arguments map[string]any, idempotencyKey string) (any, error) {
	execution, err := agent.RequireExecutionContext(ctx)
	if err != nil {
		return nil, err
	}
	if descriptor.SourceType == "mcp" {
		toolName := descriptor.OperationID
		if descriptor.SourceOperation != "" {
			toolName = descriptor.SourceOperation
		}
		return i.mcp.Call(ctx, execution.TenantID, descriptor.SourceID, toolName, arguments)
	}
	if descriptor.SourceType == "core" && descriptor.OperationID == "agent.delegate" {
		targetSlug, targetOK := arguments["target_agent_slug"].(string)
		task, taskOK := arguments["task"].(string)
		if !targetOK || !taskOK || i.delegations == nil {
			return nil, errors.New("Agent delegation arguments or store are invalid")
		}
		job, err := i.delegations.Spawn(ctx, execution, targetSlug, task, idempotencyKey)
		if err != nil {
			return nil, err
		}
		return map[string]any{
			"job_id": job.ID, "child_run_id": job.ChildRunID,
			"target_agent_slug": job.TargetAgentSlug, "state": job.State,
		}, nil
	}
	if descriptor.SourceType == "core" && descriptor.OperationID == "agent.remote_delegate" {
		targetSlug, targetOK := arguments["target_agent_slug"].(string)
		task, taskOK := arguments["task"].(string)
		if !targetOK || !taskOK {
			return nil, errors.New("remote A2A delegation arguments are invalid")
		}
		if i.remoteAgents == nil {
			return nil, errors.New("remote A2A delegation is disabled by deployment policy")
		}
		job, err := i.remoteAgents.Delegate(ctx, execution, targetSlug, task, idempotencyKey)
		if err != nil {
			return nil, err
		}
		return map[string]any{
			"job_id": job.ID, "message_id": job.MessageID, "remote_task_id": job.RemoteTaskID,
			"target_agent_slug": job.RemoteAgentSlug, "state": job.State, "response": job.Response,
		}, nil
	}
	if descriptor.SourceType != "core" || descriptor.OperationID != "enterprise.knowledge.search" {
		return nil, fmt.Errorf("core tool operation %q is not implemented", descriptor.OperationID)
	}
	query, ok := arguments["query"].(string)
	if !ok || strings.TrimSpace(query) == "" {
		return nil, errors.New("enterprise knowledge query is required")
	}
	limit, ok := arguments["limit"].(int)
	if !ok || limit < 1 || limit > 8 {
		return nil, errors.New("enterprise knowledge limit is invalid")
	}
	return i.retrieval.Search(ctx, retrieval.Query{
		TenantID: execution.TenantID, MemberID: execution.MemberID,
		Purpose: "agent_answer", Text: query, Limit: limit,
	})
}
