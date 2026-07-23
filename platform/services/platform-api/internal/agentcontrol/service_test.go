package agentcontrol

import (
	"context"
	"errors"
	"testing"
	"time"

	"github.com/qsyy0921/openim/platform/services/platform-api/internal/delegation"
	"github.com/qsyy0921/openim/platform/services/platform-api/internal/identity"
	"github.com/qsyy0921/openim/platform/services/platform-api/internal/memory"
	"github.com/qsyy0921/openim/platform/services/platform-api/internal/observe"
	"github.com/qsyy0921/openim/platform/services/platform-api/internal/proactive"
	"github.com/qsyy0921/openim/platform/services/platform-api/internal/toolruntime"
)

type verifierStub struct{ err error }

func (s verifierStub) Verify(context.Context, string) (identity.Principal, error) {
	return identity.Principal{Subject: "subject"}, s.err
}

type memberResolverStub struct {
	openIMTarget, telegramTarget string
}

func (s memberResolverStub) ResolveActiveMember(context.Context, identity.Principal, string, int32) (identity.Member, error) {
	return identity.Member{ID: "member", TenantID: "tenant"}, nil
}
func (s memberResolverStub) ReadyOpenIMUserID(context.Context, string) (string, error) {
	return s.openIMTarget, nil
}
func (s memberResolverStub) ReadyTelegramUserID(context.Context, string, string) (string, error) {
	return s.telegramTarget, nil
}

type memoryStoreStub struct {
	deleteTenant, deleteMember, deleteFact, deleteKey string
}

func (*memoryStoreStub) ListPersonal(context.Context, string, string, int) ([]memory.Fact, error) {
	return nil, nil
}
func (*memoryStoreStub) ListPersonalExposures(context.Context, string, string, int) ([]memory.Exposure, error) {
	return nil, nil
}
func (s *memoryStoreStub) DeletePersonalFact(_ context.Context, tenantID, memberID, factID, key string) (string, error) {
	s.deleteTenant, s.deleteMember, s.deleteFact, s.deleteKey = tenantID, memberID, factID, key
	return "event", nil
}
func (*memoryStoreStub) RecordFeedback(context.Context, string, string, string, string) error {
	return nil
}

type proactiveStoreStub struct{ subscription proactive.Subscription }

func (*proactiveStoreStub) ReadMemberSnapshot(context.Context, string, string, int) (proactive.MemberSnapshot, error) {
	return proactive.MemberSnapshot{}, nil
}
func (s *proactiveStoreStub) CreateSubscription(_ context.Context, subscription proactive.Subscription) (string, error) {
	s.subscription = subscription
	return "subscription", nil
}
func (*proactiveStoreStub) SetSubscriptionEnabled(context.Context, string, string, string, bool) error {
	return nil
}
func (*proactiveStoreStub) UpdatePreferences(context.Context, string, string, proactive.Preference) error {
	return nil
}
func (*proactiveStoreStub) Acknowledge(context.Context, string, string, string, string) error {
	return nil
}

type toolApprovalStoreStub struct {
	tenantID, approvalID, actorID, digest, decision string
}

type replayStoreStub struct{}

type delegationStoreStub struct{ jobs []delegation.Job }

func (s *delegationStoreStub) ListMember(context.Context, string, string, int) ([]delegation.Job, error) {
	return s.jobs, nil
}

func (*replayStoreStub) ReadReplay(context.Context, string, string, string) (observe.Bundle, error) {
	return observe.Bundle{}, nil
}

func (*toolApprovalStoreStub) ListPendingApprovals(context.Context, string, string, int) ([]toolruntime.Approval, error) {
	return nil, nil
}
func (s *toolApprovalStoreStub) Approve(_ context.Context, tenantID, approvalID, actorID, digest string) error {
	s.tenantID, s.approvalID, s.actorID, s.digest, s.decision = tenantID, approvalID, actorID, digest, "approve"
	return nil
}
func (s *toolApprovalStoreStub) Reject(_ context.Context, tenantID, approvalID, actorID, digest string) error {
	s.tenantID, s.approvalID, s.actorID, s.digest, s.decision = tenantID, approvalID, actorID, digest, "reject"
	return nil
}

