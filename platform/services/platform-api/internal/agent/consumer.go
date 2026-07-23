package agent

import (
	"context"
	"encoding/json"
	"errors"
	"fmt"
	"log/slog"

	"github.com/IBM/sarama"
	"github.com/qsyy0921/openim/platform/services/platform-api/internal/ingress"
)

type Consumer struct {
	group sarama.ConsumerGroup
	topic string
	store *Store
}

func NewConsumer(group sarama.ConsumerGroup, topic string, store *Store) *Consumer {
	return &Consumer{group: group, topic: topic, store: store}
}

func (c *Consumer) Run(ctx context.Context) error {
	go func() {
		for err := range c.group.Errors() {
			slog.Warn("Agent Kafka consumer error", "error", err)
		}
	}()
	for ctx.Err() == nil {
		if err := c.group.Consume(ctx, []string{c.topic}, &agentConsumerHandler{store: c.store}); err != nil {
			if errors.Is(err, sarama.ErrClosedConsumerGroup) || ctx.Err() != nil {
				return nil
			}
			return fmt.Errorf("consume Agent trigger events: %w", err)
		}
	}
	return nil
}

type agentConsumerHandler struct{ store *Store }

func (*agentConsumerHandler) Setup(sarama.ConsumerGroupSession) error   { return nil }
func (*agentConsumerHandler) Cleanup(sarama.ConsumerGroupSession) error { return nil }

func (h *agentConsumerHandler) ConsumeClaim(session sarama.ConsumerGroupSession, claim sarama.ConsumerGroupClaim) error {
	for record := range claim.Messages() {
		source := Source{Topic: record.Topic, Partition: record.Partition, Offset: record.Offset}
		var event ingress.Event
		if err := json.Unmarshal(record.Value, &event); err != nil {
			if err := h.store.Reject(session.Context(), source, "", "invalid_json"); err != nil {
				return err
			}
			markAndCommit(session, record)
			continue
		}
		trigger, matched, err := Classify(event)
		if err != nil {
			if err := h.store.Reject(session.Context(), source, event.EventID, "invalid_event"); err != nil {
				return err
			}
			markAndCommit(session, record)
			continue
		}
		if matched {
			result, err := h.store.Enqueue(session.Context(), source, trigger)
			if err != nil {
				return err
			}
			if result.RunID != "" {
				slog.Info("Agent Run enqueued", "run_id", result.RunID, "event_id", event.EventID)
			} else if result.RejectionReason != "" {
				slog.Info("Agent trigger rejected", "event_id", event.EventID, "reason", result.RejectionReason)
			}
		}
		markAndCommit(session, record)
	}
	return nil
}

func markAndCommit(session sarama.ConsumerGroupSession, record *sarama.ConsumerMessage) {
	session.MarkMessage(record, "")
	session.Commit()
}
