package ingress

import (
	"context"
	"fmt"
	"os"
	"testing"
	"time"

	"github.com/jackc/pgx/v5/pgxpool"
)

func TestStoreDeduplicatesIngressAndFencesOutbox(t *testing.T) {
	databaseURL := os.Getenv("PLATFORM_TEST_DATABASE_URL")
	if databaseURL == "" {
		t.Skip("PLATFORM_TEST_DATABASE_URL is not set")
	}
	ctx, cancel := context.WithTimeout(context.Background(), 20*time.Second)
	defer cancel()
	pool, err := pgxpool.New(ctx, databaseURL)
	if err != nil {
		t.Fatalf("connect PostgreSQL: %v", err)
	}
	defer pool.Close()
	var senderID string
	if err := pool.QueryRow(ctx, `
SELECT openim_user_id
FROM identity.identity_links
WHERE member_id = 'bbbbbbbb-bbbb-4bbb-8bbb-bbbbbbbbbbbb' AND provisioning_state = 'ready'`).Scan(&senderID); err != nil {
		t.Fatalf("load local mapped sender: %v", err)
	}
	serverMsgID := fmt.Sprintf("integration-%d", time.Now().UnixNano())
	message := Message{
		Source:         Source{Topic: "integration-test", Partition: 0, Offset: time.Now().UnixNano()},
		ServerMsgID:    serverMsgID,
		ClientMsgID:    "client-integration",
		SenderID:       senderID,
		ConversationID: "si_" + senderID + "_receiver",
		SessionType:    1,
		ContentType:    101,
		Content:        `{"content":"integration"}`,
		OccurredAt:     time.Now().UTC(),
	}
	store := NewStore(pool)
	outcome, err := store.Ingest(ctx, message)
	if err != nil || outcome != OutcomeAccepted {
		t.Fatalf("first Ingest() = %q, %v", outcome, err)
	}
	message.Source.Offset++
	outcome, err = store.Ingest(ctx, message)
	if err != nil || outcome != OutcomeDuplicate {
		t.Fatalf("duplicate Ingest() = %q, %v", outcome, err)
	}
	t.Cleanup(func() {
		_, _ = pool.Exec(context.Background(), "DELETE FROM integration.ingress_messages WHERE server_msg_id = $1", serverMsgID)
	})

	records, err := store.ClaimOutbox(ctx, 10, 15*time.Second)
	if err != nil {
		t.Fatalf("ClaimOutbox() error = %v", err)
	}
	var record *OutboxRecord
	for i := range records {
		if string(records[i].Payload) != "" && records[i].EventID != "" {
			var matches bool
			if err := pool.QueryRow(ctx, "SELECT EXISTS (SELECT 1 FROM integration.ingress_messages WHERE event_id = $1 AND server_msg_id = $2)", records[i].EventID, serverMsgID).Scan(&matches); err != nil {
				t.Fatalf("match outbox record: %v", err)
			}
			if matches {
				record = &records[i]
				break
			}
		}
	}
	if record == nil {
		t.Fatal("new ingress outbox record was not claimed")
	}
	if err := store.MarkPublished(ctx, record.EventID, "stale-token"); err == nil {
		t.Fatal("stale outbox lease was accepted")
	}
	if err := store.MarkPublished(ctx, record.EventID, record.LeaseToken); err != nil {
		t.Fatalf("MarkPublished() error = %v", err)
	}
}
