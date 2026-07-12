package agent

import (
	"context"
	"errors"
	"testing"

	"github.com/qsyy0921/openim/platform/services/platform-api/internal/identity"
)

type workspaceVerifierStub struct {
	principal identity.Principal
	err       error
}

func (s workspaceVerifierStub) Verify(context.Context, string) (identity.Principal, error) {
	return s.principal, s.err
}

type workspaceMembersStub struct {
	member identity.Member
	err    error
}

func (s workspaceMembersStub) ResolveActiveMember(context.Context, identity.Principal, string, int32) (identity.Member, error) {
	return s.member, s.err
}

type workspaceStoreStub struct {
	tenantID, memberID, botID string
	runs                      []WorkspaceRun
	err                       error
}

func (s *workspaceStoreStub) EnsureBotIdentity(_ context.Context, tenantID, botID string) error {
	s.tenantID, s.botID = tenantID, botID
	return s.err
}
func (s *workspaceStoreStub) ReadWorkspace(_ context.Context, tenantID, memberID string, limit int) ([]WorkspaceRun, error) {
	if limit != 30 {
		return nil, errors.New("unexpected workspace limit")
	}
	s.tenantID, s.memberID = tenantID, memberID
	return s.runs, s.err
}

type workspaceBotStub struct {
	userID, tenantID string
	err              error
}

func (s *workspaceBotStub) EnsureAgentBot(_ context.Context, userID, tenantID string) error {
	s.userID, s.tenantID = userID, tenantID
	return s.err
}

func TestWorkspaceServiceAuthenticatesMemberAndEnsuresBot(t *testing.T) {
	store := &workspaceStoreStub{runs: []WorkspaceRun{{ID: "run-1", State: "succeeded"}}}
	bot := &workspaceBotStub{}
	service := NewWorkspaceService(
		workspaceVerifierStub{principal: identity.Principal{Subject: "subject"}},
		workspaceMembersStub{member: identity.Member{ID: "member-1", TenantID: "tenant-1"}},
		store,
		bot,
	)
	workspace, err := service.Get(context.Background(), "token", "device", 5)
	if err != nil {
		t.Fatal(err)
	}
	wantBot := BotUserID("tenant-1")
	if workspace.AgentUserID != wantBot || bot.userID != wantBot || store.botID != wantBot {
		t.Fatalf("workspace bot=%q/%q/%q want %q", workspace.AgentUserID, bot.userID, store.botID, wantBot)
	}
	if store.memberID != "member-1" || len(workspace.Runs) != 1 || workspace.Runs[0].ID != "run-1" {
		t.Fatalf("workspace=%#v store=%#v", workspace, store)
	}
}

func TestWorkspaceServiceFailsClosedOnIdentityAndBotErrors(t *testing.T) {
	tests := []struct {
		name                           string
		verifierErr, memberErr, botErr error
		want                           error
	}{
		{name: "identity", verifierErr: errors.New("bad token"), want: identity.ErrUnauthenticated},
		{name: "device", memberErr: identity.ErrForbidden, want: identity.ErrForbidden},
		{name: "bot", botErr: errors.New("OpenIM unavailable"), want: errors.New("ensure Agent workspace bot")},
	}
	for _, test := range tests {
		t.Run(test.name, func(t *testing.T) {
			store := &workspaceStoreStub{}
			bot := &workspaceBotStub{err: test.botErr}
			service := NewWorkspaceService(
				workspaceVerifierStub{principal: identity.Principal{Subject: "subject"}, err: test.verifierErr},
				workspaceMembersStub{member: identity.Member{ID: "member-1", TenantID: "tenant-1"}, err: test.memberErr},
				store, bot,
			)
			_, err := service.Get(context.Background(), "token", "device", 5)
			if test.name == "bot" {
				if err == nil || err.Error() == "" {
					t.Fatalf("Get() error=%v", err)
				}
				return
			}
			if !errors.Is(err, test.want) {
				t.Fatalf("Get() error=%v want %v", err, test.want)
			}
		})
	}
}
