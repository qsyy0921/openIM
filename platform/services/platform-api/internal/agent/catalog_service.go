package agent

import (
	"context"
	"fmt"

	"github.com/qsyy0921/openim/platform/services/platform-api/internal/identity"
)

type CatalogStore interface {
	ListAgents(context.Context, string) ([]AgentSummary, error)
}

type CatalogService struct {
	verifier WorkspaceVerifier
	members  WorkspaceMemberResolver
	store    CatalogStore
}

func NewCatalogService(verifier WorkspaceVerifier, members WorkspaceMemberResolver, store CatalogStore) *CatalogService {
	return &CatalogService{verifier: verifier, members: members, store: store}
}

func (s *CatalogService) List(ctx context.Context, rawToken, deviceID string, platformID int32) ([]AgentSummary, error) {
	principal, err := s.verifier.Verify(ctx, rawToken)
	if err != nil {
		return nil, identity.ErrUnauthenticated
	}
	member, err := s.members.ResolveActiveMember(ctx, principal, deviceID, platformID)
	if err != nil {
		return nil, err
	}
	agents, err := s.store.ListAgents(ctx, member.TenantID)
	if err != nil {
		return nil, fmt.Errorf("list tenant Agent catalog: %w", err)
	}
	return agents, nil
}

var _ CatalogStore = (*Store)(nil)
