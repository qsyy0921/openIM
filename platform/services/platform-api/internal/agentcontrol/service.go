package agentcontrol

import (
	"context"
	"errors"
	"fmt"
	"strings"
	"time"

	"github.com/qsyy0921/openim/platform/services/platform-api/internal/delegation"
	"github.com/qsyy0921/openim/platform/services/platform-api/internal/identity"
	"github.com/qsyy0921/openim/platform/services/platform-api/internal/memory"
	"github.com/qsyy0921/openim/platform/services/platform-api/internal/observe"
	"github.com/qsyy0921/openim/platform/services/platform-api/internal/proactive"
	"github.com/qsyy0921/openim/platform/services/platform-api/internal/toolruntime"
)

type Verifier interface {
	Verify(context.Context, string) (identity.Principal, error)
}

type MemberResolver interface {
	ResolveActiveMember(context.Context, identity.Principal, string, int32) (identity.Member, error)
	ReadyOpenIMUserID(context.Context, string) (string, error)
	ReadyTelegramUserID(context.Context, string, string) (string, error)
}

type MemoryStore interface {
	ListPersonal(context.Context, string, string, int) ([]memory.Fact, error)
	ListPersonalExposures(context.Context, string, string, int) ([]memory.Exposure, error)
	DeletePersonalFact(context.Context, string, string, string, string) (string, error)
	RecordFeedback(context.Context, string, string, string, string) error
}

type GroupMemoryStore interface {
	ListGroup(context.Context, memory.Scope, int) ([]memory.Fact, error)
	ListGroupProposals(context.Context, memory.Scope, int) ([]memory.GroupProposal, error)
	ReviewGroupProposal(context.Context, memory.Scope, string, string, string) (string, error)
}

type GroupAccess interface {
	IsGroupMember(context.Context, string, string) (bool, error)
}

var (
	ErrGroupMemoryForbidden = errors.New("group memory access is forbidden")
	ErrGroupMemoryInvalid   = errors.New("group memory request is invalid")
)

type ProactiveStore interface {
	ReadMemberSnapshot(context.Context, string, string, int) (proactive.MemberSnapshot, error)
	CreateSubscription(context.Context, proactive.Subscription) (string, error)
	SetSubscriptionEnabled(context.Context, string, string, string, bool) error
	UpdatePreferences(context.Context, string, string, proactive.Preference) error
	Acknowledge(context.Context, string, string, string, string) error
}

type ToolApprovalStore interface {
	ListPendingApprovals(context.Context, string, string, int) ([]toolruntime.Approval, error)
	Approve(context.Context, string, string, string, string) error
	Reject(context.Context, string, string, string, string) error
}

type ReplayStore interface {
	ReadReplay(context.Context, string, string, string) (observe.Bundle, error)
}

type DelegationStore interface {
	ListMember(context.Context, string, string, int) ([]delegation.Job, error)
}

type Service struct {
	verifier    Verifier
	members     MemberResolver
	memory      MemoryStore
	proactive   ProactiveStore
	tools       ToolApprovalStore
	replays     ReplayStore
	delegations DelegationStore
	groupMemory GroupMemoryStore
	groupAccess GroupAccess
}

func NewService(verifier Verifier, members MemberResolver, memories MemoryStore, proactiveStore ProactiveStore, tools ToolApprovalStore, replays ReplayStore, delegations DelegationStore) *Service {
	if verifier == nil || members == nil || memories == nil || proactiveStore == nil || tools == nil || replays == nil || delegations == nil {
		panic("Agent control verifier, members, memory, proactive, tool approval, replay, and delegation stores are required")
	}
	groupMemory, _ := memories.(GroupMemoryStore)
	return &Service{verifier: verifier, members: members, memory: memories, proactive: proactiveStore, tools: tools, replays: replays, delegations: delegations, groupMemory: groupMemory}
}

func (s *Service) SetGroupAccess(access GroupAccess) {
	if access == nil {
		panic("group access is required")
	}
	s.groupAccess = access
}

