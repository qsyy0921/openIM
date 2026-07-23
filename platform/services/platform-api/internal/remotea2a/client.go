package remotea2a

import (
	"bytes"
	"context"
	"crypto/sha256"
	"encoding/hex"
	"encoding/json"
	"errors"
	"fmt"
	"io"
	"net"
	"net/http"
	"net/url"
	"slices"
	"strings"
	"time"
)

const (
	ProtocolVersion = "1.0"
	RESTBinding     = "HTTP+JSON"
)

type AgentInterface struct {
	URL             string `json:"url"`
	ProtocolBinding string `json:"protocolBinding"`
	ProtocolVersion string `json:"protocolVersion"`
}

type AgentSkill struct {
	ID          string   `json:"id"`
	Name        string   `json:"name"`
	Description string   `json:"description"`
	Tags        []string `json:"tags"`
}

type AgentCard struct {
	ProtocolVersion     string           `json:"protocolVersion"`
	Name                string           `json:"name"`
	Description         string           `json:"description"`
	SupportedInterfaces []AgentInterface `json:"supportedInterfaces"`
	Skills              []AgentSkill     `json:"skills"`
}

type ResolvedCard struct {
	Card        AgentCard
	Raw         json.RawMessage
	Digest      string
	EndpointURL string
}

type SendResult struct {
	RemoteTaskID string
	State        string
	Raw          json.RawMessage
	Checksum     string
}

type Config struct {
	AllowedHosts        []string
	AllowedPrivateCIDRs []string
	Timeout             time.Duration
	HTTPClient          *http.Client
	AllowHTTPForTests   bool
}

type Client struct {
	httpClient      *http.Client
	allowedHosts    map[string]struct{}
	privateNetworks []*net.IPNet
	lookupSecret    func(string) (string, bool)
	allowHTTP       bool
}

func NewClient(config Config, lookupSecret func(string) (string, bool)) (*Client, error) {
	if config.Timeout <= 0 || config.Timeout > time.Minute || len(config.AllowedHosts) == 0 || lookupSecret == nil {
		return nil, errors.New("A2A client timeout, host allowlist, and secret resolver are required")
	}
	client := &Client{allowedHosts: make(map[string]struct{}, len(config.AllowedHosts)), lookupSecret: lookupSecret, allowHTTP: config.AllowHTTPForTests}
	for _, raw := range config.AllowedHosts {
		host := strings.ToLower(strings.TrimSpace(raw))
		if host == "" || strings.ContainsAny(host, "/:@") {
			return nil, errors.New("A2A allowed host is invalid")
		}
		client.allowedHosts[host] = struct{}{}
	}
	for _, raw := range config.AllowedPrivateCIDRs {
		_, network, err := net.ParseCIDR(strings.TrimSpace(raw))
		if err != nil {
			return nil, errors.New("A2A private CIDR allowlist is invalid")
		}
		client.privateNetworks = append(client.privateNetworks, network)
	}
	if config.HTTPClient != nil {
		client.httpClient = config.HTTPClient
	} else {
		transport := http.DefaultTransport.(*http.Transport).Clone()
		transport.Proxy = http.ProxyFromEnvironment
		transport.DialContext = client.dialContext
		client.httpClient = &http.Client{
			Timeout:   config.Timeout,
			Transport: transport,
			CheckRedirect: func(*http.Request, []*http.Request) error {
				return errors.New("A2A redirects are not allowed")
			},
		}
	}
	return client, nil
}

