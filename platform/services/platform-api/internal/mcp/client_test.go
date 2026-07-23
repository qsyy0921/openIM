package mcp

import (
	"bufio"
	"context"
	"encoding/json"
	"fmt"
	"os"
	"testing"
	"time"
)

func TestClientInitializesListsAndCallsToolWithRestrictedEnvironment(t *testing.T) {
	t.Setenv("MCP_HELPER_PROCESS", "1")
	t.Setenv("MCP_ALLOWED_VALUE", "allowed")
	t.Setenv("MCP_SECRET_SHOULD_NOT_LEAK", "secret")
	ctx, cancel := context.WithCancel(context.Background())
	defer cancel()
	client, tools, err := Start(ctx, ServerConfig{
		Name: "test", Command: os.Args[0], Arguments: []string{"-test.run=TestMCPHelperProcess", "--"},
		EnvironmentKeys: []string{"MCP_HELPER_PROCESS", "MCP_ALLOWED_VALUE"}, CallTimeout: 3 * time.Second,
	})
	if err != nil {
		t.Fatal(err)
	}
	defer func() { _ = client.Close() }()
	if len(tools) != 1 || tools[0].Name != "echo" || tools[0].Description != "allowed=allowed secret=" {
		t.Fatalf("tools = %#v", tools)
	}
	result, err := client.Call(ctx, "echo", map[string]any{"value": "hello"})
	if err != nil {
		t.Fatal(err)
	}
	var response struct {
		Content []struct {
			Type string `json:"type"`
			Text string `json:"text"`
		} `json:"content"`
	}
	if err := json.Unmarshal(result, &response); err != nil || len(response.Content) != 1 || response.Content[0].Text != "hello" {
		t.Fatalf("result = %s, err=%v", result, err)
	}
}

func TestServerConfigRejectsRelativeWorkingDirectoryAndDuplicateEnv(t *testing.T) {
	if err := (ServerConfig{Name: "x", Command: "x", WorkingDirectory: "relative", CallTimeout: time.Second}).Validate(); err == nil {
		t.Fatal("relative MCP working directory was accepted")
	}
	if err := (ServerConfig{Name: "x", Command: "x", EnvironmentKeys: []string{"TOKEN", "TOKEN"}, CallTimeout: time.Second}).Validate(); err == nil {
		t.Fatal("duplicate MCP environment keys were accepted")
	}
}

func TestMCPHelperProcess(t *testing.T) {
	if os.Getenv("MCP_HELPER_PROCESS") != "1" {
		return
	}
	scanner := bufio.NewScanner(os.Stdin)
	for scanner.Scan() {
		var request struct {
			ID     int64           `json:"id"`
			Method string          `json:"method"`
			Params json.RawMessage `json:"params"`
		}
		if err := json.Unmarshal(scanner.Bytes(), &request); err != nil {
			os.Exit(2)
		}
		if request.ID == 0 {
			continue
		}
		var result any
		switch request.Method {
		case "initialize":
			result = map[string]any{"protocolVersion": "2024-11-05", "capabilities": map[string]any{}}
		case "tools/list":
			result = map[string]any{"tools": []map[string]any{{
				"name": "echo", "description": fmt.Sprintf("allowed=%s secret=%s", os.Getenv("MCP_ALLOWED_VALUE"), os.Getenv("MCP_SECRET_SHOULD_NOT_LEAK")),
				"inputSchema": map[string]any{"type": "object"},
			}}}
		case "tools/call":
			var params struct {
				Arguments map[string]any `json:"arguments"`
			}
			_ = json.Unmarshal(request.Params, &params)
			result = map[string]any{"content": []map[string]any{{"type": "text", "text": params.Arguments["value"]}}}
		default:
			os.Exit(3)
		}
		response := map[string]any{"jsonrpc": "2.0", "id": request.ID, "result": result}
		encoded, _ := json.Marshal(response)
		_, _ = fmt.Fprintln(os.Stdout, string(encoded))
	}
	os.Exit(0)
}
