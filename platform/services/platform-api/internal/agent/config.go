package agent

import (
	"crypto/tls"
	"crypto/x509"
	"errors"
	"fmt"
	"net"
	"net/url"
	"os"
	"strconv"
	"strings"
	"time"

	"github.com/IBM/sarama"
)

type Config struct {
	DatabaseURL, EventTopic, ConsumerGroup     string
	OpenIMAPIURL, OpenIMSecret                 string
	OpenIMAdminUserID                          string
	Brokers                                    []string
	TLS                                        *tls.Config
	DependencyTimeout, Poll, Lease             time.Duration
	ToolApprovalTTL                            time.Duration
	MCPReconcileInterval                       time.Duration
	MaxAttempts                                int
	IntelligenceURL                            string
	RetrievalIntelligenceURL                   string
	RetrievalModelRevision                     string
	RetrievalRerankerModel                     string
	RetrievalRerankerRevision                  string
	RetrievalDimension, RetrievalMaxCandidates int
	RetrievalHNSWEFSearch                      int
	RetrievalDenseMinSimilarity                float64
	A2AAllowedHosts, A2AAllowedPrivateCIDRs    []string
	A2ATimeout                                 time.Duration
}

func LoadConfig() (Config, error) {
	database, err := env("PLATFORM_DATABASE_URL")
	if err != nil {
		return Config{}, err
	}
	brokersRaw, err := env("PLATFORM_KAFKA_BROKERS")
	if err != nil {
		return Config{}, err
	}
	brokers := strings.Split(brokersRaw, ",")
	for i := range brokers {
		brokers[i] = strings.TrimSpace(brokers[i])
		if _, _, err := net.SplitHostPort(brokers[i]); err != nil {
			return Config{}, fmt.Errorf("invalid Kafka broker %q", brokers[i])
		}
	}
	topic, err := env("PLATFORM_EVENT_TOPIC")
	if err != nil {
		return Config{}, err
	}
	group, err := env("PLATFORM_AGENT_CONSUMER_GROUP")
	if err != nil {
		return Config{}, err
	}
	dependency, err := durationEnv("PLATFORM_DEPENDENCY_TIMEOUT")
	if err != nil {
		return Config{}, err
	}
	poll, err := durationEnv("PLATFORM_AGENT_POLL_INTERVAL")
	if err != nil {
		return Config{}, err
	}
	lease, err := durationEnv("PLATFORM_AGENT_LEASE")
	if err != nil {
		return Config{}, err
	}
	maxRaw, err := env("PLATFORM_AGENT_MAX_ATTEMPTS")
	if err != nil {
		return Config{}, err
	}
	maxAttempts, err := strconv.Atoi(maxRaw)
	if err != nil || maxAttempts < 1 || maxAttempts > 10 {
		return Config{}, errors.New("PLATFORM_AGENT_MAX_ATTEMPTS must be between 1 and 10")
	}
	intelligence, err := httpURL("PLATFORM_INTELLIGENCE_URL")
	if err != nil {
		return Config{}, err
	}
	retrievalIntelligence, err := httpURL("PLATFORM_RETRIEVAL_INTELLIGENCE_URL")
	if err != nil {
		return Config{}, err
	}
	intelligence = strings.TrimRight(intelligence, "/")
	retrievalIntelligence = strings.TrimRight(retrievalIntelligence, "/")
	if intelligence == retrievalIntelligence {
		return Config{}, errors.New("generation and retrieval intelligence URLs must be separate")
	}
	openIMAPIURL, err := httpURL("PLATFORM_OPENIM_API_URL")
	if err != nil {
		return Config{}, err
	}
	openIMSecret, err := env("PLATFORM_OPENIM_SECRET")
	if err != nil {
		return Config{}, err
	}
	openIMAdminUserID, err := env("PLATFORM_OPENIM_ADMIN_USER_ID")
	if err != nil {
		return Config{}, err
	}
	toolApprovalTTL, err := durationEnv("PLATFORM_TOOL_APPROVAL_TTL")
	if err != nil {
		return Config{}, err
	}
	mcpReconcile, err := durationEnv("PLATFORM_MCP_RECONCILE_INTERVAL")
	if err != nil {
		return Config{}, err
	}
	retrievalModel, err := env("PLATFORM_RETRIEVAL_EMBEDDING_MODEL")
	if err != nil {
		return Config{}, err
	}
	retrievalDimension, err := intEnv("PLATFORM_RETRIEVAL_EMBEDDING_DIMENSION", 8, 8192)
	if err != nil {
		return Config{}, err
	}
	retrievalCandidates, err := intEnv("PLATFORM_RETRIEVAL_MAX_CANDIDATES", 1, 10000)
	if err != nil {
		return Config{}, err
	}
	retrievalRerankerModel, err := env("PLATFORM_RETRIEVAL_RERANKER_MODEL")
	if err != nil {
		return Config{}, err
	}
	retrievalRerankerRevision, err := env("PLATFORM_RETRIEVAL_RERANKER_REVISION")
	if err != nil {
		return Config{}, err
	}
	retrievalHNSWEFSearch, err := intEnv("PLATFORM_RETRIEVAL_HNSW_EF_SEARCH", 32, 1000)
	if err != nil {
		return Config{}, err
	}
	retrievalDenseMinimum, err := floatEnv("PLATFORM_RETRIEVAL_DENSE_MIN_SIMILARITY", -1, 1)
	if err != nil {
		return Config{}, err
	}
	a2aHosts := csvEnv("PLATFORM_A2A_ALLOWED_HOSTS")
	var a2aTimeout time.Duration
	if len(a2aHosts) > 0 {
		a2aTimeout, err = durationEnv("PLATFORM_A2A_TIMEOUT")
		if err != nil {
			return Config{}, err
		}
	}
	tlsConfig, err := kafkaTLS()
	if err != nil {
		return Config{}, err
	}
	return Config{DatabaseURL: database, Brokers: brokers, EventTopic: topic, ConsumerGroup: group, TLS: tlsConfig,
		DependencyTimeout: dependency, Poll: poll, Lease: lease, MaxAttempts: maxAttempts,
		IntelligenceURL:          intelligence,
		RetrievalIntelligenceURL: retrievalIntelligence,
		ToolApprovalTTL:          toolApprovalTTL,
		MCPReconcileInterval:     mcpReconcile, OpenIMAPIURL: strings.TrimRight(openIMAPIURL, "/"),
		OpenIMSecret: openIMSecret, OpenIMAdminUserID: openIMAdminUserID,
		RetrievalModelRevision: retrievalModel, RetrievalDimension: retrievalDimension,
		RetrievalRerankerModel: retrievalRerankerModel, RetrievalRerankerRevision: retrievalRerankerRevision,
		RetrievalMaxCandidates: retrievalCandidates, RetrievalHNSWEFSearch: retrievalHNSWEFSearch,
		RetrievalDenseMinSimilarity: retrievalDenseMinimum,
		A2AAllowedHosts:             a2aHosts, A2AAllowedPrivateCIDRs: csvEnv("PLATFORM_A2A_ALLOWED_PRIVATE_CIDRS"), A2ATimeout: a2aTimeout}, nil
}