func (c *Client) ResolveCard(ctx context.Context, cardURL, expectedDigest, authEnvKey string) (ResolvedCard, error) {
	parsed, err := c.validateURL(cardURL)
	if err != nil {
		return ResolvedCard{}, err
	}
	request, err := http.NewRequestWithContext(ctx, http.MethodGet, parsed.String(), nil)
	if err != nil {
		return ResolvedCard{}, err
	}
	request.Header.Set("Accept", "application/json")
	if err := c.authorize(request, authEnvKey); err != nil {
		return ResolvedCard{}, err
	}
	response, err := c.httpClient.Do(request)
	if err != nil {
		return ResolvedCard{}, fmt.Errorf("fetch A2A Agent Card: %w", err)
	}
	defer response.Body.Close()
	if response.StatusCode != http.StatusOK {
		return ResolvedCard{}, fmt.Errorf("fetch A2A Agent Card: unexpected status %d", response.StatusCode)
	}
	raw, err := readBounded(response.Body, 256<<10)
	if err != nil {
		return ResolvedCard{}, err
	}
	canonical, err := canonicalJSON(raw)
	if err != nil {
		return ResolvedCard{}, errors.New("A2A Agent Card is malformed")
	}
	digest := digestBytes(canonical)
	if expectedDigest == "" || digest != expectedDigest {
		return ResolvedCard{}, errors.New("A2A Agent Card digest does not match the administrator pin")
	}
	var card AgentCard
	decoder := json.NewDecoder(bytes.NewReader(canonical))
	decoder.DisallowUnknownFields()
	if err := decoder.Decode(&card); err != nil {
		return ResolvedCard{}, errors.New("A2A Agent Card contains unsupported fields")
	}
	if card.ProtocolVersion != ProtocolVersion || strings.TrimSpace(card.Name) == "" || len(card.SupportedInterfaces) == 0 {
		return ResolvedCard{}, errors.New("A2A Agent Card version or identity is unsupported")
	}
	var endpoint string
	for _, item := range card.SupportedInterfaces {
		if item.ProtocolBinding == RESTBinding && item.ProtocolVersion == ProtocolVersion {
			endpoint = item.URL
			break
		}
	}
	if endpoint == "" {
		return ResolvedCard{}, errors.New("A2A Agent does not advertise the required HTTP+JSON 1.0 binding")
	}
	endpointURL, err := c.validateURL(endpoint)
	if err != nil {
		return ResolvedCard{}, fmt.Errorf("validate A2A endpoint: %w", err)
	}
	return ResolvedCard{Card: card, Raw: canonical, Digest: digest, EndpointURL: strings.TrimRight(endpointURL.String(), "/")}, nil
}

func (c *Client) SendMessage(ctx context.Context, endpointURL, messageID, task, idempotencyKey, authEnvKey string) (SendResult, error) {
	endpoint, err := c.validateURL(strings.TrimRight(endpointURL, "/") + "/message:send")
	if err != nil {
		return SendResult{}, err
	}
	if strings.TrimSpace(messageID) == "" || strings.TrimSpace(task) == "" || len([]rune(task)) > 4000 || strings.TrimSpace(idempotencyKey) == "" {
		return SendResult{}, errors.New("A2A message input is invalid")
	}
	body, err := json.Marshal(map[string]any{
		"message":       map[string]any{"messageId": messageID, "role": "ROLE_USER", "parts": []map[string]string{{"text": task}}},
		"configuration": map[string]any{"acceptedOutputModes": []string{"text/plain"}, "returnImmediately": false},
	})
	if err != nil {
		return SendResult{}, err
	}
	request, err := http.NewRequestWithContext(ctx, http.MethodPost, endpoint.String(), bytes.NewReader(body))
	if err != nil {
		return SendResult{}, err
	}
	request.Header.Set("Accept", "application/a2a+json")
	request.Header.Set("Content-Type", "application/a2a+json")
	request.Header.Set("Idempotency-Key", idempotencyKey)
	if err := c.authorize(request, authEnvKey); err != nil {
		return SendResult{}, err
	}
	response, err := c.httpClient.Do(request)
	if err != nil {
		return SendResult{}, fmt.Errorf("send A2A message: %w", err)
	}
	defer response.Body.Close()
	if response.StatusCode < 200 || response.StatusCode >= 300 {
		return SendResult{}, fmt.Errorf("send A2A message: unexpected status %d", response.StatusCode)
	}
	raw, err := readBounded(response.Body, 1<<20)
	if err != nil {
		return SendResult{}, err
	}
	canonical, err := canonicalJSON(raw)
	if err != nil {
		return SendResult{}, errors.New("A2A response is malformed")
	}
	var envelope struct {
		Task *struct {
			ID     string `json:"id"`
			Status struct {
				State string `json:"state"`
			} `json:"status"`
		} `json:"task"`
		Message json.RawMessage `json:"message"`
	}
	if err := json.Unmarshal(canonical, &envelope); err != nil {
		return SendResult{}, errors.New("A2A response envelope is malformed")
	}
	result := SendResult{Raw: canonical, Checksum: digestBytes(canonical), State: "completed"}
	if envelope.Task != nil {
		result.RemoteTaskID = strings.TrimSpace(envelope.Task.ID)
		if result.RemoteTaskID == "" {
			return SendResult{}, errors.New("A2A task response has no task ID")
		}
		result.State = mapTaskState(envelope.Task.Status.State)
		if result.State == "unsupported" {
			return SendResult{}, errors.New("A2A task response has an unsupported state")
		}
	} else if len(envelope.Message) == 0 || string(envelope.Message) == "null" {
		return SendResult{}, errors.New("A2A response contains neither a task nor a message")
	}
	return result, nil
}

