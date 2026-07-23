package agent

import (
	"strings"
	"testing"
)

func TestLoadConfigRequiresSeparateRetrievalEndpoint(t *testing.T) {
	setAgentConfigEnvironment(t)
	config, err := LoadConfig()
	if err != nil {
		t.Fatal(err)
	}
	if config.IntelligenceURL != "http://127.0.0.1:18082" {
		t.Fatalf("generation intelligence URL = %q", config.IntelligenceURL)
	}
	if config.RetrievalIntelligenceURL != "http://127.0.0.1:18083" {
		t.Fatalf("retrieval intelligence URL = %q", config.RetrievalIntelligenceURL)
	}

	t.Setenv("PLATFORM_RETRIEVAL_INTELLIGENCE_URL", "")
	if _, err := LoadConfig(); err == nil ||
		!strings.Contains(err.Error(), "PLATFORM_RETRIEVAL_INTELLIGENCE_URL") {
		t.Fatalf("missing retrieval endpoint error = %v", err)
	}

	t.Setenv("PLATFORM_RETRIEVAL_INTELLIGENCE_URL", "http://127.0.0.1:18082/")
	if _, err := LoadConfig(); err == nil || !strings.Contains(err.Error(), "must be separate") {
		t.Fatalf("shared generation/retrieval endpoint error = %v", err)
	}
}

func setAgentConfigEnvironment(t *testing.T) {
	t.Helper()
	values := map[string]string{
		"PLATFORM_DATABASE_URL":                   "postgres://platform@127.0.0.1/platform",
		"PLATFORM_KAFKA_BROKERS":                  "127.0.0.1:9092",
		"PLATFORM_EVENT_TOPIC":                    "platform.events",
		"PLATFORM_AGENT_CONSUMER_GROUP":           "agent-runtime-test",
		"PLATFORM_DEPENDENCY_TIMEOUT":             "30s",
		"PLATFORM_AGENT_POLL_INTERVAL":            "100ms",
		"PLATFORM_AGENT_LEASE":                    "30s",
		"PLATFORM_AGENT_MAX_ATTEMPTS":             "3",
		"PLATFORM_INTELLIGENCE_URL":               "http://127.0.0.1:18082/",
		"PLATFORM_RETRIEVAL_INTELLIGENCE_URL":     "http://127.0.0.1:18083/",
		"PLATFORM_OPENIM_API_URL":                 "http://127.0.0.1:12002",
		"PLATFORM_OPENIM_SECRET":                  "test-secret",
		"PLATFORM_OPENIM_ADMIN_USER_ID":           "imAdmin",
		"PLATFORM_TOOL_APPROVAL_TTL":              "5m",
		"PLATFORM_MCP_RECONCILE_INTERVAL":         "5s",
		"PLATFORM_RETRIEVAL_EMBEDDING_MODEL":      "qwen3-embedding:4b",
		"PLATFORM_RETRIEVAL_EMBEDDING_DIMENSION":  "2560",
		"PLATFORM_RETRIEVAL_MAX_CANDIDATES":       "32",
		"PLATFORM_RETRIEVAL_RERANKER_MODEL":       "BAAI/bge-reranker-v2-m3",
		"PLATFORM_RETRIEVAL_RERANKER_REVISION":    "953dc6f6f85a1b2dbfca4c34a2796e7dde08d41e",
		"PLATFORM_RETRIEVAL_HNSW_EF_SEARCH":       "100",
		"PLATFORM_RETRIEVAL_DENSE_MIN_SIMILARITY": "0.45",
		"PLATFORM_KAFKA_TLS_ENABLED":              "false",
	}
	for key, value := range values {
		t.Setenv(key, value)
	}
}