func (c Config) SaramaConfig() *sarama.Config {
	config := sarama.NewConfig()
	config.Version = sarama.V3_5_1_0
	config.ClientID = "openim-agent-runtime"
	config.Consumer.Offsets.Initial = sarama.OffsetNewest
	config.Consumer.Offsets.AutoCommit.Enable = false
	config.Consumer.Return.Errors = true
	if c.TLS != nil {
		config.Net.TLS.Enable = true
		config.Net.TLS.Config = c.TLS
	}
	return config
}

func env(key string) (string, error) {
	value := strings.TrimSpace(os.Getenv(key))
	if value == "" {
		return "", fmt.Errorf("required environment variable %s is missing", key)
	}
	return value, nil
}
func durationEnv(key string) (time.Duration, error) {
	value, err := env(key)
	if err != nil {
		return 0, err
	}
	d, err := time.ParseDuration(value)
	if err != nil || d <= 0 {
		return 0, fmt.Errorf("%s must be a positive duration", key)
	}
	return d, nil
}
func intEnv(key string, minimum, maximum int) (int, error) {
	raw, err := env(key)
	if err != nil {
		return 0, err
	}
	value, err := strconv.Atoi(raw)
	if err != nil || value < minimum || value > maximum {
		return 0, fmt.Errorf("%s must be between %d and %d", key, minimum, maximum)
	}
	return value, nil
}
func floatEnv(key string, minimum, maximum float64) (float64, error) {
	raw, err := env(key)
	if err != nil {
		return 0, err
	}
	value, err := strconv.ParseFloat(raw, 64)
	if err != nil || value < minimum || value > maximum {
		return 0, fmt.Errorf("%s must be between %g and %g", key, minimum, maximum)
	}
	return value, nil
}
func httpURL(key string) (string, error) {
	value, err := env(key)
	if err != nil {
		return "", err
	}
	parsed, err := url.Parse(value)
	if err != nil || parsed.Host == "" || (parsed.Scheme != "http" && parsed.Scheme != "https") {
		return "", fmt.Errorf("%s must be an HTTP(S) URL", key)
	}
	return value, nil
}

func csvEnv(key string) []string {
	raw := strings.TrimSpace(os.Getenv(key))
	if raw == "" {
		return nil
	}
	values := strings.Split(raw, ",")
	result := make([]string, 0, len(values))
	for _, value := range values {
		if value = strings.TrimSpace(value); value != "" {
			result = append(result, value)
		}
	}
	return result
}

func kafkaTLS() (*tls.Config, error) {
	raw, err := env("PLATFORM_KAFKA_TLS_ENABLED")
	if err != nil {
		return nil, err
	}
	enabled, err := strconv.ParseBool(raw)
	if err != nil {
		return nil, errors.New("PLATFORM_KAFKA_TLS_ENABLED must be true or false")
	}
	if !enabled {
		return nil, nil
	}
	caPath, err := env("PLATFORM_KAFKA_TLS_CA")
	if err != nil {
		return nil, err
	}
	pem, err := os.ReadFile(caPath)
	if err != nil {
		return nil, fmt.Errorf("read Kafka CA: %w", err)
	}
	roots := x509.NewCertPool()
	if !roots.AppendCertsFromPEM(pem) {
		return nil, errors.New("Kafka CA contains no certificates")
	}
	config := &tls.Config{MinVersion: tls.VersionTLS12, RootCAs: roots}
	certPath, keyPath := strings.TrimSpace(os.Getenv("PLATFORM_KAFKA_TLS_CERT")), strings.TrimSpace(os.Getenv("PLATFORM_KAFKA_TLS_KEY"))
	if (certPath == "") != (keyPath == "") {
		return nil, errors.New("Kafka client certificate and key must be set together")
	}
	if certPath != "" {
		cert, err := tls.LoadX509KeyPair(certPath, keyPath)
		if err != nil {
			return nil, err
		}
		config.Certificates = []tls.Certificate{cert}
	}
	return config, nil
}
