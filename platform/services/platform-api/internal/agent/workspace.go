package agent

import (
	"context"
	"errors"
	"fmt"
	"time"

	"github.com/qsyy0921/openim/platform/services/platform-api/internal/identity"
)

type WorkspaceCitation struct {
	CitationID string `json:"citation_id"`
	Title      string `json:"title"`
	SourceURI  string `json:"source_uri"`
	Checksum   string `json:"checksum"`
}

type WorkspaceIntent struct {
	ID             string    `json:"intent_id"`
	ActionType     string    `json:"action_type"`
	Title          string    `json:"title"`
	PayloadDigest  string    `json:"payload_digest"`
	State          string    `json:"state"`
	ExpiresAt      time.Time `json:"expires_at"`
	ExecutionID    string    `json:"execution_id,omitempty"`
	ExecutionState string    `json:"execution_state,omitempty"`
	TicketID       string    `json:"ticket_id,omitempty"`
}

type WorkspaceRun struct {
	ID                 string              `json:"run_id"`
	AgentID            string              `json:"agent_id"`
	AgentDisplayName   string              `json:"agent_display_name"`
	AgentVersionNumber int                 `json:"agent_version_number"`
	AgentSpecChecksum  string              `json:"agent_spec_checksum"`
	ConversationID     string              `json:"conversation_id"`
	Prompt             string              `json:"prompt"`
	State              string              `json:"state"`
	Answer             string              `json:"answer,omitempty"`
	Model              string              `json:"model,omitempty"`
	LastError          string              `json:"last_error,omitempty"`
	CreatedAt          time.Time           `json:"created_at"`
	UpdatedAt          time.Time           `json:"updated_at"`
	Citations          []WorkspaceCitation `json:"citations"`
	Intent             *WorkspaceIntent    `json:"intent,omitempty"`
}

type Workspace struct {
	AgentUserID string         `json:"agent_user_id"`
	Runs        []WorkspaceRun `json:"runs"`
}

func (s *Store) ReadWorkspace(ctx context.Context, tenantID, memberID string, limit int) ([]WorkspaceRun, error) {
	if tenantID == "" || memberID == "" || limit < 1 || limit > 50 {
		return nil, errors.New("workspace query is invalid")
	}
	const runsQuery = `
SELECT r.id::text, r.agent_id::text, d.display_name, v.version_number, r.agent_spec_checksum,
       r.conversation_id, r.prompt, r.state,
       COALESCE(r.candidate_text, ''), COALESCE(r.model, ''), COALESCE(r.last_error, ''),
       r.created_at, r.updated_at,
       COALESCE(i.id::text, ''), COALESCE(i.action_type, ''), COALESCE(i.payload->>'title', ''),
       COALESCE(i.payload_digest, ''), COALESCE(i.state, ''), i.expires_at,
       COALESCE(e.id::text, ''), COALESCE(e.state, ''), COALESCE(e.ticket_id::text, '')
FROM agent.runs r
JOIN agent.definitions d ON d.tenant_id = r.tenant_id AND d.id = r.agent_id
JOIN agent.versions v ON v.tenant_id = r.tenant_id AND v.agent_id = r.agent_id AND v.id = r.agent_version_id
LEFT JOIN action.intents i ON i.run_id = r.id
LEFT JOIN action.executions e ON e.intent_id = i.id
WHERE r.tenant_id = $1::uuid AND r.principal_member_id = $2::uuid
ORDER BY r.created_at DESC
LIMIT $3`
	rows, err := s.pool.Query(ctx, runsQuery, tenantID, memberID, limit)
	if err != nil {
		return nil, fmt.Errorf("read Agent workspace runs: %w", err)
	}
	defer rows.Close()
	runs := make([]WorkspaceRun, 0)
	byID := make(map[string]int)
	for rows.Next() {
		var run WorkspaceRun
		var intentID, actionType, title, digest, intentState, executionID, executionState, ticketID string
		var expiresAt *time.Time
		if err := rows.Scan(
			&run.ID, &run.AgentID, &run.AgentDisplayName, &run.AgentVersionNumber, &run.AgentSpecChecksum,
			&run.ConversationID, &run.Prompt, &run.State, &run.Answer, &run.Model, &run.LastError,
			&run.CreatedAt, &run.UpdatedAt, &intentID, &actionType, &title, &digest, &intentState, &expiresAt,
			&executionID, &executionState, &ticketID,
		); err != nil {
			return nil, fmt.Errorf("scan Agent workspace run: %w", err)
		}
		run.Citations = make([]WorkspaceCitation, 0)
		if intentID != "" && expiresAt != nil {
			run.Intent = &WorkspaceIntent{
				ID: intentID, ActionType: actionType, Title: title, PayloadDigest: digest,
				State: intentState, ExpiresAt: *expiresAt, ExecutionID: executionID,
				ExecutionState: executionState, TicketID: ticketID,
			}
		}
		byID[run.ID] = len(runs)
		runs = append(runs, run)
	}
	if err := rows.Err(); err != nil {
		return nil, fmt.Errorf("iterate Agent workspace runs: %w", err)
	}
	if len(runs) == 0 {
		return runs, nil
	}
	const citationsQuery = `
WITH recent AS (
    SELECT id FROM agent.runs
    WHERE tenant_id = $1::uuid AND principal_member_id = $2::uuid
    ORDER BY created_at DESC LIMIT $3
)
SELECT c.run_id::text, c.citation_id, c.title, c.source_uri, c.checksum
FROM agent.run_citations c
JOIN recent r ON r.id = c.run_id
ORDER BY c.run_id, c.ordinal`
	citationRows, err := s.pool.Query(ctx, citationsQuery, tenantID, memberID, limit)
	if err != nil {
		return nil, fmt.Errorf("read Agent workspace citations: %w", err)
	}
	defer citationRows.Close()
	for citationRows.Next() {
		var runID string
		var citation WorkspaceCitation
		if err := citationRows.Scan(&runID, &citation.CitationID, &citation.Title, &citation.SourceURI, &citation.Checksum); err != nil {
			return nil, fmt.Errorf("scan Agent workspace citation: %w", err)
		}
		if index, ok := byID[runID]; ok {
			runs[index].Citations = append(runs[index].Citations, citation)
		}
	}
	if err := citationRows.Err(); err != nil {
		return nil, fmt.Errorf("iterate Agent workspace citations: %w", err)
	}
	return runs, nil
}

