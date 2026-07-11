package main

import (
	"context"
	"errors"
	"log/slog"
	"os"
	"os/signal"
	"syscall"

	"github.com/IBM/sarama"
	"github.com/jackc/pgx/v5/pgxpool"
	"github.com/qsyy0921/openim/platform/services/platform-api/internal/ingress"
)

func main() {
	if err := run(); err != nil {
		slog.Error("platform-ingress stopped with error", "error", err)
		os.Exit(1)
	}
}

func run() error {
	cfg, err := ingress.LoadConfig()
	if err != nil {
		return err
	}
	startupCtx, cancelStartup := context.WithTimeout(context.Background(), cfg.DependencyTimeout)
	defer cancelStartup()
	pool, err := pgxpool.New(startupCtx, cfg.DatabaseURL)
	if err != nil {
		return err
	}
	defer pool.Close()
	if err := pool.Ping(startupCtx); err != nil {
		return err
	}

	kafkaConfig := cfg.SaramaConfig()
	group, err := sarama.NewConsumerGroup(cfg.Brokers, cfg.ConsumerGroup, kafkaConfig)
	if err != nil {
		return err
	}
	defer group.Close()
	producer, err := sarama.NewSyncProducer(cfg.Brokers, kafkaConfig)
	if err != nil {
		return err
	}
	defer producer.Close()

	store := ingress.NewStore(pool)
	consumer := ingress.NewConsumer(group, cfg.SourceTopic, store)
	publisher := ingress.NewPublisher(
		store,
		ingress.NewKafkaProducer(producer, cfg.EventTopic),
		cfg.PollInterval,
		cfg.OutboxLease,
		cfg.OutboxBatch,
	)
	ctx, stop := signal.NotifyContext(context.Background(), os.Interrupt, syscall.SIGTERM)
	defer stop()
	errCh := make(chan error, 2)
	go func() { errCh <- consumer.Run(ctx) }()
	go func() { errCh <- publisher.Run(ctx) }()
	slog.Info("platform-ingress started", "source_topic", cfg.SourceTopic, "event_topic", cfg.EventTopic, "consumer_group", cfg.ConsumerGroup)
	select {
	case <-ctx.Done():
		return nil
	case err := <-errCh:
		stop()
		if err == nil || errors.Is(err, context.Canceled) {
			return nil
		}
		return err
	}
}