func TestCreateSubscriptionDerivesBoundTelegramTarget(t *testing.T) {
	proactiveStore := &proactiveStoreStub{}
	service := NewService(verifierStub{}, memberResolverStub{telegramTarget: "12345"}, &memoryStoreStub{}, proactiveStore, &toolApprovalStoreStub{}, &replayStoreStub{}, &delegationStoreStub{})
	_, err := service.CreateSubscription(context.Background(), "token", "device", 5, CreateSubscriptionRequest{
		AgentID: "agent", Query: "LLM agents", SourceChannel: "telegram",
		Categories: []string{"cs.AI"}, PollInterval: 30 * time.Minute,
	})
	if err != nil {
		t.Fatalf("create subscription: %v", err)
	}
	if proactiveStore.subscription.TargetID != "12345" || proactiveStore.subscription.MemberID != "member" {
		t.Fatalf("subscription target was not derived from member binding: %+v", proactiveStore.subscription)
	}
}

func TestDeleteMemoryFactScopesIdempotencyToMember(t *testing.T) {
	memoryStore := &memoryStoreStub{}
	service := NewService(verifierStub{}, memberResolverStub{}, memoryStore, &proactiveStoreStub{}, &toolApprovalStoreStub{}, &replayStoreStub{}, &delegationStoreStub{})
	if _, err := service.DeleteMemoryFact(context.Background(), "token", "device", 5, "fact", "request-1"); err != nil {
		t.Fatalf("delete memory fact: %v", err)
	}
	if memoryStore.deleteTenant != "tenant" || memoryStore.deleteMember != "member" ||
		memoryStore.deleteFact != "fact" || memoryStore.deleteKey != "api:memory-delete:member:request-1" {
		t.Fatalf("unexpected delete scope: %+v", memoryStore)
	}
}

func TestControlServiceFailsClosedOnInvalidIdentity(t *testing.T) {
	service := NewService(verifierStub{err: errors.New("bad token")}, memberResolverStub{}, &memoryStoreStub{}, &proactiveStoreStub{}, &toolApprovalStoreStub{}, &replayStoreStub{}, &delegationStoreStub{})
	_, err := service.GetMemory(context.Background(), "token", "device", 5)
	if !errors.Is(err, identity.ErrUnauthenticated) {
		t.Fatalf("GetMemory error = %v", err)
	}
}

func TestToolApprovalDecisionUsesAuthenticatedMember(t *testing.T) {
	tools := &toolApprovalStoreStub{}
	service := NewService(verifierStub{}, memberResolverStub{}, &memoryStoreStub{}, &proactiveStoreStub{}, tools, &replayStoreStub{}, &delegationStoreStub{})
	digest := "sha256:aaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaa"
	if err := service.DecideToolApproval(context.Background(), "token", "device", 5, "approval", digest, "reject"); err != nil {
		t.Fatalf("reject tool approval: %v", err)
	}
	if tools.tenantID != "tenant" || tools.actorID != "member" || tools.approvalID != "approval" ||
		tools.digest != digest || tools.decision != "reject" {
		t.Fatalf("unexpected tool approval scope: %+v", tools)
	}
}

func TestListDelegationsUsesAuthenticatedMemberScope(t *testing.T) {
	store := &delegationStoreStub{jobs: []delegation.Job{{ID: "job-1", TargetAgentSlug: "research-agent", State: "running"}}}
	service := NewService(verifierStub{}, memberResolverStub{}, &memoryStoreStub{}, &proactiveStoreStub{}, &toolApprovalStoreStub{}, &replayStoreStub{}, store)
	jobs, err := service.ListDelegations(context.Background(), "token", "device", 5)
	if err != nil {
		t.Fatal(err)
	}
	if len(jobs) != 1 || jobs[0].ID != "job-1" || jobs[0].TargetAgentSlug != "research-agent" {
		t.Fatalf("delegations = %#v", jobs)
	}
}