type MemoryFact struct {
	ID        string    `json:"fact_id"`
	Category  string    `json:"category"`
	Content   string    `json:"content"`
	Checksum  string    `json:"checksum"`
	UpdatedAt time.Time `json:"updated_at"`
}

type MemoryExposure struct {
	ID              string    `json:"exposure_id"`
	RunID           string    `json:"run_id"`
	FactID          string    `json:"fact_id"`
	Category        string    `json:"category"`
	Content         string    `json:"content"`
	RetrievalReason string    `json:"retrieval_reason"`
	Feedback        string    `json:"feedback,omitempty"`
	CreatedAt       time.Time `json:"created_at"`
}

type MemorySnapshot struct {
	Facts     []MemoryFact     `json:"facts"`
	Exposures []MemoryExposure `json:"exposures"`
}

type GroupMemoryProposal struct {
	ID                 string     `json:"proposal_id"`
	SourceRunID        string     `json:"source_run_id"`
	ProposedByMemberID string     `json:"proposed_by_member_id"`
	Category           string     `json:"category"`
	Content            string     `json:"content"`
	Checksum           string     `json:"checksum"`
	State              string     `json:"state"`
	ReviewedByMemberID string     `json:"reviewed_by_member_id,omitempty"`
	Confidence         float64    `json:"confidence"`
	CreatedAt          time.Time  `json:"created_at"`
	ReviewedAt         *time.Time `json:"reviewed_at,omitempty"`
}

type GroupMemorySnapshot struct {
	SourceChannel  string                `json:"source_channel"`
	ConversationID string                `json:"conversation_id"`
	Facts          []MemoryFact          `json:"facts"`
	Proposals      []GroupMemoryProposal `json:"proposals"`
}

func (s *Service) GetGroupMemory(ctx context.Context, rawToken, deviceID string, platformID int32, sourceChannel, conversationID string) (GroupMemorySnapshot, error) {
	member, scope, err := s.authorizeGroupMemory(ctx, rawToken, deviceID, platformID, sourceChannel, conversationID)
	_ = member
	if err != nil {
		return GroupMemorySnapshot{}, err
	}
	facts, err := s.groupMemory.ListGroup(ctx, scope, 100)
	if err != nil {
		return GroupMemorySnapshot{}, err
	}
	proposals, err := s.groupMemory.ListGroupProposals(ctx, scope, 100)
	if err != nil {
		return GroupMemorySnapshot{}, err
	}
	result := GroupMemorySnapshot{SourceChannel: scope.SourceChannel, ConversationID: scope.ConversationID,
		Facts: make([]MemoryFact, 0, len(facts)), Proposals: make([]GroupMemoryProposal, 0, len(proposals))}
	for _, fact := range facts {
		result.Facts = append(result.Facts, MemoryFact{ID: fact.ID, Category: fact.Category, Content: fact.Content, Checksum: fact.Checksum, UpdatedAt: fact.UpdatedAt})
	}
	for _, proposal := range proposals {
		result.Proposals = append(result.Proposals, GroupMemoryProposal{
			ID: proposal.ID, SourceRunID: proposal.SourceRunID, ProposedByMemberID: proposal.ProposedByMemberID,
			Category: proposal.Category, Content: proposal.Content, Checksum: proposal.Checksum,
			State: proposal.State, ReviewedByMemberID: proposal.ReviewedByMemberID,
			Confidence: proposal.Confidence, CreatedAt: proposal.CreatedAt, ReviewedAt: proposal.ReviewedAt,
		})
	}
	return result, nil
}

func (s *Service) ReviewGroupMemory(ctx context.Context, rawToken, deviceID string, platformID int32, sourceChannel, conversationID, proposalID, decision string) (string, error) {
	member, scope, err := s.authorizeGroupMemory(ctx, rawToken, deviceID, platformID, sourceChannel, conversationID)
	if err != nil {
		return "", err
	}
	return s.groupMemory.ReviewGroupProposal(ctx, scope, strings.TrimSpace(proposalID), member.ID, strings.TrimSpace(decision))
}

