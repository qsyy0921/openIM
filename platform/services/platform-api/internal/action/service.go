package action

import (
	"context"
	"fmt"

	"github.com/qsyy0921/openim/platform/services/platform-api/internal/identity"
)

type Verifier interface {
	Verify(context.Context, string) (identity.Principal, error)
}
type MemberResolver interface {
	ResolveActiveMember(context.Context, identity.Principal, string, int32) (identity.Member, error)
}

type Service struct {
	verifier Verifier
	members  MemberResolver
	store    *Store
}

func NewService(verifier Verifier, members MemberResolver, store *Store) *Service {
	return &Service{verifier: verifier, members: members, store: store}
}

func (s *Service) Approve(ctx context.Context, rawToken, deviceID string, platformID int32, intentID, digest string) (ApprovalResult, error) {
	principal, err := s.verifier.Verify(ctx, rawToken)
	if err != nil {
		return ApprovalResult{}, identity.ErrUnauthenticated
	}
	member, err := s.members.ResolveActiveMember(ctx, principal, deviceID, platformID)
	if err != nil {
		return ApprovalResult{}, err
	}
	result, err := s.store.Approve(ctx, Principal{TenantID: member.TenantID, MemberID: member.ID}, intentID, digest)
	if err != nil {
		return ApprovalResult{}, fmt.Errorf("approve action: %w", err)
	}
	return result, nil
}
