package mcp

import (
	"context"
	"encoding/json"
	"errors"
	"fmt"
	"sync"
	"time"
)

type Session interface {
	Call(context.Context, string, map[string]any) (json.RawMessage, error)
	Refresh(context.Context) ([]Tool, error)
	Close() error
}

type Connector func(context.Context, ServerConfig) (Session, []Tool, error)

type managedServer struct {
	server       Server
	session      Session
	instanceID   string
	restartCount int
}

type Supervisor struct {
	store    RegistryStore
	connect  Connector
	interval time.Duration
	mu       sync.RWMutex
	servers  map[string]*managedServer
	restarts map[string]int
}

func NewSupervisor(store RegistryStore, connector Connector, interval time.Duration) (*Supervisor, error) {
	if store == nil || connector == nil || interval <= 0 {
		return nil, errors.New("MCP supervisor dependencies and interval are required")
	}
	return &Supervisor{
		store: store, connect: connector, interval: interval,
		servers: make(map[string]*managedServer), restarts: make(map[string]int),
	}, nil
}

func (s *Supervisor) Run(ctx context.Context) error {
	ticker := time.NewTicker(s.interval)
	defer ticker.Stop()
	defer s.closeAll()
	for {
		if err := s.reconcile(ctx); err != nil {
			return err
		}
		select {
		case <-ctx.Done():
			return nil
		case <-ticker.C:
		}
	}
}

func (s *Supervisor) reconcile(ctx context.Context) error {
	configured, err := s.store.ListEnabled(ctx)
	if err != nil {
		return err
	}
	desired := make(map[string]Server, len(configured))
	for _, server := range configured {
		key := serverKey(server.TenantID, server.Slug)
		desired[key] = server
		s.mu.RLock()
		managed := s.servers[key]
		s.mu.RUnlock()
		if managed == nil {
			s.mu.RLock()
			restarts := s.restarts[key]
			s.mu.RUnlock()
			if restarts >= 5 {
				if err := s.store.RecordHealth(ctx, server, "restart-exhausted", "degraded", restarts, "restart_exhausted"); err != nil {
					return err
				}
				continue
			}
			if err := s.start(ctx, key, server); err != nil {
				continue
			}
			continue
		}
		tools, err := managed.session.Refresh(ctx)
		if err != nil {
			s.remove(ctx, key, "health_check_failed")
			continue
		}
		digest, err := toolCatalogDigest(tools)
		if err != nil || digest != server.ExpectedCatalogDigest || managed.server.ConfigDigest != server.ConfigDigest {
			s.remove(ctx, key, "catalog_or_config_drift")
			continue
		}
		if err := s.store.RecordHealth(ctx, server, managed.instanceID, "healthy", managed.restartCount, ""); err != nil {
			return err
		}
	}
	s.mu.RLock()
	var stale []string
	for key := range s.servers {
		if _, exists := desired[key]; !exists {
			stale = append(stale, key)
		}
	}
	s.mu.RUnlock()
	for _, key := range stale {
		s.remove(ctx, key, "disabled")
	}
	return nil
}

func (s *Supervisor) start(ctx context.Context, key string, server Server) error {
	instanceID := fmt.Sprintf("mcp:%s:%d", server.ID, time.Now().UnixNano())
	s.mu.RLock()
	restartCount := s.restarts[key]
	s.mu.RUnlock()
	if err := s.store.RecordHealth(ctx, server, instanceID, "starting", restartCount, ""); err != nil {
		return err
	}
	session, tools, err := s.connect(ctx, server.Config)
	if err != nil {
		s.recordRestart(key)
		_ = s.store.RecordHealth(ctx, server, instanceID, "degraded", restartCount+1, "connect_failed")
		return err
	}
	digest, err := toolCatalogDigest(tools)
	if err != nil || digest != server.ExpectedCatalogDigest {
		_ = session.Close()
		s.recordRestart(key)
		_ = s.store.RecordHealth(ctx, server, instanceID, "degraded", restartCount+1, "catalog_mismatch")
		return errors.New("MCP tool catalog does not match configured digest")
	}
	managed := &managedServer{server: server, session: session, instanceID: instanceID, restartCount: restartCount}
	s.mu.Lock()
	s.servers[key] = managed
	s.mu.Unlock()
	if err := s.store.RecordHealth(ctx, server, instanceID, "healthy", restartCount, ""); err != nil {
		s.mu.Lock()
		delete(s.servers, key)
		s.mu.Unlock()
		_ = session.Close()
		return err
	}
	return nil
}

func (s *Supervisor) Call(ctx context.Context, tenantID, sourceID, toolName string, arguments map[string]any) (json.RawMessage, error) {
	key := serverKey(tenantID, sourceID)
	s.mu.RLock()
	managed := s.servers[key]
	s.mu.RUnlock()
	if managed == nil {
		return nil, errors.New("MCP server is not healthy")
	}
	result, err := managed.session.Call(ctx, toolName, arguments)
	if err != nil {
		return nil, fmt.Errorf("call MCP tool: %w", err)
	}
	return result, nil
}

func (s *Supervisor) remove(ctx context.Context, key, reason string) {
	s.mu.Lock()
	managed := s.servers[key]
	delete(s.servers, key)
	s.mu.Unlock()
	if managed == nil {
		return
	}
	_ = managed.session.Close()
	s.recordRestart(key)
	_ = s.store.RecordHealth(ctx, managed.server, managed.instanceID, "degraded", managed.restartCount+1, reason)
}

func (s *Supervisor) recordRestart(key string) {
	s.mu.Lock()
	s.restarts[key]++
	s.mu.Unlock()
}

func (s *Supervisor) closeAll() {
	s.mu.Lock()
	servers := s.servers
	s.servers = make(map[string]*managedServer)
	s.mu.Unlock()
	for _, managed := range servers {
		_ = managed.session.Close()
	}
}

func serverKey(tenantID, slug string) string { return tenantID + "\x00" + slug }

func DefaultConnector(ctx context.Context, config ServerConfig) (Session, []Tool, error) {
	return Start(ctx, config)
}
