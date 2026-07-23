package config

import (
	"errors"
	"fmt"
	"net"
	"net/url"
	"os"
	"strconv"
	"strings"
	"time"
)

const (
	httpAddrKey                    = "PLATFORM_HTTP_ADDR"
	versionKey                     = "PLATFORM_VERSION"
	shutdownTimeoutKey             = "PLATFORM_SHUTDOWN_TIMEOUT"
	databaseURLKey                 = "PLATFORM_DATABASE_URL"
	oidcIssuerKey                  = "PLATFORM_OIDC_ISSUER"
	oidcAudienceKey                = "PLATFORM_OIDC_AUDIENCE"
	openIMAPIURLKey                = "PLATFORM_OPENIM_API_URL"
	openIMWSURLKey                 = "PLATFORM_OPENIM_WS_URL"
	openIMSecretKey                = "PLATFORM_OPENIM_SECRET"
	openIMAdminUserKey             = "PLATFORM_OPENIM_ADMIN_USER_ID"
	dependencyTimeoutKey           = "PLATFORM_DEPENDENCY_TIMEOUT"
	knowledgeMinIOURLKey           = "PLATFORM_KNOWLEDGE_MINIO_URL"
	knowledgeMinIOAccessKey        = "PLATFORM_KNOWLEDGE_MINIO_ACCESS_KEY"
	knowledgeMinIOSecretKey        = "PLATFORM_KNOWLEDGE_MINIO_SECRET_KEY"
	knowledgeMinIOBucketKey        = "PLATFORM_KNOWLEDGE_MINIO_BUCKET"
	knowledgeParserRevisionKey     = "PLATFORM_KNOWLEDGE_PARSER_REVISION"
	knowledgeMaxAttemptsKey        = "PLATFORM_KNOWLEDGE_INGESTION_MAX_ATTEMPTS"
	retrievalEmbeddingModelKey     = "PLATFORM_RETRIEVAL_EMBEDDING_MODEL"
	retrievalEmbeddingDimensionKey = "PLATFORM_RETRIEVAL_EMBEDDING_DIMENSION"
)

type Config struct {
	HTTPAddr                    string
	Version                     string
	ShutdownTimeout             time.Duration
	DependencyTimeout           time.Duration
	DatabaseURL                 string
	OIDCIssuer                  string
	OIDCAudience                string
	OpenIMAPIURL                string
	OpenIMWSURL                 string
	OpenIMSecret                string
	OpenIMAdminUserID           string
	KnowledgeMinIOURL           string
	KnowledgeMinIOAccessKey     string
	KnowledgeMinIOSecretKey     string
	KnowledgeMinIOBucket        string
	KnowledgeParserRevision     string
	KnowledgeMaxAttempts        int
	RetrievalEmbeddingModel     string
	RetrievalEmbeddingDimension int
	A2AAllowedHosts             []string
	A2AAllowedPrivateCIDRs      []string
	A2ATimeout                  time.Duration
}

type LookupEnv func(string) (string, bool)

func LoadFromEnv() (Config, error) {
	return Load(os.LookupEnv)
}

