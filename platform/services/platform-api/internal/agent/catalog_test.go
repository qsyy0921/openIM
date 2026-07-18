package agent

import (
	"context"
	"errors"
	"testing"

	"github.com/qsyy0921/openim/platform/services/platform-api/internal/identity"
)

const seedAgentSpecJSON = `{"runtime_kind":"knowledge_ticket_v1","instructions":"Answer only from authorized evidence and abstain when evidence is absent.","model_route":"deepseek-v4-pro","retrieval":{"purpose":"agent_answer","limit":5},"allowed_action_types":["create_ticket"],"max_model_attempts":3}`
const seedAgentSpecChecksum = "sha256:27dcf2cfab60cb915d584127e1524bcda821ce29c8383346adb8f8d2b31bb8d8"

func TestParseAgentSpecAcceptsSeedAndVerifiesChecksum(t *testing.T) {
	spec, err := ParseAgentSpec(AgentSpecSchemaV1, []byte(seedAgentSpecJSON), seedAgentSpecChecksum)
	if err != nil {
		t.Fatal(err)
	}
	if spec.RuntimeKind != KnowledgeTicketRuntime || spec.Retrieval.Limit != 5 || !spec.AllowsAction("create_ticket") {
		t.Fatalf("spec = %#v", spec)
	}
	checksum, err := AgentSpecChecksum(spec)
	if err != nil || checksum != seedAgentSpecChecksum {
		t.Fatalf("checksum = %q, %v", checksum, err)
	}
}

func TestDecodeAgentSpecRejectsUnknownFieldsBeforePublication(t *testing.T) {
	if _, err := DecodeAgentSpec(AgentSpecSchemaV1, []byte(`{"runtime_kind":"knowledge_ticket_v1","unknown":true}`)); !errors.Is(err, ErrInvalidCatalog) {
		t.Fatalf("error = %v", err)
	}
}

type catalogStoreStub struct {
	tenantID string
	agents   []AgentSummary
	err      error
}

func (s *catalogStoreStub) ListAgents(_ context.Context, tenantID string) ([]AgentSummary, error) {
	s.tenantID = tenantID
	return s.agents, s.err
}

func TestCatalogServiceAuthenticatesAndScopesTenant(t *testing.T) {
	store := &catalogStoreStub{agents: []AgentSummary{{ID: "agent-1", TriggerAlias: "@agent"}}}
	service := NewCatalogService(
		workspaceVerifierStub{principal: identity.Principal{Subject: "subject"}},
		workspaceMembersStub{member: identity.Member{ID: "member-1", TenantID: "tenant-1"}},
		store,
	)
	agents, err := service.List(context.Background(), "token", "device", 5)
	if err != nil || store.tenantID != "tenant-1" || len(agents) != 1 {
		t.Fatalf("agents=%#v store=%#v err=%v", agents, store, err)
	}
}

func TestCatalogServiceFailsClosedOnIdentity(t *testing.T) {
	service := NewCatalogService(workspaceVerifierStub{err: errors.New("bad token")}, workspaceMembersStub{}, &catalogStoreStub{})
	if _, err := service.List(context.Background(), "token", "device", 5); !errors.Is(err, identity.ErrUnauthenticated) {
		t.Fatalf("error=%v", err)
	}
}

func TestParseAgentSpecFailsClosed(t *testing.T) {
	tests := []struct {
		name, raw, checksum string
		schema              int
	}{
		{name: "schema", raw: seedAgentSpecJSON, checksum: seedAgentSpecChecksum, schema: 2},
		{name: "unknown field", raw: `{"runtime_kind":"knowledge_ticket_v1","unknown":true}`, checksum: seedAgentSpecChecksum, schema: 1},
		{name: "route", raw: `{"runtime_kind":"knowledge_ticket_v1","instructions":"x","model_route":"other","retrieval":{"purpose":"agent_answer","limit":5},"allowed_action_types":[],"max_model_attempts":3}`, checksum: seedAgentSpecChecksum, schema: 1},
		{name: "action", raw: `{"runtime_kind":"knowledge_ticket_v1","instructions":"x","model_route":"deepseek-v4-pro","retrieval":{"purpose":"agent_answer","limit":5},"allowed_action_types":["delete_user"],"max_model_attempts":3}`, checksum: seedAgentSpecChecksum, schema: 1},
		{name: "checksum", raw: seedAgentSpecJSON, checksum: "sha256:wrong", schema: 1},
	}
	for _, test := range tests {
		t.Run(test.name, func(t *testing.T) {
			if _, err := ParseAgentSpec(test.schema, []byte(test.raw), test.checksum); !errors.Is(err, ErrInvalidCatalog) {
				t.Fatalf("error = %v", err)
			}
		})
	}
}
