package mcp

import (
	"bufio"
	"context"
	"encoding/json"
	"errors"
	"fmt"
	"io"
	"os"
	"os/exec"
	"path/filepath"
	"sort"
	"strings"
	"sync"
	"time"
)

const maxMessageBytes = 4 << 20

type ServerConfig struct {
	Name             string
	Command          string
	Arguments        []string
	WorkingDirectory string
	EnvironmentKeys  []string
	CallTimeout      time.Duration
}

func (c ServerConfig) Validate() error {
	if c.Name == "" || c.Name != strings.TrimSpace(c.Name) || c.Command == "" || c.Command != strings.TrimSpace(c.Command) {
		return errors.New("MCP server name and command are required")
	}
	if c.CallTimeout <= 0 || c.CallTimeout > 5*time.Minute {
		return errors.New("MCP call timeout is invalid")
	}
	if c.WorkingDirectory != "" && !filepath.IsAbs(c.WorkingDirectory) {
		return errors.New("MCP working directory must be absolute")
	}
	seen := make(map[string]struct{}, len(c.EnvironmentKeys))
	for _, key := range c.EnvironmentKeys {
		if key == "" || key != strings.TrimSpace(key) || strings.ContainsAny(key, "=\x00") {
			return errors.New("MCP environment key is invalid")
		}
		if _, duplicate := seen[key]; duplicate {
			return errors.New("MCP environment keys contain duplicates")
		}
		seen[key] = struct{}{}
	}
	return nil
}

type Tool struct {
	Name        string          `json:"name"`
	Description string          `json:"description"`
	InputSchema json.RawMessage `json:"inputSchema"`
}

type rpcResponse struct {
	JSONRPC string          `json:"jsonrpc"`
	ID      int64           `json:"id"`
	Result  json.RawMessage `json:"result"`
	Error   *struct {
		Code    int    `json:"code"`
		Message string `json:"message"`
	} `json:"error"`
}

type Client struct {
	config    ServerConfig
	process   *exec.Cmd
	stdin     io.WriteCloser
	responses chan rpcResponse
	readErr   chan error
	mu        sync.Mutex
	nextID    int64
	closed    bool
}

func Start(ctx context.Context, config ServerConfig) (*Client, []Tool, error) {
	if err := config.Validate(); err != nil {
		return nil, nil, err
	}
	command := exec.CommandContext(ctx, config.Command, config.Arguments...)
	command.Dir = config.WorkingDirectory
	command.Env = restrictedEnvironment(config.EnvironmentKeys)
	stdin, err := command.StdinPipe()
	if err != nil {
		return nil, nil, fmt.Errorf("open MCP stdin: %w", err)
	}
	stdout, err := command.StdoutPipe()
	if err != nil {
		return nil, nil, fmt.Errorf("open MCP stdout: %w", err)
	}
	stderr, err := command.StderrPipe()
	if err != nil {
		return nil, nil, fmt.Errorf("open MCP stderr: %w", err)
	}
	if err := command.Start(); err != nil {
		return nil, nil, fmt.Errorf("start MCP server: %w", err)
	}
	client := &Client{
		config: config, process: command, stdin: stdin,
		responses: make(chan rpcResponse, 8), readErr: make(chan error, 1), nextID: 1,
	}
	go client.readResponses(stdout)
	go func() { _, _ = io.Copy(io.Discard, io.LimitReader(stderr, 1<<20)) }()
	if _, err := client.request(ctx, "initialize", map[string]any{
		"protocolVersion": "2024-11-05",
		"capabilities":    map[string]any{"tools": map[string]any{}},
		"clientInfo":      map[string]string{"name": "openim-agent-runtime", "version": "1"},
	}); err != nil {
		_ = client.Close()
		return nil, nil, fmt.Errorf("initialize MCP server: %w", err)
	}
	if err := client.notify("notifications/initialized", map[string]any{}); err != nil {
		_ = client.Close()
		return nil, nil, err
	}
	result, err := client.request(ctx, "tools/list", map[string]any{})
	if err != nil {
		_ = client.Close()
		return nil, nil, fmt.Errorf("list MCP tools: %w", err)
	}
	var catalog struct {
		Tools []Tool `json:"tools"`
	}
	if err := json.Unmarshal(result, &catalog); err != nil {
		_ = client.Close()
		return nil, nil, fmt.Errorf("decode MCP tool catalog: %w", err)
	}
	for _, tool := range catalog.Tools {
		if tool.Name == "" || len(tool.InputSchema) == 0 {
			_ = client.Close()
			return nil, nil, errors.New("MCP server returned an invalid tool descriptor")
		}
	}
	sort.Slice(catalog.Tools, func(i, j int) bool { return catalog.Tools[i].Name < catalog.Tools[j].Name })
	return client, catalog.Tools, nil
}