func Load(lookup LookupEnv) (Config, error) {
	httpAddr, err := required(lookup, httpAddrKey)
	if err != nil {
		return Config{}, err
	}
	if err := validateAddress(httpAddr); err != nil {
		return Config{}, fmt.Errorf("%s: %w", httpAddrKey, err)
	}

	version, err := required(lookup, versionKey)
	if err != nil {
		return Config{}, err
	}

	shutdownValue, err := required(lookup, shutdownTimeoutKey)
	if err != nil {
		return Config{}, err
	}
	shutdownTimeout, err := time.ParseDuration(shutdownValue)
	if err != nil {
		return Config{}, fmt.Errorf("%s: parse duration: %w", shutdownTimeoutKey, err)
	}
	if shutdownTimeout <= 0 {
		return Config{}, fmt.Errorf("%s: must be greater than zero", shutdownTimeoutKey)
	}

	dependencyTimeout, err := requiredDuration(lookup, dependencyTimeoutKey)
	if err != nil {
		return Config{}, err
	}
	databaseURL, err := required(lookup, databaseURLKey)
	if err != nil {
		return Config{}, err
	}
	oidcIssuer, err := requiredURL(lookup, oidcIssuerKey, "https", "http")
	if err != nil {
		return Config{}, err
	}
	oidcAudience, err := required(lookup, oidcAudienceKey)
	if err != nil {
		return Config{}, err
	}
	openIMAPIURL, err := requiredURL(lookup, openIMAPIURLKey, "http", "https")
	if err != nil {
		return Config{}, err
	}
	openIMWSURL, err := requiredURL(lookup, openIMWSURLKey, "ws", "wss")
	if err != nil {
		return Config{}, err
	}
	openIMSecret, err := required(lookup, openIMSecretKey)
	if err != nil {
		return Config{}, err
	}
	openIMAdminUserID, err := required(lookup, openIMAdminUserKey)
	if err != nil {
		return Config{}, err
	}
	knowledgeMinIOURL, err := requiredURL(lookup, knowledgeMinIOURLKey, "http", "https")
	if err != nil {
		return Config{}, err
	}
	knowledgeMinIOAccess, err := required(lookup, knowledgeMinIOAccessKey)
	if err != nil {
		return Config{}, err
	}
	knowledgeMinIOSecret, err := required(lookup, knowledgeMinIOSecretKey)
	if err != nil {
		return Config{}, err
	}
	knowledgeMinIOBucket, err := required(lookup, knowledgeMinIOBucketKey)
	if err != nil {
		return Config{}, err
	}
	if len(knowledgeMinIOBucket) < 3 || len(knowledgeMinIOBucket) > 63 {
		return Config{}, fmt.Errorf("%s: bucket length must be between 3 and 63", knowledgeMinIOBucketKey)
	}
	knowledgeParserRevision, err := required(lookup, knowledgeParserRevisionKey)
	if err != nil {
		return Config{}, err
	}
	knowledgeMaxAttempts, err := requiredInt(lookup, knowledgeMaxAttemptsKey, 1, 8)
	if err != nil {
		return Config{}, err
	}
	retrievalEmbeddingModel, err := required(lookup, retrievalEmbeddingModelKey)
	if err != nil {
		return Config{}, err
	}
	retrievalEmbeddingDimension, err := requiredInt(lookup, retrievalEmbeddingDimensionKey, 2560, 2560)
	if err != nil {
		return Config{}, err
	}
	a2aHosts := optionalCSV(lookup, "PLATFORM_A2A_ALLOWED_HOSTS")
	var a2aTimeout time.Duration
	if len(a2aHosts) > 0 {
		a2aTimeout, err = requiredDuration(lookup, "PLATFORM_A2A_TIMEOUT")
		if err != nil {
			return Config{}, err
		}
	}

	return Config{
		HTTPAddr:                    httpAddr,
		Version:                     version,
		ShutdownTimeout:             shutdownTimeout,
		DependencyTimeout:           dependencyTimeout,
		DatabaseURL:                 databaseURL,
		OIDCIssuer:                  strings.TrimRight(oidcIssuer, "/"),
		OIDCAudience:                oidcAudience,
		OpenIMAPIURL:                strings.TrimRight(openIMAPIURL, "/"),
		OpenIMWSURL:                 openIMWSURL,
		OpenIMSecret:                openIMSecret,
		OpenIMAdminUserID:           openIMAdminUserID,
		KnowledgeMinIOURL:           strings.TrimRight(knowledgeMinIOURL, "/"),
		KnowledgeMinIOAccessKey:     knowledgeMinIOAccess,
		KnowledgeMinIOSecretKey:     knowledgeMinIOSecret,
		KnowledgeMinIOBucket:        knowledgeMinIOBucket,
		KnowledgeParserRevision:     knowledgeParserRevision,
		KnowledgeMaxAttempts:        knowledgeMaxAttempts,
		RetrievalEmbeddingModel:     retrievalEmbeddingModel,
		RetrievalEmbeddingDimension: retrievalEmbeddingDimension,
		A2AAllowedHosts:             a2aHosts,
		A2AAllowedPrivateCIDRs:      optionalCSV(lookup, "PLATFORM_A2A_ALLOWED_PRIVATE_CIDRS"),
		A2ATimeout:                  a2aTimeout,
	}, nil
}

func requiredInt(lookup LookupEnv, key string, minimum, maximum int) (int, error) {
	raw, err := required(lookup, key)
	if err != nil {
		return 0, err
	}
	value, err := strconv.Atoi(raw)
	if err != nil || value < minimum || value > maximum {
		return 0, fmt.Errorf("%s: must be between %d and %d", key, minimum, maximum)
	}
	return value, nil
}

func optionalCSV(lookup LookupEnv, key string) []string {
	raw, _ := lookup(key)
	if raw = strings.TrimSpace(raw); raw == "" {
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

func requiredDuration(lookup LookupEnv, key string) (time.Duration, error) {
	value, err := required(lookup, key)
	if err != nil {
		return 0, err
	}
	duration, err := time.ParseDuration(value)
	if err != nil {
		return 0, fmt.Errorf("%s: parse duration: %w", key, err)
	}
	if duration <= 0 {
		return 0, fmt.Errorf("%s: must be greater than zero", key)
	}
	return duration, nil
}

func requiredURL(lookup LookupEnv, key string, schemes ...string) (string, error) {
	value, err := required(lookup, key)
	if err != nil {
		return "", err
	}
	parsed, err := url.Parse(value)
	if err != nil || parsed.Host == "" {
		return "", fmt.Errorf("%s: invalid URL", key)
	}
	for _, scheme := range schemes {
		if parsed.Scheme == scheme {
			return value, nil
		}
	}
	return "", fmt.Errorf("%s: unsupported URL scheme %q", key, parsed.Scheme)
}

func required(lookup LookupEnv, key string) (string, error) {
	value, ok := lookup(key)
	value = strings.TrimSpace(value)
	if !ok || value == "" {
		return "", fmt.Errorf("required environment variable %s is missing", key)
	}
	return value, nil
}

func validateAddress(address string) error {
	_, portValue, err := net.SplitHostPort(address)
	if err != nil {
		return fmt.Errorf("invalid host:port: %w", err)
	}
	port, err := strconv.Atoi(portValue)
	if err != nil {
		return fmt.Errorf("invalid port: %w", err)
	}
	if port < 1 || port > 65535 {
		return errors.New("port must be between 1 and 65535")
	}
	return nil
}
