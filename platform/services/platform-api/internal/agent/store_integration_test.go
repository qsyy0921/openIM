package agent

import (
	"context"
	"crypto/sha256"
	"fmt"
	"os"
	"testing"
	"time"

	"github.com/google/uuid"
	"github.com/jackc/pgx/v5/pgxpool"
	"github.com/qsyy0921/openim/platform/services/platform-api/internal/delivery"
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
	trigger := Trigger{
		EventID:  "agent-integration-" + time.Now().Format("20060102150405.000000000"),
		TenantID: "aaaaaaaa-aaaa-4aaa-8aaa-aaaaaaaaaaaa", MemberID: "bbbbbbbb-bbbb-4bbb-8bbb-bbbbbbbbbbbb",
		SourceChannel: "openim", ConversationID: "si_a_b",
		SenderID: "integration-openim-user", SessionType: 1, Mentions: []Mention{{Alias: "@agent", Prompt: "question"}},
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
	t.Cleanup(func() {
		_, _ = pool.Exec(context.Background(), "DELETE FROM audit.delivery_events WHERE run_id = $1::uuid", runID)
		_, _ = pool.Exec(context.Background(), "DELETE FROM agent.runs WHERE id = $1::uuid", runID)
	})

	run, err := store.Claim(ctx, 15*time.Second, 3)
	if err != nil || run == nil || run.ID != runID {
		t.Fatalf("Claim() = %#v, %v", run, err)
	}
	if err := store.FailOrRetry(ctx, *run, "transient integration failure", 3, 0); err != nil {
		t.Fatalf("FailOrRetry() = %v", err)
	}
	run, err = store.Claim(ctx, 15*time.Second, 3)
	if err != nil || run == nil || run.ID != runID {
		t.Fatalf("Claim() after retry = %#v, %v", run, err)
	}
	stale := *run
	stale.LeaseToken = "stale"
	if err := store.SaveCandidate(ctx, stale, Candidate{Text: "answer", Model: "model", ProviderResponseID: "resp", GroundingStatus: GroundingNotApplicable}, nil); err == nil {
		t.Fatal("stale lease saved candidate")
	}
	if err := store.SaveCandidate(ctx, *run, Candidate{Text: "answer", Model: "model", ProviderResponseID: "resp", GroundingStatus: GroundingNotApplicable}, nil); err != nil {
		t.Fatal(err)
	}
	run, err = store.Claim(ctx, 15*time.Second, 3)
	if err != nil || run == nil || run.CandidateText != "answer" {
		t.Fatalf("reply claim = %#v, %v", run, err)
	}
	deliveries := delivery.NewStore(pool)
	if err := deliveries.Prepare(ctx, delivery.PrepareRequest{
		RunID: run.ID, LeaseToken: run.LeaseToken, TenantID: run.TenantID,
		Channel: "openim", TargetID: run.SenderID, SessionType: 1, Content: run.CandidateText,
	}); err != nil {
		t.Fatal(err)
	}
	deliveryRecord, err := deliveries.Claim(ctx, 15*time.Second, 3)
	if err != nil || deliveryRecord == nil || deliveryRecord.RunID != runID {
		t.Fatalf("delivery Claim() = %#v, %v", deliveryRecord, err)
	}
	if err := deliveries.MarkSent(ctx, *deliveryRecord, "server-msg-1"); err != nil {
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

func TestLockAuthorizedCitationRejectsRevocationBeforePersistence(t *testing.T) {
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

	tenantID := "aaaaaaaa-aaaa-4aaa-8aaa-aaaaaaaaaaaa"
	memberID := "bbbbbbbb-bbbb-4bbb-8bbb-bbbbbbbbbbbb"
	documentID, versionID, chunkID := uuid.NewString(), uuid.NewString(), uuid.NewString()
	content := "The atomic citation authorization fixture remains private."
	checksum := checksumForTest(content)
	if _, err := pool.Exec(ctx, `
INSERT INTO knowledge.documents (id, tenant_id, title, source_uri, classification, status)
VALUES ($1::uuid, $2::uuid, 'Atomic citation fixture', $3, 'internal', 'active')`,
		documentID, tenantID, "knowledge://"+documentID,
	); err != nil {
		t.Fatal(err)
	}
	if _, err := pool.Exec(ctx, `
INSERT INTO knowledge.document_versions (
    id, tenant_id, document_id, version_number, checksum, status, published_at, ingestion_state
) VALUES ($1::uuid, $2::uuid, $3::uuid, 1, $4, 'published', now(), 'indexed')`,
		versionID, tenantID, documentID, "sha256:version-"+versionID,
	); err != nil {
		t.Fatal(err)
	}
	if _, err := pool.Exec(ctx, `
INSERT INTO knowledge.chunks (id, tenant_id, document_id, version_id, ordinal, content, checksum)
VALUES ($1::uuid, $2::uuid, $3::uuid, $4::uuid, 0, $5, $6)`,
		chunkID, tenantID, documentID, versionID, content, checksum,
	); err != nil {
		t.Fatal(err)
	}
	if _, err := pool.Exec(ctx,
		"UPDATE knowledge.documents SET current_version_id = $2::uuid WHERE id = $1::uuid",
		documentID, versionID,
	); err != nil {
		t.Fatal(err)
	}
	if _, err := pool.Exec(ctx, `
INSERT INTO authz.document_grants (tenant_id, document_id, member_id, permission)
VALUES ($1::uuid, $2::uuid, $3::uuid, 'read')`,
		tenantID, documentID, memberID,
	); err != nil {
		t.Fatal(err)
	}

	t.Cleanup(func() {
		_, _ = pool.Exec(context.Background(), "UPDATE knowledge.documents SET current_version_id = NULL WHERE id = $1::uuid", documentID)
		_, _ = pool.Exec(context.Background(), "DELETE FROM knowledge.documents WHERE id = $1::uuid", documentID)
	})
	run := Run{TenantID: tenantID, MemberID: memberID}
	evidence := Evidence{
		CitationID: "C1", DocumentID: documentID, VersionID: versionID, ChunkID: chunkID,
		Title: "untrusted title", SourceURI: "untrusted://source",
		Checksum: checksum, Content: "untrusted content",
	}
	tx, err := pool.Begin(ctx)
	if err != nil {
		t.Fatal(err)
	}
	locked, err := lockAuthorizedCitation(ctx, tx, run, evidence)
	_ = tx.Rollback(ctx)
	if err != nil {
		t.Fatal(err)
	}
	if locked.Title != "Atomic citation fixture" || locked.SourceURI != "knowledge://"+documentID ||
		locked.Content != content || locked.Checksum != checksum {
		t.Fatalf("locked citation did not use authoritative metadata: %#v", locked)
	}
	if _, err := pool.Exec(ctx, `
DELETE FROM authz.document_grants
WHERE tenant_id = $1::uuid AND document_id = $2::uuid AND member_id = $3::uuid`,
		tenantID, documentID, memberID,
	); err != nil {
		t.Fatal(err)
	}
	tx, err = pool.Begin(ctx)
	if err != nil {
		t.Fatal(err)
	}
	_, err = lockAuthorizedCitation(ctx, tx, run, evidence)
	_ = tx.Rollback(ctx)
	if err == nil {
		t.Fatal("revoked citation authorization was locked")
	}
}

func checksumForTest(value string) string {
	sum := sha256.Sum256([]byte(value))
	return fmt.Sprintf("sha256:%x", sum[:])
}
