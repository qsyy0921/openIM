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
	DatabaseURL, EventTopic, ConsumerGroup        string
	Brokers                                       []string
	TLS                                           *tls.Config
	DependencyTimeout, Poll, Lease                time.Duration
	MaxAttempts                                   int
	IntelligenceURL                               string
	OpenIMAPIURL, OpenIMSecret, OpenIMAdminUserID string
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
	openIM, err := httpURL("PLATFORM_OPENIM_API_URL")
	if err != nil {
		return Config{}, err
	}
	secret, err := env("PLATFORM_OPENIM_SECRET")
	if err != nil {
		return Config{}, err
	}
	admin, err := env("PLATFORM_OPENIM_ADMIN_USER_ID")
	if err != nil {
		return Config{}, err
	}
	tlsConfig, err := kafkaTLS()
	if err != nil {
		return Config{}, err
	}
	return Config{DatabaseURL: database, Brokers: brokers, EventTopic: topic, ConsumerGroup: group, TLS: tlsConfig,
		DependencyTimeout: dependency, Poll: poll, Lease: lease, MaxAttempts: maxAttempts,
		IntelligenceURL: strings.TrimRight(intelligence, "/"), OpenIMAPIURL: strings.TrimRight(openIM, "/"), OpenIMSecret: secret, OpenIMAdminUserID: admin}, nil
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
