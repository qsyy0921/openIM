package memory

import (
	"context"
	"errors"
	"fmt"
	"time"

	"github.com/jackc/pgx/v5"
)

var ErrGroupProposalNotFound = errors.New("group memory proposal is not found")

type GroupProposal struct {
	ID, SourceChannel, ConversationID, SourceRunID, ProposedByMemberID string
	FactKey, Category, Content, Checksum, State, ReviewedByMemberID    string
	Confidence                                                         float64
	CreatedAt                                                          time.Time
	ReviewedAt                                                         *time.Time
}

func (s *Store) ListGroup(ctx context.Context, scope Scope, limit int) ([]Fact, error) {
	if err := scope.Validate(); err != nil {
		return nil, err
	}
	if scope.Type != "group" || limit < 1 || limit > 100 {
		return nil, errors.New("group memory list input is invalid")
	}
	rows, err := s.pool.Query(ctx, `
SELECT fact.id::text, fact.stream_id::text, fact.fact_key, fact.category,
       fact.content, fact.checksum, fact.updated_at
FROM memory.streams AS stream
JOIN memory.facts AS fact ON fact.stream_id = stream.id AND fact.state = 'active'
WHERE stream.tenant_id = $1::uuid AND stream.scope_type = 'group'
  AND stream.source_channel = $2 AND stream.conversation_id = $3
ORDER BY fact.updated_at DESC, fact.id
LIMIT $4`, scope.TenantID, scope.SourceChannel, scope.ConversationID, limit)
	if err != nil {
		return nil, fmt.Errorf("list group memory: %w", err)
	}
	defer rows.Close()
	result := make([]Fact, 0)
	for rows.Next() {
		var fact Fact
		if err := rows.Scan(&fact.ID, &fact.StreamID, &fact.FactKey, &fact.Category,
			&fact.Content, &fact.Checksum, &fact.UpdatedAt); err != nil {
			return nil, fmt.Errorf("scan group memory: %w", err)
		}
		result = append(result, fact)
	}
	return result, rows.Err()
}

func (s *Store) ListGroupProposals(ctx context.Context, scope Scope, limit int) ([]GroupProposal, error) {
	if err := scope.Validate(); err != nil {
		return nil, err
	}
	if scope.Type != "group" || limit < 1 || limit > 100 {
		return nil, errors.New("group memory proposal list input is invalid")
	}
	rows, err := s.pool.Query(ctx, `
SELECT id::text, source_channel, conversation_id, source_run_id::text,
       proposed_by_member_id::text, fact_key, category, content, checksum,
       confidence, state, COALESCE(reviewed_by_member_id::text, ''), reviewed_at, created_at
FROM memory.group_fact_proposals
WHERE tenant_id = $1::uuid AND source_channel = $2 AND conversation_id = $3
ORDER BY CASE state WHEN 'pending' THEN 0 ELSE 1 END, created_at DESC, id
LIMIT $4`, scope.TenantID, scope.SourceChannel, scope.ConversationID, limit)
	if err != nil {
		return nil, fmt.Errorf("list group memory proposals: %w", err)
	}
	defer rows.Close()
	result := make([]GroupProposal, 0)
	for rows.Next() {
		var item GroupProposal
		if err := rows.Scan(&item.ID, &item.SourceChannel, &item.ConversationID, &item.SourceRunID,
			&item.ProposedByMemberID, &item.FactKey, &item.Category, &item.Content, &item.Checksum,
			&item.Confidence, &item.State, &item.ReviewedByMemberID, &item.ReviewedAt, &item.CreatedAt); err != nil {
			return nil, fmt.Errorf("scan group memory proposal: %w", err)
		}
		result = append(result, item)
	}
	return result, rows.Err()
}

func (s *Store) ReviewGroupProposal(ctx context.Context, scope Scope, proposalID, reviewerMemberID, decision string) (string, error) {
	if err := scope.Validate(); err != nil {
		return "", err
	}
	if scope.Type != "group" || proposalID == "" || reviewerMemberID == "" || (decision != "approve" && decision != "reject") {
		return "", errors.New("group memory review input is invalid")
	}
	var factKey, category, content, checksum, sourceRunID, state string
	err := s.pool.QueryRow(ctx, `
SELECT fact_key, category, content, checksum, source_run_id::text, state
FROM memory.group_fact_proposals
WHERE tenant_id = $1::uuid AND source_channel = $2 AND conversation_id = $3 AND id = $4::uuid`,
		scope.TenantID, scope.SourceChannel, scope.ConversationID, proposalID).Scan(
		&factKey, &category, &content, &checksum, &sourceRunID, &state)
	if errors.Is(err, pgx.ErrNoRows) {
		return "", ErrGroupProposalNotFound
	}
	if err != nil {
		return "", fmt.Errorf("read group memory proposal: %w", err)
	}
	requestedState := "rejected"
	if decision == "approve" {
		requestedState = "approved"
	}
	if state != "pending" {
		if state != requestedState {
			return "", errors.New("group memory proposal was already reviewed with a different decision")
		}
		var eventID string
		_ = s.pool.QueryRow(ctx, `SELECT COALESCE(memory_event_id::text, '') FROM audit.group_memory_review_events WHERE tenant_id = $1::uuid AND proposal_id = $2::uuid`, scope.TenantID, proposalID).Scan(&eventID)
		return eventID, nil
	}
	var eventID string
	if decision == "approve" {
		payload, err := NewFactPayload(category, content)
		if err != nil || payload.Checksum != checksum {
			return "", errors.New("group memory proposal payload is invalid")
		}
		eventID, err = s.AppendUpsert(ctx, scope, factKey, sourceRunID, "group-memory-proposal:"+proposalID, payload)
		if err != nil {
			return "", err
		}
	}
	tx, err := s.pool.Begin(ctx)
	if err != nil {
		return "", err
	}
	defer func() { _ = tx.Rollback(ctx) }()
	result, err := tx.Exec(ctx, `
UPDATE memory.group_fact_proposals
SET state = $5, reviewed_by_member_id = $6::uuid, reviewed_at = now(), updated_at = now()
WHERE tenant_id = $1::uuid AND source_channel = $2 AND conversation_id = $3
  AND id = $4::uuid AND state = 'pending'`, scope.TenantID, scope.SourceChannel,
		scope.ConversationID, proposalID, requestedState, reviewerMemberID)
	if err != nil {
		return "", fmt.Errorf("review group memory proposal: %w", err)
	}
	if result.RowsAffected() != 1 {
		return "", errors.New("group memory proposal review lost a concurrent race")
	}
	if _, err := tx.Exec(ctx, `
INSERT INTO audit.group_memory_review_events
    (tenant_id, proposal_id, actor_member_id, decision, memory_event_id)
VALUES ($1::uuid, $2::uuid, $3::uuid, $4, NULLIF($5, '')::uuid)`,
		scope.TenantID, proposalID, reviewerMemberID, decision, eventID); err != nil {
		return "", fmt.Errorf("audit group memory review: %w", err)
	}
	if err := tx.Commit(ctx); err != nil {
		return "", err
	}
	return eventID, nil
}
