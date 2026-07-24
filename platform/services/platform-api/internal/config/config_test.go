package config

import (
	"strings"
	"testing"
	"time"
)

func TestLoad(t *testing.T) {
	values := validValues()

	cfg, err := Load(mapLookup(values))
	if err != nil {
		t.Fatalf("Load() error = %v", err)
	}
	if cfg.HTTPAddr != values[httpAddrKey] {
		t.Fatalf("HTTPAddr = %q", cfg.HTTPAddr)
	}
	if cfg.Version != values[versionKey] {
		t.Fatalf("Version = %q", cfg.Version)
	}
	if cfg.ShutdownTimeout != 5*time.Second {
		t.Fatalf("ShutdownTimeout = %v", cfg.ShutdownTimeout)
	}
	if cfg.DependencyTimeout != 3*time.Second {
		t.Fatalf("DependencyTimeout = %v", cfg.DependencyTimeout)
	}
	if cfg.OpenIMAPIURL != "http://127.0.0.1:10002" {
		t.Fatalf("OpenIMAPIURL = %q", cfg.OpenIMAPIURL)
	}
}

func TestLoadRequiresEveryValue(t *testing.T) {
	base := validValues()

	for _, key := range []string{
		httpAddrKey, versionKey, shutdownTimeoutKey, dependencyTimeoutKey,
		databaseURLKey, oidcIssuerKey, oidcAudienceKey, openIMAPIURLKey,
		openIMWSURLKey, openIMSecretKey, openIMAdminUserKey, knowledgeMinIOURLKey,
		knowledgeMinIOAccessKey, knowledgeMinIOSecretKey, knowledgeMinIOBucketKey,
		knowledgeParserRevisionKey, knowledgeMaxAttemptsKey, retrievalEmbeddingModelKey,
		retrievalEmbeddingDimensionKey, retrievalProjectionRevisionKey,
	} {
		t.Run(key, func(t *testing.T) {
			values := make(map[string]string, len(base))
			for k, value := range base {
				values[k] = value
			}
			delete(values, key)

			_, err := Load(mapLookup(values))
			if err == nil || !strings.Contains(err.Error(), key) {
				t.Fatalf("Load() error = %v, want missing %s", err, key)
			}
		})
	}
}

func TestLoadRejectsInvalidValues(t *testing.T) {
	tests := []struct {
		name   string
		key    string
		value  string
		needle string
	}{
		{name: "address", key: httpAddrKey, value: "not-an-address", needle: httpAddrKey},
		{name: "port", key: httpAddrKey, value: "127.0.0.1:70000", needle: httpAddrKey},
		{name: "duration", key: shutdownTimeoutKey, value: "later", needle: shutdownTimeoutKey},
		{name: "non-positive duration", key: shutdownTimeoutKey, value: "0s", needle: shutdownTimeoutKey},
		{name: "dependency duration", key: dependencyTimeoutKey, value: "0s", needle: dependencyTimeoutKey},
		{name: "OIDC URL", key: oidcIssuerKey, value: "ftp://issuer.example", needle: oidcIssuerKey},
		{name: "OpenIM API URL", key: openIMAPIURLKey, value: "ws://127.0.0.1", needle: openIMAPIURLKey},
		{name: "OpenIM WS URL", key: openIMWSURLKey, value: "http://127.0.0.1", needle: openIMWSURLKey},
		{name: "retrieval projection", key: retrievalProjectionRevisionKey, value: "chunk-content-v1", needle: retrievalProjectionRevisionKey},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			values := validValues()
			values[tt.key] = tt.value

			_, err := Load(mapLookup(values))
			if err == nil || !strings.Contains(err.Error(), tt.needle) {
				t.Fatalf("Load() error = %v, want %q", err, tt.needle)
			}
		})
	}
}

func validValues() map[string]string {
	return map[string]string{
		httpAddrKey:                    "127.0.0.1:18080",
		versionKey:                     "test-version",
		shutdownTimeoutKey:             "5s",
		dependencyTimeoutKey:           "3s",
		databaseURLKey:                 "postgres://platform:secret@127.0.0.1/platform",
		oidcIssuerKey:                  "https://identity.example.test/realms/platform/",
		oidcAudienceKey:                "platform-api",
		openIMAPIURLKey:                "http://127.0.0.1:10002/",
		openIMWSURLKey:                 "ws://127.0.0.1:10001",
		openIMSecretKey:                "test-only-secret",
		openIMAdminUserKey:             "imAdmin",
		knowledgeMinIOURLKey:           "http://127.0.0.1:12005",
		knowledgeMinIOAccessKey:        "test-access",
		knowledgeMinIOSecretKey:        "test-secret",
		knowledgeMinIOBucketKey:        "enterprise-knowledge",
		knowledgeParserRevisionKey:     "openim-knowledge-parser-v1",
		knowledgeMaxAttemptsKey:        "3",
		retrievalEmbeddingModelKey:     "qwen3-embedding:4b",
		retrievalEmbeddingDimensionKey: "2560",
		retrievalProjectionRevisionKey: "document-title-content-v1",
	}
}

func mapLookup(values map[string]string) LookupEnv {
	return func(key string) (string, bool) {
		value, ok := values[key]
		return value, ok
	}
}
