package admincontrol

import (
	"context"
	"errors"
	"testing"

	"github.com/qsyy0921/openim/platform/services/platform-api/internal/identity"
	"github.com/qsyy0921/openim/platform/services/platform-api/internal/observe"
	"github.com/qsyy0921/openim/platform/services/platform-api/internal/runtimecontrol"
)

type verifierStub struct{ err error }

func (s verifierStub) Verify(context.Context, string) (identity.Principal, error) {
	return identity.Principal{Subject: "member"}, s.err
}

type memberStoreStub struct{ roles []string }

func (s memberStoreStub) ResolveActiveMember(context.Context, identity.Principal, string, int32) (identity.Member, error) {
	return identity.Member{ID: "member-1", TenantID: "tenant-1"}, nil
}
func (s memberStoreStub) ListMemberRoles(context.Context, string, string) ([]string, error) {
	return s.roles, nil
}

type runtimeStoreStub struct{ setCalls int }

func (s *runtimeStoreStub) List(context.Context, string) ([]runtimecontrol.Control, error) {
	return nil, nil
}
func (s *runtimeStoreStub) Set(_ context.Context, tenantID, memberID, component string, paused bool, reason string, revision int64) (runtimecontrol.Control, error) {
	s.setCalls++
	return runtimecontrol.Control{Component: component, Paused: paused, Reason: reason, Revision: revision + 1}, nil
}

type observerStub struct{}

func (observerStub) ReadOperationalSnapshot(context.Context, string) (observe.OperationalSnapshot, error) {
	return observe.OperationalSnapshot{}, nil
}

func TestEffectiveControlsAddsExplicitRunningDefaults(t *testing.T) {
	controls := effectiveControls([]runtimecontrol.Control{{Component: "agent_delivery", Paused: true, Reason: "incident", Revision: 2}})
	if len(controls) != 3 {
		t.Fatalf("controls=%d", len(controls))
	}
	if controls[0].Component != "agent_execution" || controls[0].Paused || controls[0].Revision != 0 {
		t.Fatalf("unexpected execution default: %#v", controls[0])
	}
	if controls[1].Component != "agent_delivery" || !controls[1].Paused || controls[1].Revision != 2 {
		t.Fatalf("unexpected stored control: %#v", controls[1])
	}
}

func TestAgentAdminCanReadButCannotChangeIncidentControls(t *testing.T) {
	runtime := &runtimeStoreStub{}
	service := NewService(verifierStub{}, memberStoreStub{roles: []string{"agent_admin"}}, runtime, observerStub{})
	if _, err := service.GetSnapshot(context.Background(), "token", "device", 5); err != nil {
		t.Fatal(err)
	}
	_, err := service.SetRuntimeControl(context.Background(), "token", "device", 5, "agent_execution", true, "incident", 0)
	if !errors.Is(err, ErrForbidden) || runtime.setCalls != 0 {
		t.Fatalf("err=%v calls=%d", err, runtime.setCalls)
	}
}

func TestPlatformAdminCanChangeIncidentControls(t *testing.T) {
	runtime := &runtimeStoreStub{}
	service := NewService(verifierStub{}, memberStoreStub{roles: []string{"platform_admin"}}, runtime, observerStub{})
	control, err := service.SetRuntimeControl(context.Background(), "token", "device", 5, "agent_delivery", true, "outage", 2)
	if err != nil {
		t.Fatal(err)
	}
	if runtime.setCalls != 1 || control.Revision != 3 {
		t.Fatalf("control=%#v calls=%d", control, runtime.setCalls)
	}
}
