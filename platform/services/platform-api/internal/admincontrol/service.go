package admincontrol

import (
	"context"
	"errors"
	"slices"
	"strings"

	"github.com/qsyy0921/openim/platform/services/platform-api/internal/identity"
	"github.com/qsyy0921/openim/platform/services/platform-api/internal/observe"
	"github.com/qsyy0921/openim/platform/services/platform-api/internal/remotea2a"
	"github.com/qsyy0921/openim/platform/services/platform-api/internal/runtimecontrol"
)

var ErrForbidden = errors.New("Agent administration is forbidden")

type Verifier interface {
	Verify(context.Context, string) (identity.Principal, error)
}

type MemberStore interface {
	ResolveActiveMember(context.Context, identity.Principal, string, int32) (identity.Member, error)
	ListMemberRoles(context.Context, string, string) ([]string, error)
}

type RuntimeStore interface {
	List(context.Context, string) ([]runtimecontrol.Control, error)
	Set(context.Context, string, string, string, bool, string, int64) (runtimecontrol.Control, error)
}

type Observer interface {
	ReadOperationalSnapshot(context.Context, string) (observe.OperationalSnapshot, error)
}

type Service struct {
	verifier  Verifier
	members   MemberStore
	runtime   RuntimeStore
	observer  Observer
	catalog   *CatalogStore
	remoteA2A *remotea2a.Store
}

func NewService(verifier Verifier, members MemberStore, runtime RuntimeStore, observer Observer) *Service {
	if verifier == nil || members == nil || runtime == nil || observer == nil {
		panic("administrator verifier, member, runtime, and observer stores are required")
	}
	return &Service{verifier: verifier, members: members, runtime: runtime, observer: observer}
}

type Snapshot struct {
	Roles      []string                    `json:"roles"`
	Controls   []runtimecontrol.Control    `json:"controls"`
	Operations observe.OperationalSnapshot `json:"operations"`
}

func (s *Service) GetSnapshot(ctx context.Context, rawToken, deviceID string, platformID int32) (Snapshot, error) {
	member, roles, err := s.resolve(ctx, rawToken, deviceID, platformID)
	if err != nil {
		return Snapshot{}, err
	}
	if !slices.Contains(roles, "platform_admin") && !slices.Contains(roles, "agent_admin") {
		return Snapshot{}, ErrForbidden
	}
	controls, err := s.runtime.List(ctx, member.TenantID)
	if err != nil {
		return Snapshot{}, err
	}
	operations, err := s.observer.ReadOperationalSnapshot(ctx, member.TenantID)
	if err != nil {
		return Snapshot{}, err
	}
	return Snapshot{Roles: roles, Controls: effectiveControls(controls), Operations: operations}, nil
}

func (s *Service) SetRuntimeControl(ctx context.Context, rawToken, deviceID string, platformID int32, component string, paused bool, reason string, expectedRevision int64) (runtimecontrol.Control, error) {
	member, roles, err := s.resolve(ctx, rawToken, deviceID, platformID)
	if err != nil {
		return runtimecontrol.Control{}, err
	}
	if !slices.Contains(roles, "platform_admin") {
		return runtimecontrol.Control{}, ErrForbidden
	}
	return s.runtime.Set(ctx, member.TenantID, member.ID, strings.TrimSpace(component), paused, strings.TrimSpace(reason), expectedRevision)
}

func (s *Service) resolve(ctx context.Context, rawToken, deviceID string, platformID int32) (identity.Member, []string, error) {
	principal, err := s.verifier.Verify(ctx, rawToken)
	if err != nil {
		return identity.Member{}, nil, identity.ErrUnauthenticated
	}
	member, err := s.members.ResolveActiveMember(ctx, principal, deviceID, platformID)
	if err != nil {
		return identity.Member{}, nil, err
	}
	roles, err := s.members.ListMemberRoles(ctx, member.TenantID, member.ID)
	if err != nil {
		return identity.Member{}, nil, err
	}
	return member, roles, nil
}

func effectiveControls(stored []runtimecontrol.Control) []runtimecontrol.Control {
	byComponent := make(map[string]runtimecontrol.Control, len(stored))
	for _, control := range stored {
		byComponent[control.Component] = control
	}
	result := make([]runtimecontrol.Control, 0, 3)
	for _, component := range []string{"agent_execution", "agent_delivery", "proactive_dispatch"} {
		control, ok := byComponent[component]
		if !ok {
			control = runtimecontrol.Control{Component: component, Paused: false, Reason: "not configured", Revision: 0}
		}
		result = append(result, control)
	}
	return result
}

var _ Verifier = (*identity.OIDCVerifier)(nil)
var _ MemberStore = (*identity.PostgresStore)(nil)
var _ RuntimeStore = (*runtimecontrol.Store)(nil)
var _ Observer = (*observe.Store)(nil)