func (s *Service) authorizeGroupMemory(ctx context.Context, rawToken, deviceID string, platformID int32, sourceChannel, conversationID string) (identity.Member, memory.Scope, error) {
	member, err := s.resolve(ctx, rawToken, deviceID, platformID)
	if err != nil {
		return identity.Member{}, memory.Scope{}, err
	}
	if s.groupMemory == nil || s.groupAccess == nil || sourceChannel != "openim" {
		return identity.Member{}, memory.Scope{}, ErrGroupMemoryInvalid
	}
	groupID, ok := strings.CutPrefix(strings.TrimSpace(conversationID), "sg_")
	if !ok || groupID == "" {
		return identity.Member{}, memory.Scope{}, ErrGroupMemoryInvalid
	}
	userID, err := s.members.ReadyOpenIMUserID(ctx, member.ID)
	if err != nil {
		return identity.Member{}, memory.Scope{}, err
	}
	allowed, err := s.groupAccess.IsGroupMember(ctx, groupID, userID)
	if err != nil {
		return identity.Member{}, memory.Scope{}, fmt.Errorf("authorize group memory membership: %w", err)
	}
	if !allowed {
		return identity.Member{}, memory.Scope{}, ErrGroupMemoryForbidden
	}
	scope := memory.Scope{Type: "group", TenantID: member.TenantID, SourceChannel: "openim", ConversationID: "sg_" + groupID}
	return member, scope, nil
}

func (s *Service) GetMemory(ctx context.Context, rawToken, deviceID string, platformID int32) (MemorySnapshot, error) {
	member, err := s.resolve(ctx, rawToken, deviceID, platformID)
	if err != nil {
		return MemorySnapshot{}, err
	}
	facts, err := s.memory.ListPersonal(ctx, member.TenantID, member.ID, 100)
	if err != nil {
		return MemorySnapshot{}, fmt.Errorf("read member memory: %w", err)
	}
	exposures, err := s.memory.ListPersonalExposures(ctx, member.TenantID, member.ID, 100)
	if err != nil {
		return MemorySnapshot{}, fmt.Errorf("read member memory exposures: %w", err)
	}
	result := MemorySnapshot{Facts: make([]MemoryFact, 0, len(facts)), Exposures: make([]MemoryExposure, 0, len(exposures))}
	for _, fact := range facts {
		result.Facts = append(result.Facts, MemoryFact{
			ID: fact.ID, Category: fact.Category, Content: fact.Content,
			Checksum: fact.Checksum, UpdatedAt: fact.UpdatedAt,
		})
	}
	for _, exposure := range exposures {
		result.Exposures = append(result.Exposures, MemoryExposure{
			ID: exposure.ID, RunID: exposure.RunID, FactID: exposure.FactID,
			Category: exposure.Category, Content: exposure.Content,
			RetrievalReason: exposure.RetrievalReason, Feedback: exposure.Feedback,
			CreatedAt: exposure.CreatedAt,
		})
	}
	return result, nil
}

func (s *Service) DeleteMemoryFact(ctx context.Context, rawToken, deviceID string, platformID int32, factID, idempotencyKey string) (string, error) {
	member, err := s.resolve(ctx, rawToken, deviceID, platformID)
	if err != nil {
		return "", err
	}
	factID = strings.TrimSpace(factID)
	idempotencyKey = strings.TrimSpace(idempotencyKey)
	if factID == "" || idempotencyKey == "" || len(idempotencyKey) > 128 {
		return "", errors.New("memory delete request is invalid")
	}
	eventID, err := s.memory.DeletePersonalFact(ctx, member.TenantID, member.ID, factID,
		"api:memory-delete:"+member.ID+":"+idempotencyKey)
	if err != nil {
		return "", err
	}
	return eventID, nil
}