func (c *Client) Call(ctx context.Context, toolName string, arguments map[string]any) (json.RawMessage, error) {
	if toolName == "" || arguments == nil {
		return nil, errors.New("MCP tool name and arguments are required")
	}
	return c.request(ctx, "tools/call", map[string]any{"name": toolName, "arguments": arguments})
}

func (c *Client) Refresh(ctx context.Context) ([]Tool, error) {
	result, err := c.request(ctx, "tools/list", map[string]any{})
	if err != nil {
		return nil, err
	}
	var catalog struct {
		Tools []Tool `json:"tools"`
	}
	if err := json.Unmarshal(result, &catalog); err != nil {
		return nil, fmt.Errorf("decode refreshed MCP tool catalog: %w", err)
	}
	sort.Slice(catalog.Tools, func(i, j int) bool { return catalog.Tools[i].Name < catalog.Tools[j].Name })
	return catalog.Tools, nil
}

func (c *Client) request(ctx context.Context, method string, params any) (json.RawMessage, error) {
	c.mu.Lock()
	defer c.mu.Unlock()
	if c.closed {
		return nil, errors.New("MCP client is closed")
	}
	id := c.nextID
	c.nextID++
	request := map[string]any{"jsonrpc": "2.0", "id": id, "method": method, "params": params}
	if err := c.write(request); err != nil {
		return nil, err
	}
	timeout := time.NewTimer(c.config.CallTimeout)
	defer timeout.Stop()
	for {
		select {
		case <-ctx.Done():
			return nil, ctx.Err()
		case <-timeout.C:
			return nil, fmt.Errorf("MCP %s timed out", method)
		case err := <-c.readErr:
			return nil, err
		case response := <-c.responses:
			if response.ID != id {
				return nil, errors.New("MCP response ID does not match serialized request")
			}
			if response.Error != nil {
				return nil, fmt.Errorf("MCP error %d: %s", response.Error.Code, response.Error.Message)
			}
			return response.Result, nil
		}
	}
}

func (c *Client) notify(method string, params any) error {
	c.mu.Lock()
	defer c.mu.Unlock()
	if c.closed {
		return errors.New("MCP client is closed")
	}
	return c.write(map[string]any{"jsonrpc": "2.0", "method": method, "params": params})
}

func (c *Client) write(value any) error {
	encoded, err := json.Marshal(value)
	if err != nil {
		return fmt.Errorf("encode MCP request: %w", err)
	}
	if len(encoded) > maxMessageBytes {
		return errors.New("MCP request exceeds 4 MiB")
	}
	encoded = append(encoded, '\n')
	if _, err := c.stdin.Write(encoded); err != nil {
		return fmt.Errorf("write MCP request: %w", err)
	}
	return nil
}

func (c *Client) readResponses(stdout io.Reader) {
	scanner := bufio.NewScanner(stdout)
	scanner.Buffer(make([]byte, 64<<10), maxMessageBytes)
	for scanner.Scan() {
		line := scanner.Bytes()
		if len(strings.TrimSpace(string(line))) == 0 {
			continue
		}
		var response rpcResponse
		if err := json.Unmarshal(line, &response); err != nil || response.JSONRPC != "2.0" || response.ID <= 0 {
			c.reportReadError(errors.New("MCP server returned an invalid JSON-RPC response"))
			return
		}
		c.responses <- response
	}
	if err := scanner.Err(); err != nil {
		c.reportReadError(fmt.Errorf("read MCP response: %w", err))
		return
	}
	c.reportReadError(errors.New("MCP server closed stdout"))
}

func (c *Client) reportReadError(err error) {
	select {
	case c.readErr <- err:
	default:
	}
}

func (c *Client) Close() error {
	c.mu.Lock()
	if c.closed {
		c.mu.Unlock()
		return nil
	}
	c.closed = true
	_ = c.stdin.Close()
	process := c.process
	c.mu.Unlock()
	done := make(chan error, 1)
	go func() { done <- process.Wait() }()
	select {
	case err := <-done:
		return err
	case <-time.After(5 * time.Second):
		if err := process.Process.Kill(); err != nil {
			return fmt.Errorf("kill MCP server: %w", err)
		}
		<-done
		return nil
	}
}

func restrictedEnvironment(extraKeys []string) []string {
	keys := []string{"PATH", "Path", "SYSTEMROOT", "SystemRoot", "HOME", "USERPROFILE", "TMP", "TEMP"}
	keys = append(keys, extraKeys...)
	seen := make(map[string]struct{}, len(keys))
	result := make([]string, 0, len(keys))
	for _, key := range keys {
		normalized := strings.ToUpper(key)
		if _, duplicate := seen[normalized]; duplicate {
			continue
		}
		seen[normalized] = struct{}{}
		if value, ok := os.LookupEnv(key); ok {
			result = append(result, key+"="+value)
		}
	}
	return result
}
