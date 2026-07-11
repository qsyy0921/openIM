package ingress

import (
	"crypto/tls"
	"crypto/x509"
	"errors"
	"fmt"
	"net"
	"os"
	"strconv"
	"strings"
	"time"

	"github.com/IBM/sarama"
)

type Config struct {
	DatabaseURL       string
	DependencyTimeout time.Duration
	Brokers           []string
	SourceTopic       string
	ConsumerGroup     string
	EventTopic        string
	PollInterval      time.Duration
	OutboxLease       time.Duration
	OutboxBatch       int
	TLS               *tls.Config
}

func LoadConfig() (Config, error) {
	required := func(key string) (string, error) {
		value := strings.TrimSpace(os.Getenv(key))
		if value == "" {
			return "", fmt.Errorf("required environment variable %s is missing", key)
		}
		return value, nil
	}
	databaseURL, err := required("PLATFORM_DATABASE_URL")
	if err != nil {
		return Config{}, err
	}
	dependencyTimeout, err := requiredDuration(required, "PLATFORM_DEPENDENCY_TIMEOUT")
	if err != nil {
		return Config{}, err
	}
	brokerValue, err := required("PLATFORM_KAFKA_BROKERS")
	if err != nil {
		return Config{}, err
	}
	brokers := strings.Split(brokerValue, ",")
	for i := range brokers {
		brokers[i] = strings.TrimSpace(brokers[i])
		if _, _, err := net.SplitHostPort(brokers[i]); err != nil {
			return Config{}, fmt.Errorf("PLATFORM_KAFKA_BROKERS: invalid broker %q: %w", brokers[i], err)
		}
	}
	sourceTopic, err := required("PLATFORM_OPENIM_INGRESS_TOPIC")
	if err != nil {
		return Config{}, err
	}
	consumerGroup, err := required("PLATFORM_OPENIM_INGRESS_GROUP")
	if err != nil {
		return Config{}, err
	}
	eventTopic, err := required("PLATFORM_EVENT_TOPIC")
	if err != nil {
		return Config{}, err
	}
	pollInterval, err := requiredDuration(required, "PLATFORM_OUTBOX_POLL_INTERVAL")
	if err != nil {
		return Config{}, err
	}
	outboxLease, err := requiredDuration(required, "PLATFORM_OUTBOX_LEASE")
	if err != nil {
		return Config{}, err
	}
	batchValue, err := required("PLATFORM_OUTBOX_BATCH")
	if err != nil {
		return Config{}, err
	}
	outboxBatch, err := strconv.Atoi(batchValue)
	if err != nil || outboxBatch < 1 || outboxBatch > 1000 {
		return Config{}, errors.New("PLATFORM_OUTBOX_BATCH must be between 1 and 1000")
	}
	tlsConfig, err := loadTLS(required)
	if err != nil {
		return Config{}, err
	}
	return Config{
		DatabaseURL:       databaseURL,
		DependencyTimeout: dependencyTimeout,
		Brokers:           brokers,
		SourceTopic:       sourceTopic,
		ConsumerGroup:     consumerGroup,
		EventTopic:        eventTopic,
		PollInterval:      pollInterval,
		OutboxLease:       outboxLease,
		OutboxBatch:       outboxBatch,
		TLS:               tlsConfig,
	}, nil
}

func (c Config) SaramaConfig() *sarama.Config {
	config := sarama.NewConfig()
	config.Version = sarama.V3_5_1_0
	config.ClientID = "openim-platform-ingress"
	config.Consumer.Offsets.Initial = sarama.OffsetNewest
	config.Consumer.Offsets.AutoCommit.Enable = false
	config.Consumer.Return.Errors = true
	config.Producer.RequiredAcks = sarama.WaitForAll
	config.Producer.Return.Successes = true
	config.Producer.Return.Errors = true
	if c.TLS != nil {
		config.Net.TLS.Enable = true
		config.Net.TLS.Config = c.TLS
	}
	return config
}

func requiredDuration(required func(string) (string, error), key string) (time.Duration, error) {
	value, err := required(key)
	if err != nil {
		return 0, err
	}
	duration, err := time.ParseDuration(value)
	if err != nil || duration <= 0 {
		return 0, fmt.Errorf("%s must be a positive duration", key)
	}
	return duration, nil
}

func loadTLS(required func(string) (string, error)) (*tls.Config, error) {
	value, err := required("PLATFORM_KAFKA_TLS_ENABLED")
	if err != nil {
		return nil, err
	}
	enabled, err := strconv.ParseBool(value)
	if err != nil {
		return nil, errors.New("PLATFORM_KAFKA_TLS_ENABLED must be true or false")
	}
	if !enabled {
		return nil, nil
	}
	caPath, err := required("PLATFORM_KAFKA_TLS_CA")
	if err != nil {
		return nil, err
	}
	caPEM, err := os.ReadFile(caPath)
	if err != nil {
		return nil, fmt.Errorf("read Kafka CA: %w", err)
	}
	roots := x509.NewCertPool()
	if !roots.AppendCertsFromPEM(caPEM) {
		return nil, errors.New("PLATFORM_KAFKA_TLS_CA contains no certificates")
	}
	config := &tls.Config{MinVersion: tls.VersionTLS12, RootCAs: roots}
	certPath := strings.TrimSpace(os.Getenv("PLATFORM_KAFKA_TLS_CERT"))
	keyPath := strings.TrimSpace(os.Getenv("PLATFORM_KAFKA_TLS_KEY"))
	if (certPath == "") != (keyPath == "") {
		return nil, errors.New("PLATFORM_KAFKA_TLS_CERT and PLATFORM_KAFKA_TLS_KEY must be set together")
	}
	if certPath != "" {
		certificate, err := tls.LoadX509KeyPair(certPath, keyPath)
		if err != nil {
			return nil, fmt.Errorf("load Kafka client certificate: %w", err)
		}
		config.Certificates = []tls.Certificate{certificate}
	}
	return config, nil
}