func (c *Client) authorize(request *http.Request, authEnvKey string) error {
	authEnvKey = strings.TrimSpace(authEnvKey)
	if authEnvKey == "" {
		return nil
	}
	secret, ok := c.lookupSecret(authEnvKey)
	secret = strings.TrimSpace(secret)
	if !ok || secret == "" || strings.ContainsAny(secret, "\r\n") {
		return errors.New("A2A authentication secret is unavailable")
	}
	request.Header.Set("Authorization", "Bearer "+secret)
	return nil
}

func (c *Client) validateURL(raw string) (*url.URL, error) {
	parsed, err := url.Parse(strings.TrimSpace(raw))
	if err != nil || parsed.Host == "" || parsed.User != nil || parsed.Fragment != "" {
		return nil, errors.New("A2A URL is invalid")
	}
	if parsed.Scheme != "https" && !(c.allowHTTP && parsed.Scheme == "http") {
		return nil, errors.New("A2A URL must use HTTPS")
	}
	host := strings.ToLower(parsed.Hostname())
	if _, ok := c.allowedHosts[host]; !ok {
		return nil, errors.New("A2A URL host is not allowlisted")
	}
	return parsed, nil
}

func (c *Client) dialContext(ctx context.Context, network, address string) (net.Conn, error) {
	host, port, err := net.SplitHostPort(address)
	if err != nil {
		return nil, err
	}
	if _, ok := c.allowedHosts[strings.ToLower(host)]; !ok {
		return nil, errors.New("A2A dial host is not allowlisted")
	}
	addresses, err := net.DefaultResolver.LookupIPAddr(ctx, host)
	if err != nil {
		return nil, err
	}
	var lastErr error
	for _, address := range addresses {
		if !c.allowedIP(address.IP) {
			lastErr = errors.New("A2A dial resolved to a prohibited address")
			continue
		}
		connection, err := (&net.Dialer{}).DialContext(ctx, network, net.JoinHostPort(address.IP.String(), port))
		if err == nil {
			return connection, nil
		}
		lastErr = err
	}
	if lastErr == nil {
		lastErr = errors.New("A2A host resolved to no usable addresses")
	}
	return nil, lastErr
}

func (c *Client) allowedIP(ip net.IP) bool {
	if ip == nil || ip.IsUnspecified() || ip.IsMulticast() || ip.IsLinkLocalUnicast() || ip.IsLinkLocalMulticast() || ip.IsLoopback() || ip.IsPrivate() {
		return slices.ContainsFunc(c.privateNetworks, func(network *net.IPNet) bool { return network.Contains(ip) })
	}
	return true
}

func readBounded(reader io.Reader, limit int64) ([]byte, error) {
	limited := io.LimitReader(reader, limit+1)
	value, err := io.ReadAll(limited)
	if err != nil {
		return nil, err
	}
	if int64(len(value)) > limit {
		return nil, errors.New("A2A response exceeds the configured size limit")
	}
	return value, nil
}

func canonicalJSON(raw []byte) ([]byte, error) {
	var value any
	decoder := json.NewDecoder(bytes.NewReader(raw))
	decoder.UseNumber()
	if err := decoder.Decode(&value); err != nil {
		return nil, err
	}
	if decoder.Decode(&struct{}{}) != io.EOF {
		return nil, errors.New("multiple JSON values")
	}
	return json.Marshal(value)
}

func digestBytes(value []byte) string {
	digest := sha256.Sum256(value)
	return "sha256:" + hex.EncodeToString(digest[:])
}

func mapTaskState(value string) string {
	switch value {
	case "TASK_STATE_COMPLETED":
		return "completed"
	case "TASK_STATE_FAILED":
		return "failed"
	case "TASK_STATE_REJECTED", "TASK_STATE_CANCELED":
		return "rejected"
	case "TASK_STATE_SUBMITTED", "TASK_STATE_WORKING", "TASK_STATE_INPUT_REQUIRED", "TASK_STATE_AUTH_REQUIRED":
		return "submitted"
	default:
		return "unsupported"
	}
}
