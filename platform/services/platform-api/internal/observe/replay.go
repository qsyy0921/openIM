package observe

import (
	"context"
	"crypto/sha256"
	"encoding/hex"
	"encoding/json"
	"errors"
	"fmt"
	"time"

	"github.com/jackc/pgx/v5"
	"github.com/jackc/pgx/v5/pgxpool"
)

var ErrRunNotFound = errors.New("Agent Run replay is not found")

type RunSnapshot struct {
	ID                    string    `json:"run_id"`
	SourceEventID         string    `json:"source_event_id"`
	AgentID               string    `json:"agent_id"`
	AgentVersionID        string    `json:"agent_version_id"`
	AgentSpecChecksum     string    `json:"agent_spec_checksum"`
	CapabilitySnapshotID  string    `json:"capability_snapshot_id"`
	SourceChannel         string    `json:"source_channel"`
	ConversationID        string    `json:"conversation_id"`
	ConversationSequence  int64     `json:"conversation_sequence"`
	ExecutionPlane        string    `json:"execution_plane"`
	TraceID               string    `json:"trace_id"`
	State                 string    `json:"state"`
	Prompt                string    `json:"prompt"`
	CandidateText         string    `json:"candidate_text,omitempty"`
	RouteStatus           string    `json:"route_status,omitempty"`
	RouteOperationID      string    `json:"route_operation_id,omitempty"`
	Model                 string    `json:"model,omitempty"`
	ProviderResponseID    string    `json:"provider_response_id,omitempty"`
	Attempts              int       `json:"attempts"`
	RouteAttempts         int       `json:"route_attempts"`
	ToolPlanAttempts      int       `json:"tool_plan_attempts"`
	ToolExecutionAttempts int       `json:"tool_execution_attempts"`
	ModelAttempts         int       `json:"model_attempts"`
	FinalizationAttempts  int       `json:"finalization_attempts"`
	CreatedAt             time.Time `json:"created_at"`
	UpdatedAt             time.Time `json:"updated_at"`
}

type LifecycleEvent struct {
	Sequence  int64           `json:"sequence"`
	Type      string          `json:"type"`
	Evidence  json.RawMessage `json:"evidence"`
	CreatedAt time.Time       `json:"created_at"`
}

type ToolCall struct {
	CallID            string     `json:"call_id"`
	OperationID       string     `json:"operation_id"`
	Risk              string     `json:"risk"`
	ArgumentsDigest   string     `json:"arguments_digest"`
	PolicyDecision    string     `json:"policy_decision"`
	PolicyReason      string     `json:"policy_reason"`
	State             string     `json:"state"`
	Attempts          int        `json:"attempts"`
	ErrorCode         string     `json:"error_code,omitempty"`
	ApprovalState     string     `json:"approval_state,omitempty"`
	ApprovalExpiresAt *time.Time `json:"approval_expires_at,omitempty"`
}

type Delivery struct {
	Channel           string    `json:"channel"`
	State             string    `json:"state"`
	ExternalMessageID string    `json:"external_message_id,omitempty"`
	LastError         string    `json:"last_error,omitempty"`
	SessionType       int       `json:"session_type"`
	Attempts          int       `json:"attempts"`
	WaitingApproval   bool      `json:"waiting_approval"`
	CreatedAt         time.Time `json:"created_at"`
	UpdatedAt         time.Time `json:"updated_at"`
}

type Citation struct {
	CitationID string `json:"citation_id"`
	Title      string `json:"title"`
	SourceURI  string `json:"source_uri"`
	Checksum   string `json:"checksum"`
	Ordinal    int    `json:"ordinal"`
}

type MemoryExposure struct {
	FactID          string `json:"fact_id"`
	FactChecksum    string `json:"fact_checksum"`
	RetrievalReason string `json:"retrieval_reason"`
	Ordinal         int    `json:"ordinal"`
}

type Bundle struct {
	SchemaVersion   int              `json:"schema_version"`
	Run             RunSnapshot      `json:"run"`
	Lifecycle       []LifecycleEvent `json:"lifecycle"`
	ToolCalls       []ToolCall       `json:"tool_calls"`
	Delivery        *Delivery        `json:"delivery,omitempty"`
	Citations       []Citation       `json:"citations"`
	MemoryExposures []MemoryExposure `json:"memory_exposures"`
	Checksum        string           `json:"checksum"`
}

type Store struct{ pool *pgxpool.Pool }

func NewStore(pool *pgxpool.Pool) *Store { return &Store{pool: pool} }

