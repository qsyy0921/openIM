package ingress

import (
	"context"
	"fmt"
	"log/slog"
	"time"

	"github.com/IBM/sarama"
)

type EventProducer interface {
	Publish(ctx context.Context, key string, payload []byte) error
}

type KafkaProducer struct {
	producer sarama.SyncProducer
	topic    string
}

func NewKafkaProducer(producer sarama.SyncProducer, topic string) *KafkaProducer {
	return &KafkaProducer{producer: producer, topic: topic}
}

func (p *KafkaProducer) Publish(ctx context.Context, key string, payload []byte) error {
	if err := ctx.Err(); err != nil {
		return err
	}
	_, _, err := p.producer.SendMessage(&sarama.ProducerMessage{
		Topic: p.topic,
		Key:   sarama.StringEncoder(key),
		Value: sarama.ByteEncoder(payload),
	})
	if err != nil {
		return fmt.Errorf("publish platform event: %w", err)
	}
	return nil
}

type Publisher struct {
	store        *Store
	producer     EventProducer
	pollInterval time.Duration
	lease        time.Duration
	batch        int
}

func NewPublisher(store *Store, producer EventProducer, pollInterval, lease time.Duration, batch int) *Publisher {
	return &Publisher{store: store, producer: producer, pollInterval: pollInterval, lease: lease, batch: batch}
}

func (p *Publisher) Run(ctx context.Context) error {
	ticker := time.NewTicker(p.pollInterval)
	defer ticker.Stop()
	for {
		if err := p.publishBatch(ctx); err != nil {
			return err
		}
		select {
		case <-ctx.Done():
			return nil
		case <-ticker.C:
		}
	}
}

func (p *Publisher) publishBatch(ctx context.Context) error {
	records, err := p.store.ClaimOutbox(ctx, p.batch, p.lease)
	if err != nil {
		return err
	}
	for _, record := range records {
		if err := p.producer.Publish(ctx, record.EventKey, record.Payload); err != nil {
			failure := err.Error()
			if len(failure) > 512 {
				failure = failure[:512]
			}
			if releaseErr := p.store.ReleaseOutbox(ctx, record.EventID, record.LeaseToken, failure, retryDelay(record.Attempts)); releaseErr != nil {
				return fmt.Errorf("publish failed (%v) and release failed: %w", err, releaseErr)
			}
			slog.Warn("outbox publish failed", "event_id", record.EventID, "attempt", record.Attempts, "error", err)
			continue
		}
		if err := p.store.MarkPublished(ctx, record.EventID, record.LeaseToken); err != nil {
			return err
		}
		slog.Info("outbox event published", "event_id", record.EventID, "attempt", record.Attempts)
	}
	return nil
}

func retryDelay(attempt int) time.Duration {
	if attempt < 1 {
		attempt = 1
	}
	if attempt > 8 {
		attempt = 8
	}
	return time.Duration(1<<(attempt-1)) * time.Second
}
