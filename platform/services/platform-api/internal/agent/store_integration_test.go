package agent

import (
	"context"
	"os"
	"testing"
	"time"

	"github.com/jackc/pgx/v5/pgxpool"
)

func TestStoreDeduplicatesAndFencesRun(t *testing.T) {
	databaseURL := os.Getenv("PLATFORM_TEST_DATABASE_URL")
	if databaseURL == "" {
		t.Skip("PLATFORM_TEST_DATABASE_URL is not set")
	}
	ctx, cancel := context.WithTimeout(context.Background(), 20*time.Second)
	defer cancel()
	pool, err := pgxpool.New(ctx, databaseURL)
	if err != nil {
		t.Fatal(err)
	}
	defer pool.Close()
	var senderID string
	if err := pool.QueryRow(ctx, `SELECT openim_user_id FROM identity.identity_links WHERE member_id = 'bbbbbbbb-bbbb-4bbb-8bbb-bbbbbbbbbbbb' AND provisioning_state = 'ready'`).Scan(&senderID); err != nil {
		t.Fatal(err)
	}
	trigger := Trigger{
		EventID:  "agent-integration-" + time.Now().Format("20060102150405.000000000"),
		TenantID: "aaaaaaaa-aaaa-4aaa-8aaa-aaaaaaaaaaaa", ConversationID: "si_a_b",
		SenderID: senderID, SessionType: 1, Mentions: []Mention{{Alias: "@agent", Prompt: "question"}},
	}
	store := NewStore(pool)
	source := Source{Topic: "integration", Partition: 0, Offset: time.Now().UnixNano()}
	result, err := store.Enqueue(ctx, source, trigger)
	if err != nil {
		t.Fatal(err)
	}
	runID := result.RunID
	duplicate, err := store.Enqueue(ctx, source, trigger)
	if err != nil || duplicate.RunID != runID {
		t.Fatalf("duplicate Enqueue() = %#v, %v", duplicate, err)
	}
	t.Cleanup(func() { _, _ = pool.Exec(context.Background(), "DELETE FROM agent.runs WHERE id = $1::uuid", runID) })

	run, err := store.Claim(ctx, 15*time.Second, 3)
	if err != nil || run == nil || run.ID != runID {
		t.Fatalf("Claim() = %#v, %v", run, err)
	}
	stale := *run
	stale.LeaseToken = "stale"
	if err := store.SaveCandidate(ctx, stale, Candidate{Text: "answer", Model: "model", ProviderResponseID: "resp"}, nil); err == nil {
		t.Fatal("stale lease saved candidate")
	}
	if err := store.SaveCandidate(ctx, *run, Candidate{Text: "answer", Model: "model", ProviderResponseID: "resp"}, nil); err != nil {
		t.Fatal(err)
	}
	run, err = store.Claim(ctx, 15*time.Second, 3)
	if err != nil || run == nil || run.CandidateText != "answer" {
		t.Fatalf("reply claim = %#v, %v", run, err)
	}
	if err := store.CompleteReply(ctx, *run, "server-msg-1", false); err != nil {
		t.Fatal(err)
	}
	var state, serverMsgID string
	if err := pool.QueryRow(ctx, "SELECT state, reply_server_msg_id FROM agent.runs WHERE id = $1::uuid", runID).Scan(&state, &serverMsgID); err != nil {
		t.Fatal(err)
	}
	if state != "succeeded" || serverMsgID != "server-msg-1" {
		t.Fatalf("state=%q reply=%q", state, serverMsgID)
	}
}