func (s *Service) FeedbackMemory(ctx context.Context, rawToken, deviceID string, platformID int32, exposureID, signal string) error {
	member, err := s.resolve(ctx, rawToken, deviceID, platformID)
	if err != nil {
		return err
	}
	exposureID, signal = strings.TrimSpace(exposureID), strings.TrimSpace(signal)
	if exposureID == "" {
		return errors.New("memory exposure is required")
	}
	return s.memory.RecordFeedback(ctx, member.TenantID, exposureID, member.ID, signal)
}

type ProactivePreference struct {
	Enabled      bool    `json:"enabled"`
	Timezone     string  `json:"timezone"`
	QuietStart   string  `json:"quiet_start"`
	QuietEnd     string  `json:"quiet_end"`
	DailyBudget  int     `json:"daily_budget"`
	MinimumScore float64 `json:"minimum_score"`
}

type ProactiveSubscription struct {
	ID               string     `json:"subscription_id"`
	AgentID          string     `json:"agent_id"`
	Query            string     `json:"query"`
	Categories       []string   `json:"categories"`
	SourceChannel    string     `json:"source_channel"`
	Enabled          bool       `json:"enabled"`
	PollIntervalSecs int        `json:"poll_interval_seconds"`
	BaselineComplete bool       `json:"baseline_complete"`
	LastSuccessAt    *time.Time `json:"last_success_at,omitempty"`
	LastError        string     `json:"last_error,omitempty"`
	CreatedAt        time.Time  `json:"created_at"`
	UpdatedAt        time.Time  `json:"updated_at"`
}

type ProactiveEvent struct {
	ID                string    `json:"event_id"`
	SubscriptionID    string    `json:"subscription_id"`
	Title             string    `json:"title"`
	Summary           string    `json:"summary"`
	URL               string    `json:"url"`
	State             string    `json:"state"`
	Score             *float64  `json:"score,omitempty"`
	RankReasons       []string  `json:"rank_reasons"`
	SuppressionReason string    `json:"suppression_reason,omitempty"`
	RunID             string    `json:"run_id,omitempty"`
	Feedback          string    `json:"feedback,omitempty"`
	PublishedAt       time.Time `json:"published_at"`
	CreatedAt         time.Time `json:"created_at"`
	UpdatedAt         time.Time `json:"updated_at"`
}

type ProactiveSnapshot struct {
	Preference    ProactivePreference     `json:"preference"`
	Subscriptions []ProactiveSubscription `json:"subscriptions"`
	Events        []ProactiveEvent        `json:"events"`
}

func (s *Service) GetProactive(ctx context.Context, rawToken, deviceID string, platformID int32) (ProactiveSnapshot, error) {
	member, err := s.resolve(ctx, rawToken, deviceID, platformID)
	if err != nil {
		return ProactiveSnapshot{}, err
	}
	snapshot, err := s.proactive.ReadMemberSnapshot(ctx, member.TenantID, member.ID, 50)
	if err != nil {
		return ProactiveSnapshot{}, err
	}
	result := ProactiveSnapshot{
		Preference: ProactivePreference{
			Enabled: snapshot.Preference.Enabled, Timezone: snapshot.Preference.Timezone,
			QuietStart: formatClock(snapshot.Preference.QuietStart), QuietEnd: formatClock(snapshot.Preference.QuietEnd),
			DailyBudget: snapshot.Preference.DailyBudget, MinimumScore: snapshot.Preference.MinimumScore,
		},
		Subscriptions: make([]ProactiveSubscription, 0, len(snapshot.Subscriptions)),
		Events:        make([]ProactiveEvent, 0, len(snapshot.Events)),
	}
	for _, subscription := range snapshot.Subscriptions {
		result.Subscriptions = append(result.Subscriptions, ProactiveSubscription{
			ID: subscription.ID, AgentID: subscription.AgentID, Query: subscription.Query,
			Categories: subscription.Categories, SourceChannel: subscription.SourceChannel,
			Enabled: subscription.Enabled, PollIntervalSecs: int(subscription.PollInterval / time.Second),
			BaselineComplete: subscription.BaselineComplete, LastSuccessAt: subscription.LastSuccessAt,
			LastError: subscription.LastError, CreatedAt: subscription.CreatedAt, UpdatedAt: subscription.UpdatedAt,
		})
	}
	for _, event := range snapshot.Events {
		result.Events = append(result.Events, ProactiveEvent{
			ID: event.ID, SubscriptionID: event.SubscriptionID, Title: event.Title,
			Summary: event.Summary, URL: event.URL, State: event.State, Score: event.Score,
			RankReasons: event.RankReasons, SuppressionReason: event.SuppressionReason,
			RunID: event.RunID, Feedback: event.Feedback, PublishedAt: event.PublishedAt,
			CreatedAt: event.CreatedAt, UpdatedAt: event.UpdatedAt,
		})
	}
	return result, nil
}

