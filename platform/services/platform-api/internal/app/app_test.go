package app

import (
	"context"
	"encoding/json"
	"net"
	"net/http"
	"testing"
	"time"

	"github.com/qsyy0921/openim/platform/services/platform-api/internal/action"
	"github.com/qsyy0921/openim/platform/services/platform-api/internal/agent"
	"github.com/qsyy0921/openim/platform/services/platform-api/internal/config"
	"github.com/qsyy0921/openim/platform/services/platform-api/internal/httpserver"
	"github.com/qsyy0921/openim/platform/services/platform-api/internal/identity"
)

func TestRunServesHealthAndShutsDown(t *testing.T) {
	listener, err := net.Listen("tcp", "127.0.0.1:0")
	if err != nil {
		t.Fatalf("listen: %v", err)
	}

	ctx, cancel := context.WithCancel(context.Background())
	done := make(chan error, 1)
	go func() {
		done <- Run(ctx, config.Config{
			Version:         "test-version",
			ShutdownTimeout: time.Second,
		}, listener, httpserver.NewHandler("test-version", sessionStub{}, deviceStub{}, approvalStub{}, agentWorkspaceStub{}))
	}()

	client := &http.Client{Timeout: time.Second}
	var response *http.Response
	for deadline := time.Now().Add(2 * time.Second); time.Now().Before(deadline); {
		response, err = client.Get("http://" + listener.Addr().String() + "/healthz")
		if err == nil {
			break
		}
		time.Sleep(10 * time.Millisecond)
	}
	if err != nil {
		cancel()
		t.Fatalf("GET /healthz: %v", err)
	}
	defer response.Body.Close()

	var body map[string]string
	if err := json.NewDecoder(response.Body).Decode(&body); err != nil {
		cancel()
		t.Fatalf("decode health: %v", err)
	}
	if body["status"] != "ready" || body["version"] != "test-version" {
		cancel()
		t.Fatalf("health body = %#v", body)
	}

	cancel()
	select {
	case err := <-done:
		if err != nil {
			t.Fatalf("Run() error = %v", err)
		}
	case <-time.After(2 * time.Second):
		t.Fatal("Run() did not stop after cancellation")
	}
}

type sessionStub struct{}
type deviceStub struct{}
type approvalStub struct{}
type agentWorkspaceStub struct{}

func (agentWorkspaceStub) Get(context.Context, string, string, int32) (agent.Workspace, error) {
	return agent.Workspace{}, nil
}

func (approvalStub) Approve(context.Context, string, string, int32, string, string) (action.ApprovalResult, error) {
	return action.ApprovalResult{}, nil
}

func (sessionStub) CreateSession(context.Context, string, string, int32) (identity.Session, error) {
	return identity.Session{}, nil
}

func (deviceStub) List(context.Context, string, string, int32) (identity.DeviceSnapshot, error) {
	return identity.DeviceSnapshot{}, nil
}

func (deviceStub) LogoutPlatform(context.Context, string, string, int32, int32) error {
	return nil
}