type WorkspaceVerifier interface {
	Verify(context.Context, string) (identity.Principal, error)
}

type WorkspaceMemberResolver interface {
	ResolveActiveMember(context.Context, identity.Principal, string, int32) (identity.Member, error)
}

type WorkspaceStore interface {
	EnsureBotIdentity(context.Context, string, string) error
	ReadWorkspace(context.Context, string, string, int) ([]WorkspaceRun, error)
}

type WorkspaceBotEnsurer interface {
	EnsureAgentBot(context.Context, string, string) error
}

type WorkspaceService struct {
	verifier WorkspaceVerifier
	members  WorkspaceMemberResolver
	store    WorkspaceStore
	bots     WorkspaceBotEnsurer
}

func NewWorkspaceService(verifier WorkspaceVerifier, members WorkspaceMemberResolver, store WorkspaceStore, bots WorkspaceBotEnsurer) *WorkspaceService {
	return &WorkspaceService{verifier: verifier, members: members, store: store, bots: bots}
}

func (s *WorkspaceService) Get(ctx context.Context, rawToken, deviceID string, platformID int32) (Workspace, error) {
	principal, err := s.verifier.Verify(ctx, rawToken)
	if err != nil {
		return Workspace{}, identity.ErrUnauthenticated
	}
	member, err := s.members.ResolveActiveMember(ctx, principal, deviceID, platformID)
	if err != nil {
		return Workspace{}, err
	}
	botID := BotUserID(member.TenantID)
	if err := s.store.EnsureBotIdentity(ctx, member.TenantID, botID); err != nil {
		return Workspace{}, fmt.Errorf("ensure Agent workspace bot mapping: %w", err)
	}
	if err := s.bots.EnsureAgentBot(ctx, botID, member.TenantID); err != nil {
		return Workspace{}, fmt.Errorf("ensure Agent workspace bot: %w", err)
	}
	runs, err := s.store.ReadWorkspace(ctx, member.TenantID, member.ID, 30)
	if err != nil {
		return Workspace{}, err
	}
	return Workspace{AgentUserID: botID, Runs: runs}, nil
}

var _ WorkspaceStore = (*Store)(nil)
var _ WorkspaceVerifier = (*identity.OIDCVerifier)(nil)
var _ WorkspaceMemberResolver = (*identity.PostgresStore)(nil)