type CreateSubscriptionRequest struct {
	AgentID, Query, SourceChannel string
	Categories                    []string
	PollInterval                  time.Duration
}

func (s *Service) CreateSubscription(ctx context.Context, rawToken, deviceID string, platformID int32, request CreateSubscriptionRequest) (string, error) {
	member, err := s.resolve(ctx, rawToken, deviceID, platformID)
	if err != nil {
		return "", err
	}
	request.AgentID = strings.TrimSpace(request.AgentID)
	request.Query = strings.TrimSpace(request.Query)
	request.SourceChannel = strings.TrimSpace(request.SourceChannel)
	var targetID string
	switch request.SourceChannel {
	case "openim":
		targetID, err = s.members.ReadyOpenIMUserID(ctx, member.ID)
	case "telegram":
		targetID, err = s.members.ReadyTelegramUserID(ctx, member.TenantID, member.ID)
	default:
		return "", errors.New("proactive source channel is invalid")
	}
	if err != nil {
		return "", err
	}
	return s.proactive.CreateSubscription(ctx, proactive.Subscription{
		TenantID: member.TenantID, MemberID: member.ID, AgentID: request.AgentID,
		Query: request.Query, Categories: request.Categories, SourceChannel: request.SourceChannel,
		TargetID: targetID, PollInterval: request.PollInterval,
	})
}

func (s *Service) SetSubscriptionEnabled(ctx context.Context, rawToken, deviceID string, platformID int32, subscriptionID string, enabled bool) error {
	member, err := s.resolve(ctx, rawToken, deviceID, platformID)
	if err != nil {
		return err
	}
	return s.proactive.SetSubscriptionEnabled(ctx, member.TenantID, member.ID, strings.TrimSpace(subscriptionID), enabled)
}

func (s *Service) UpdatePreferences(ctx context.Context, rawToken, deviceID string, platformID int32, preference proactive.Preference) error {
	member, err := s.resolve(ctx, rawToken, deviceID, platformID)
	if err != nil {
		return err
	}
	return s.proactive.UpdatePreferences(ctx, member.TenantID, member.ID, preference)
}

func (s *Service) Acknowledge(ctx context.Context, rawToken, deviceID string, platformID int32, eventID, signal string) error {
	member, err := s.resolve(ctx, rawToken, deviceID, platformID)
	if err != nil {
		return err
	}
	return s.proactive.Acknowledge(ctx, member.TenantID, strings.TrimSpace(eventID), member.ID, strings.TrimSpace(signal))
}

type ToolApproval struct {
	ID              string    `json:"approval_id"`
	RunID           string    `json:"run_id"`
	OperationID     string    `json:"operation_id"`
	ArgumentsDigest string    `json:"arguments_digest"`
	Risk            string    `json:"risk"`
	PolicyReason    string    `json:"policy_reason"`
	ExpiresAt       time.Time `json:"expires_at"`
}

func (s *Service) ListToolApprovals(ctx context.Context, rawToken, deviceID string, platformID int32) ([]ToolApproval, error) {
	member, err := s.resolve(ctx, rawToken, deviceID, platformID)
	if err != nil {
		return nil, err
	}
	items, err := s.tools.ListPendingApprovals(ctx, member.TenantID, member.ID, 50)
	if err != nil {
		return nil, err
	}
	result := make([]ToolApproval, 0, len(items))
	for _, item := range items {
		result = append(result, ToolApproval{
			ID: item.ID, RunID: item.RunID, OperationID: item.OperationID,
			ArgumentsDigest: item.ArgumentsDigest, Risk: item.Risk,
			PolicyReason: item.PolicyReason, ExpiresAt: item.ExpiresAt,
		})
	}
	return result, nil
}

