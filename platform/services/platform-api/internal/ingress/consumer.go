package ingress

import (
	"context"
	"errors"
	"fmt"
	"log/slog"

	"github.com/IBM/sarama"
	"github.com/openimsdk/protocol/sdkws"
	"google.golang.org/protobuf/proto"
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
			slog.Warn("Kafka consumer group error", "error", err)
		}
	}()
	for ctx.Err() == nil {
		if err := c.group.Consume(ctx, []string{c.topic}, &consumerHandler{store: c.store}); err != nil {
			if errors.Is(err, sarama.ErrClosedConsumerGroup) || ctx.Err() != nil {
				return nil
			}
			return fmt.Errorf("consume OpenIM messages: %w", err)
		}
	}
	return nil
}

type consumerHandler struct {
	store *Store
}

func (*consumerHandler) Setup(sarama.ConsumerGroupSession) error   { return nil }
func (*consumerHandler) Cleanup(sarama.ConsumerGroupSession) error { return nil }

func (h *consumerHandler) ConsumeClaim(session sarama.ConsumerGroupSession, claim sarama.ConsumerGroupClaim) error {
	for {
		select {
		case <-session.Context().Done():
			return nil
		case record, ok := <-claim.Messages():
			if !ok {
				return nil
			}
			source := Source{Topic: record.Topic, Partition: record.Partition, Offset: record.Offset, Key: string(record.Key)}
			var data sdkws.MsgData
			if err := proto.Unmarshal(record.Value, &data); err != nil {
				if storeErr := h.store.Reject(session.Context(), source, "", "", "invalid_protobuf"); storeErr != nil {
					return storeErr
				}
				markAndCommit(session, record)
				continue
			}
			message, err := Normalize(source, &data)
			if err != nil {
				if storeErr := h.store.Reject(session.Context(), source, data.ServerMsgID, data.SendID, "invalid_message"); storeErr != nil {
					return storeErr
				}
				markAndCommit(session, record)
				continue
			}
			outcome, err := h.store.Ingest(session.Context(), message)
			if err != nil {
				return err
			}
			slog.Info("OpenIM message ingested", "outcome", outcome, "topic", record.Topic, "partition", record.Partition, "offset", record.Offset)
			markAndCommit(session, record)
		}
	}
}

func markAndCommit(session sarama.ConsumerGroupSession, record *sarama.ConsumerMessage) {
	session.MarkMessage(record, "")
	session.Commit()
}
