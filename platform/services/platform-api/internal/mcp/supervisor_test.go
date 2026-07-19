package mcp

import (
	"context"
	"encoding/json"
	"errors"
	"testing"
	"time"
)

type registryStub struct {
	servers []Server
	states  []string
}

func (s *registryStub) ListEnabled(context.Context) ([]Server, error) { return s.servers, nil }
func (s *registryStub) RecordHealth(_ context.Context, _ Server, _ string, state string, _ int, _ string) error {
	s.states = append(s.states, state)
	return nil
}

type sessionStub struct {
	tools  []Tool
	closed bool
	err    error
}

func (s *sessionStub) Call(context.Context, string, map[string]any) (json.RawMessage, error) {
	return json.RawMessage(`{"content":[{"type":"text","text":"ok"}]}`), s.err
}
func (s *sessionStub) Refresh(context.Context) ([]Tool, error) { return s.tools, s.err }
func (s *sessionStub) Close() error                            { s.closed = true; return nil }

func TestSupervisorExposesOnlyDigestMatchedHealthyServer(t *testing.T) {
	tools := []Tool{{Name: "search", InputSchema: json.RawMessage(`{"type":"object"}`)}}
	digest, err := toolCatalogDigest(tools)
	if err != nil {
		t.Fatal(err)
	}
	server := Server{ID: "server-1", TenantID: "tenant-1", Slug: "research", ExpectedCatalogDigest: digest}
	store := &registryStub{servers: []Server{server}}
	session := &sessionStub{tools: tools}
	supervisor, _ := NewSupervisor(store, func(context.Context, ServerConfig) (Session, []Tool, error) {
		return session, tools, nil
	}, time.Second)
	if err := supervisor.reconcile(context.Background()); err != nil {
		t.Fatal(err)
	}
	if _, err := supervisor.Call(context.Background(), "tenant-1", "research", "search", map[string]any{}); err != nil {
		t.Fatal(err)
	}
	if len(store.states) != 2 || store.states[0] != "starting" || store.states[1] != "healthy" {
		t.Fatalf("health states = %#v", store.states)
	}
}

func TestSupervisorRejectsCatalogDrift(t *testing.T) {
	expected, _ := toolCatalogDigest([]Tool{{Name: "search", InputSchema: json.RawMessage(`{"type":"object"}`)}})
	actual := []Tool{{Name: "write", InputSchema: json.RawMessage(`{"type":"object"}`)}}
	store := &registryStub{servers: []Server{{ID: "server-1", TenantID: "tenant-1", Slug: "research", ExpectedCatalogDigest: expected}}}
	session := &sessionStub{tools: actual}
	supervisor, _ := NewSupervisor(store, func(context.Context, ServerConfig) (Session, []Tool, error) {
		return session, actual, nil
	}, time.Second)
	if err := supervisor.reconcile(context.Background()); err != nil {
		t.Fatal(err)
	}
	if _, err := supervisor.Call(context.Background(), "tenant-1", "research", "write", map[string]any{}); err == nil {
		t.Fatal("drifted MCP server was callable")
	}
	if !session.closed {
		t.Fatal("drifted MCP session was not closed")
	}
}

func TestSupervisorRemovesServerAfterHealthFailure(t *testing.T) {
	tools := []Tool{{Name: "search", InputSchema: json.RawMessage(`{"type":"object"}`)}}
	digest, _ := toolCatalogDigest(tools)
	store := &registryStub{servers: []Server{{ID: "server-1", TenantID: "tenant-1", Slug: "research", ExpectedCatalogDigest: digest}}}
	session := &sessionStub{tools: tools}
	supervisor, _ := NewSupervisor(store, func(context.Context, ServerConfig) (Session, []Tool, error) {
		return session, tools, nil
	}, time.Second)
	if err := supervisor.reconcile(context.Background()); err != nil {
		t.Fatal(err)
	}
	session.err = errors.New("crashed")
	if err := supervisor.reconcile(context.Background()); err != nil {
		t.Fatal(err)
	}
	if _, err := supervisor.Call(context.Background(), "tenant-1", "research", "search", map[string]any{}); err == nil {
		t.Fatal("unhealthy MCP server was callable")
	}
}