func (s *Store) ReadReplay(ctx context.Context, tenantID, memberID, runID string) (Bundle, error) {
	if tenantID == "" || memberID == "" || runID == "" {
		return Bundle{}, errors.New("Agent replay query is invalid")
	}
	tx, err := s.pool.BeginTx(ctx, pgx.TxOptions{IsoLevel: pgx.RepeatableRead, AccessMode: pgx.ReadOnly})
	if err != nil {
		return Bundle{}, fmt.Errorf("begin Agent replay: %w", err)
	}
	defer func() { _ = tx.Rollback(ctx) }()
	bundle := Bundle{SchemaVersion: 1, Lifecycle: make([]LifecycleEvent, 0), ToolCalls: make([]ToolCall, 0), Citations: make([]Citation, 0), MemoryExposures: make([]MemoryExposure, 0)}
	const runQuery = `
SELECT id::text, source_event_id, agent_id::text, agent_version_id::text,
       agent_spec_checksum, capability_snapshot_id, source_channel, conversation_id,
       conversation_sequence, execution_plane, trace_id, state, prompt,
       COALESCE(candidate_text, ''), COALESCE(route_status, ''),
       COALESCE(route_operation_id, ''), COALESCE(model, ''),
       COALESCE(provider_response_id, ''), attempts, route_attempts,
       tool_plan_attempts, tool_execution_attempts, model_attempts,
       finalization_attempts, created_at, updated_at
FROM agent.runs
WHERE tenant_id = $1::uuid AND principal_member_id = $2::uuid AND id = $3::uuid`
	if err := tx.QueryRow(ctx, runQuery, tenantID, memberID, runID).Scan(
		&bundle.Run.ID, &bundle.Run.SourceEventID, &bundle.Run.AgentID, &bundle.Run.AgentVersionID,
		&bundle.Run.AgentSpecChecksum, &bundle.Run.CapabilitySnapshotID, &bundle.Run.SourceChannel,
		&bundle.Run.ConversationID, &bundle.Run.ConversationSequence, &bundle.Run.ExecutionPlane,
		&bundle.Run.TraceID, &bundle.Run.State, &bundle.Run.Prompt, &bundle.Run.CandidateText,
		&bundle.Run.RouteStatus, &bundle.Run.RouteOperationID, &bundle.Run.Model,
		&bundle.Run.ProviderResponseID, &bundle.Run.Attempts, &bundle.Run.RouteAttempts,
		&bundle.Run.ToolPlanAttempts, &bundle.Run.ToolExecutionAttempts, &bundle.Run.ModelAttempts,
		&bundle.Run.FinalizationAttempts, &bundle.Run.CreatedAt, &bundle.Run.UpdatedAt,
	); err != nil {
		if errors.Is(err, pgx.ErrNoRows) {
			return Bundle{}, ErrRunNotFound
		}
		return Bundle{}, fmt.Errorf("read Agent replay Run: %w", err)
	}
	rows, err := tx.Query(ctx, `
SELECT id, event_type, evidence, created_at
FROM audit.agent_run_events
WHERE tenant_id = $1::uuid AND run_id = $2::uuid
ORDER BY id`, tenantID, runID)
	if err != nil {
		return Bundle{}, fmt.Errorf("read Agent replay lifecycle: %w", err)
	}
	for rows.Next() {
		var item LifecycleEvent
		var evidence []byte
		if err := rows.Scan(&item.Sequence, &item.Type, &evidence, &item.CreatedAt); err != nil {
			rows.Close()
			return Bundle{}, fmt.Errorf("scan Agent replay lifecycle: %w", err)
		}
		item.Evidence, err = canonicalObject(evidence)
		if err != nil {
			rows.Close()
			return Bundle{}, fmt.Errorf("canonicalize Agent replay evidence: %w", err)
		}
		bundle.Lifecycle = append(bundle.Lifecycle, item)
	}
	if err := rows.Err(); err != nil {
		rows.Close()
		return Bundle{}, fmt.Errorf("iterate Agent replay lifecycle: %w", err)
	}
	rows.Close()
	toolRows, err := tx.Query(ctx, `
SELECT call.call_id, descriptor.operation_id, descriptor.risk,
       call.arguments_digest, call.policy_decision, call.policy_reason,
       call.state, call.attempts, COALESCE(call.error_code, ''),
       COALESCE(approval.state, ''), approval.expires_at
FROM agent.tool_calls AS call
JOIN capability.tool_descriptors AS descriptor
  ON descriptor.tenant_id = call.tenant_id AND descriptor.id = call.tool_id
LEFT JOIN agent.tool_approvals AS approval
  ON approval.tenant_id = call.tenant_id AND approval.tool_call_id = call.id
WHERE call.tenant_id = $1::uuid AND call.run_id = $2::uuid
ORDER BY call.created_at, call.id`, tenantID, runID)
	if err != nil {
		return Bundle{}, fmt.Errorf("read Agent replay tools: %w", err)
	}
	for toolRows.Next() {
		var item ToolCall
		if err := toolRows.Scan(&item.CallID, &item.OperationID, &item.Risk, &item.ArgumentsDigest,
			&item.PolicyDecision, &item.PolicyReason, &item.State, &item.Attempts,
			&item.ErrorCode, &item.ApprovalState, &item.ApprovalExpiresAt); err != nil {
			toolRows.Close()
			return Bundle{}, fmt.Errorf("scan Agent replay tool: %w", err)
		}
		bundle.ToolCalls = append(bundle.ToolCalls, item)
	}
	if err := toolRows.Err(); err != nil {
		toolRows.Close()
		return Bundle{}, fmt.Errorf("iterate Agent replay tools: %w", err)
	}
	toolRows.Close()
	var delivery Delivery
	err = tx.QueryRow(ctx, `
SELECT channel, state, COALESCE(external_message_id, ''), COALESCE(last_error, ''),
       session_type, attempts, waiting_approval, created_at, updated_at
FROM agent.deliveries
WHERE tenant_id = $1::uuid AND run_id = $2::uuid`, tenantID, runID).Scan(
		&delivery.Channel, &delivery.State, &delivery.ExternalMessageID, &delivery.LastError,
		&delivery.SessionType, &delivery.Attempts, &delivery.WaitingApproval,
		&delivery.CreatedAt, &delivery.UpdatedAt,
	)
	if err == nil {
		bundle.Delivery = &delivery
	} else if !errors.Is(err, pgx.ErrNoRows) {
		return Bundle{}, fmt.Errorf("read Agent replay delivery: %w", err)
	}
	citationRows, err := tx.Query(ctx, `
SELECT citation_id, ordinal, title, source_uri, checksum
FROM agent.run_citations WHERE run_id = $1::uuid ORDER BY ordinal`, runID)
	if err != nil {
		return Bundle{}, fmt.Errorf("read Agent replay citations: %w", err)
	}
	for citationRows.Next() {
		var item Citation
		if err := citationRows.Scan(&item.CitationID, &item.Ordinal, &item.Title, &item.SourceURI, &item.Checksum); err != nil {
			citationRows.Close()
			return Bundle{}, fmt.Errorf("scan Agent replay citation: %w", err)
		}
		bundle.Citations = append(bundle.Citations, item)
	}
	if err := citationRows.Err(); err != nil {
		citationRows.Close()
		return Bundle{}, fmt.Errorf("iterate Agent replay citations: %w", err)
	}
	citationRows.Close()
	memoryRows, err := tx.Query(ctx, `
SELECT fact_id::text, fact_checksum, retrieval_reason, ordinal
FROM memory.exposures
WHERE tenant_id = $1::uuid AND run_id = $2::uuid
ORDER BY ordinal`, tenantID, runID)
	if err != nil {
		return Bundle{}, fmt.Errorf("read Agent replay memory exposures: %w", err)
	}
	for memoryRows.Next() {
		var item MemoryExposure
		if err := memoryRows.Scan(&item.FactID, &item.FactChecksum, &item.RetrievalReason, &item.Ordinal); err != nil {
			memoryRows.Close()
			return Bundle{}, fmt.Errorf("scan Agent replay memory exposure: %w", err)
		}
		bundle.MemoryExposures = append(bundle.MemoryExposures, item)
	}
	if err := memoryRows.Err(); err != nil {
		memoryRows.Close()
		return Bundle{}, fmt.Errorf("iterate Agent replay memory exposures: %w", err)
	}
	memoryRows.Close()
	if err := tx.Commit(ctx); err != nil {
		return Bundle{}, fmt.Errorf("commit Agent replay read: %w", err)
	}
	bundle.Checksum, err = replayChecksum(bundle)
	if err != nil {
		return Bundle{}, err
	}
	return bundle, nil
}

func VerifyChecksum(bundle Bundle) (bool, error) {
	if bundle.Checksum == "" {
		return false, nil
	}
	expected := bundle.Checksum
	actual, err := replayChecksum(bundle)
	if err != nil {
		return false, err
	}
	return expected == actual, nil
}

func replayChecksum(bundle Bundle) (string, error) {
	bundle.Checksum = ""
	encoded, err := json.Marshal(bundle)
	if err != nil {
		return "", fmt.Errorf("encode Agent replay bundle: %w", err)
	}
	digest := sha256.Sum256(encoded)
	return "sha256:" + hex.EncodeToString(digest[:]), nil
}

func canonicalObject(raw []byte) (json.RawMessage, error) {
	var value map[string]any
	if err := json.Unmarshal(raw, &value); err != nil {
		return nil, err
	}
	return json.Marshal(value)
}