func (s *Service) DecideToolApproval(ctx context.Context, rawToken, deviceID string, platformID int32, approvalID, argumentsDigest, decision string) error {
	member, err := s.resolve(ctx, rawToken, deviceID, platformID)
	if err != nil {
		return err
	}
	approvalID, argumentsDigest = strings.TrimSpace(approvalID), strings.TrimSpace(argumentsDigest)
	if approvalID == "" || argumentsDigest == "" {
		return errors.New("tool approval decision is invalid")
	}
	switch decision {
	case "approve":
		return s.tools.Approve(ctx, member.TenantID, approvalID, member.ID, argumentsDigest)
	case "reject":
		return s.tools.Reject(ctx, member.TenantID, approvalID, member.ID, argumentsDigest)
	default:
		return errors.New("tool approval decision is invalid")
	}
}

func (s *Service) GetReplay(ctx context.Context, rawToken, deviceID string, platformID int32, runID string) (observe.Bundle, error) {
	member, err := s.resolve(ctx, rawToken, deviceID, platformID)
	if err != nil {
		return observe.Bundle{}, err
	}
	return s.replays.ReadReplay(ctx, member.TenantID, member.ID, strings.TrimSpace(runID))
}

type Delegation struct {
	ID              string    `json:"delegation_id"`
	ParentRunID     string    `json:"parent_run_id"`
	ChildRunID      string    `json:"child_run_id"`
	TargetAgentID   string    `json:"target_agent_id"`
	TargetAgentSlug string    `json:"target_agent_slug"`
	Task            string    `json:"task"`
	State           string    `json:"state"`
	LastError       string    `json:"last_error,omitempty"`
	CreatedAt       time.Time `json:"created_at"`
	UpdatedAt       time.Time `json:"updated_at"`
}

func (s *Service) ListDelegations(ctx context.Context, rawToken, deviceID string, platformID int32) ([]Delegation, error) {
	member, err := s.resolve(ctx, rawToken, deviceID, platformID)
	if err != nil {
		return nil, err
	}
	jobs, err := s.delegations.ListMember(ctx, member.TenantID, member.ID, 50)
	if err != nil {
		return nil, err
	}
	result := make([]Delegation, 0, len(jobs))
	for _, job := range jobs {
		result = append(result, Delegation{
			ID: job.ID, ParentRunID: job.ParentRunID, ChildRunID: job.ChildRunID,
			TargetAgentID: job.TargetAgentID, TargetAgentSlug: job.TargetAgentSlug,
			Task: job.Task, State: job.State, LastError: job.LastError,
			CreatedAt: job.CreatedAt, UpdatedAt: job.UpdatedAt,
		})
	}
	return result, nil
}

func (s *Service) resolve(ctx context.Context, rawToken, deviceID string, platformID int32) (identity.Member, error) {
	principal, err := s.verifier.Verify(ctx, rawToken)
	if err != nil {
		return identity.Member{}, identity.ErrUnauthenticated
	}
	return s.members.ResolveActiveMember(ctx, principal, deviceID, platformID)
}

func formatClock(value time.Duration) string {
	hour := int(value / time.Hour)
	value %= time.Hour
	minute := int(value / time.Minute)
	return fmt.Sprintf("%02d:%02d", hour, minute)
}

var _ Verifier = (*identity.OIDCVerifier)(nil)
var _ MemberResolver = (*identity.PostgresStore)(nil)
var _ MemoryStore = (*memory.Store)(nil)
var _ ProactiveStore = (*proactive.Store)(nil)
var _ ToolApprovalStore = (*toolruntime.Store)(nil)
var _ ReplayStore = (*observe.Store)(nil)
var _ DelegationStore = (*delegation.Store)(nil)
