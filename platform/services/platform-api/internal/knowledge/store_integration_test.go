package knowledge

import (
	"context"
	"errors"
	"os"
	"testing"
	"time"

	"github.com/jackc/pgx/v5/pgxpool"
	"github.com/qsyy0921/openim/platform/services/platform-api/internal/knowledgeprojection"
)

func TestStoreIngestionPublicationAndImmediateRevocation(t *testing.T) {
	databaseURL := os.Getenv("PLATFORM_TEST_DATABASE_URL")
	if databaseURL == "" {
		t.Skip("PLATFORM_TEST_DATABASE_URL is not set")
	}
	ctx, cancel := context.WithTimeout(context.Background(), 30*time.Second)
	defer cancel()
	pool, err := pgxpool.New(ctx, databaseURL)
	if err != nil {
		t.Fatal(err)
	}
	defer pool.Close()
	tenantID, err := randomUUID()
	if err != nil {
		t.Fatal(err)
	}
	memberID, err := randomUUID()
	if err != nil {
		t.Fatal(err)
	}
	if _, err := pool.Exec(ctx, `
INSERT INTO identity.tenants (id, external_id, display_name, status)
VALUES ($1::uuid, $2, 'Knowledge integration', 'active')`,
		tenantID, "knowledge-"+tenantID); err != nil {
		t.Fatal(err)
	}
	if _, err := pool.Exec(ctx, `
INSERT INTO identity.members (id, tenant_id, issuer, subject, display_name, status)
VALUES ($1::uuid, $2::uuid, 'test', $3, 'Knowledge admin', 'active')`,
		memberID, tenantID, "subject-"+memberID); err != nil {
		t.Fatal(err)
	}
	t.Cleanup(func() {
		_, _ = pool.Exec(context.Background(), "UPDATE knowledge.documents SET current_version_id = NULL WHERE tenant_id = $1::uuid", tenantID)
		_, _ = pool.Exec(context.Background(), "DELETE FROM knowledge.documents WHERE tenant_id = $1::uuid", tenantID)
		_, _ = pool.Exec(context.Background(), "DELETE FROM knowledge.index_generations WHERE tenant_id = $1::uuid", tenantID)
		_, _ = pool.Exec(context.Background(), "DELETE FROM audit.knowledge_events WHERE tenant_id = $1::uuid", tenantID)
		_, _ = pool.Exec(context.Background(), "DELETE FROM identity.members WHERE tenant_id = $1::uuid", tenantID)
		_, _ = pool.Exec(context.Background(), "DELETE FROM identity.tenants WHERE id = $1::uuid", tenantID)
	})
	store, err := NewStore(pool, StoreConfig{
		Bucket: "knowledge-test", ParserRevision: ParserRevision,
		EmbeddingRevision: "qwen3-embedding:4b", EmbeddingDimension: 2560,
		ProjectionRevision: knowledgeprojection.Revision, MaxAttempts: 3,
	})
	if err != nil {
		t.Fatal(err)
	}
	checksum := checksumText("The records are retained for seven years.")
	command := UploadCommand{
		TenantID: tenantID, ActorMemberID: memberID, Title: "Governance handbook",
		Classification: "internal", IdempotencyKey: "knowledge-integration-upload-1",
		File: UploadFile{
			OriginalFilename: "retention.txt", DeclaredType: "text/plain", DetectedType: "text/plain",
			Format: FormatText, Size: 41, Checksum: checksum,
		},
	}
	reservation, err := store.ReserveUpload(ctx, command)
	if err != nil {
		t.Fatal(err)
	}
	if reservation.Idempotent || reservation.VersionNumber != 1 || reservation.IngestionState != "uploading" {
		t.Fatalf("unexpected reservation: %#v", reservation)
	}
	if err := store.CompleteUpload(ctx, reservation, memberID); err != nil {
		t.Fatal(err)
	}
	if err := store.CompleteUpload(ctx, reservation, memberID); err != nil {
		t.Fatalf("completion reconciliation failed: %v", err)
	}
	job, found, err := store.ClaimJob(ctx, "integration-worker", 5*time.Minute)
	if err != nil || !found {
		t.Fatalf("claim job: found=%v err=%v", found, err)
	}
	if err := store.RenewJobLease(ctx, job, 5*time.Minute); err != nil {
		t.Fatalf("renew job lease: %v", err)
	}
	chunks, err := ChunkSections(reservation.VersionID, []Section{{Heading: "Retention", Text: "The records are retained for seven years."}})
	if err != nil {
		t.Fatal(err)
	}
	vector := make([]float32, 2560)
	vector[0] = 1
	indexed := []IndexedChunk{{Chunk: chunks[0], Embedding: vector}}
	if err := store.CompleteJob(ctx, job, indexed); err != nil {
		t.Fatal(err)
	}
	if err := store.RenewJobLease(ctx, job, 5*time.Minute); !errors.Is(err, ErrLeaseLost) {
		t.Fatalf("completed job lease renewal = %v, want ErrLeaseLost", err)
	}
	if err := store.SetGrant(ctx, tenantID, memberID, reservation.DocumentID, memberID, true); err != nil {
		t.Fatal(err)
	}
	if err := store.Publish(ctx, tenantID, memberID, reservation.DocumentID, reservation.VersionID); err != nil {
		t.Fatal(err)
	}
	var currentVersion string
	var projectionCount int
	if err := pool.QueryRow(ctx, `
SELECT document.current_version_id::text,
       (SELECT count(*)::integer
        FROM knowledge.chunk_search_indexes AS search
        JOIN knowledge.index_generations AS generation ON generation.id = search.generation_id
        WHERE search.tenant_id = document.tenant_id AND generation.state = 'active')
FROM knowledge.documents AS document
WHERE document.tenant_id = $1::uuid AND document.id = $2::uuid`,
		tenantID, reservation.DocumentID).Scan(&currentVersion, &projectionCount); err != nil {
		t.Fatal(err)
	}
	if currentVersion != reservation.VersionID || projectionCount != 1 {
		t.Fatalf("publication projection mismatch: current=%s count=%d", currentVersion, projectionCount)
	}
	var titleIndexed bool
	if err := pool.QueryRow(ctx, `
SELECT search.search_vector @@ to_tsquery('simple', 'governance')
FROM knowledge.chunk_search_indexes AS search
JOIN knowledge.index_generations AS generation
  ON generation.id = search.generation_id
WHERE search.tenant_id = $1::uuid
  AND search.chunk_id = $2::uuid
  AND generation.state = 'active'
  AND generation.projection_revision = $3`,
		tenantID, chunks[0].ID, knowledgeprojection.Revision).Scan(&titleIndexed); err != nil {
		t.Fatal(err)
	}
	if !titleIndexed {
		t.Fatal("document title was not included in the active lexical projection")
	}
	documents, err := store.ListDocuments(ctx, tenantID)
	if err != nil || len(documents) != 1 || documents[0].GrantCount != 1 ||
		documents[0].CurrentVersionID != reservation.VersionID {
		t.Fatalf("document snapshot = %#v, %v", documents, err)
	}
	members, err := store.ListMembers(ctx, tenantID, reservation.DocumentID)
	if err != nil || len(members) != 1 || !members[0].Granted {
		t.Fatalf("member grant snapshot = %#v, %v", members, err)
	}
	duplicate, err := store.ReserveUpload(ctx, command)
	if err != nil {
		t.Fatal(err)
	}
	if !duplicate.Idempotent || duplicate.VersionID != reservation.VersionID {
		t.Fatalf("idempotent reservation changed identity: %#v", duplicate)
	}
	if err := store.SetGrant(ctx, tenantID, memberID, reservation.DocumentID, memberID, false); err != nil {
		t.Fatal(err)
	}
	var grants int
	if err := pool.QueryRow(ctx, `
SELECT count(*)::integer FROM authz.document_grants
WHERE tenant_id = $1::uuid AND document_id = $2::uuid AND member_id = $3::uuid`,
		tenantID, reservation.DocumentID, memberID).Scan(&grants); err != nil {
		t.Fatal(err)
	}
	if grants != 0 {
		t.Fatalf("revoked grant remained present: %d", grants)
	}
	if err := store.Unpublish(ctx, tenantID, memberID, reservation.DocumentID); err != nil {
		t.Fatal(err)
	}
	var unpublished bool
	if err := pool.QueryRow(ctx, `
SELECT current_version_id IS NULL FROM knowledge.documents
WHERE tenant_id = $1::uuid AND id = $2::uuid`, tenantID, reservation.DocumentID).Scan(&unpublished); err != nil {
		t.Fatal(err)
	}
	if !unpublished {
		t.Fatal("document remained published")
	}
	if _, err := pool.Exec(ctx, `
UPDATE knowledge.documents SET classification = 'restricted'
WHERE tenant_id = $1::uuid AND id = $2::uuid`, tenantID, reservation.DocumentID); err != nil {
		t.Fatal(err)
	}
	if err := store.Publish(ctx, tenantID, memberID, reservation.DocumentID, reservation.VersionID); !errors.Is(err, ErrConflict) {
		t.Fatalf("restricted document publication = %v, want ErrConflict", err)
	}
}

func TestStoreObjectCleanupLeaseRetryAndTerminalState(t *testing.T) {
	databaseURL := os.Getenv("PLATFORM_TEST_DATABASE_URL")
	if databaseURL == "" {
		t.Skip("PLATFORM_TEST_DATABASE_URL is not set")
	}
	ctx, cancel := context.WithTimeout(context.Background(), 30*time.Second)
	defer cancel()
	pool, err := pgxpool.New(ctx, databaseURL)
	if err != nil {
		t.Fatal(err)
	}
	defer pool.Close()
	tenantID, err := randomUUID()
	if err != nil {
		t.Fatal(err)
	}
	memberID, err := randomUUID()
	if err != nil {
		t.Fatal(err)
	}
	if _, err := pool.Exec(ctx, `
INSERT INTO identity.tenants (id, external_id, display_name, status)
VALUES ($1::uuid, $2, 'Cleanup integration', 'active')`,
		tenantID, "cleanup-"+tenantID); err != nil {
		t.Fatal(err)
	}
	if _, err := pool.Exec(ctx, `
INSERT INTO identity.members (id, tenant_id, issuer, subject, display_name, status)
VALUES ($1::uuid, $2::uuid, 'test', $3, 'Cleanup admin', 'active')`,
		memberID, tenantID, "subject-"+memberID); err != nil {
		t.Fatal(err)
	}
	t.Cleanup(func() {
		_, _ = pool.Exec(context.Background(), "DELETE FROM audit.knowledge_events WHERE tenant_id = $1::uuid", tenantID)
		_, _ = pool.Exec(context.Background(), "DELETE FROM knowledge.documents WHERE tenant_id = $1::uuid", tenantID)
		_, _ = pool.Exec(context.Background(), "DELETE FROM identity.members WHERE tenant_id = $1::uuid", tenantID)
		_, _ = pool.Exec(context.Background(), "DELETE FROM identity.tenants WHERE id = $1::uuid", tenantID)
	})
	store, err := NewStore(pool, StoreConfig{
		Bucket: "knowledge-cleanup", ParserRevision: ParserRevision,
		EmbeddingRevision: "qwen3-embedding:4b", EmbeddingDimension: 2560,
		ProjectionRevision: knowledgeprojection.Revision, MaxAttempts: 3,
	})
	if err != nil {
		t.Fatal(err)
	}
	newFailedReservation := func(key string) UploadReservation {
		t.Helper()
		reservation, reserveErr := store.ReserveUpload(ctx, UploadCommand{
			TenantID: tenantID, ActorMemberID: memberID, Title: "Cleanup policy " + key,
			Classification: "internal", IdempotencyKey: "cleanup-integration-" + key,
			File: UploadFile{
				OriginalFilename: key + ".txt", DeclaredType: "text/plain", DetectedType: "text/plain",
				Format: FormatText, Size: 8, Checksum: checksumText("cleanup-" + key),
			},
		})
		if reserveErr != nil {
			t.Fatal(reserveErr)
		}
		if markErr := store.MarkUploadFailed(ctx, reservation, memberID,
			"OBJECT_PUT_FAILED", "source object upload failed", true); markErr != nil {
			t.Fatal(markErr)
		}
		return reservation
	}

	completedReservation := newFailedReservation("complete")
	cleanup, found, err := store.ClaimObjectCleanup(ctx, "cleanup-worker", time.Minute)
	if err != nil || !found {
		t.Fatalf("claim cleanup: found=%v err=%v", found, err)
	}
	if cleanup.VersionID != completedReservation.VersionID || cleanup.Attempts != 1 || cleanup.MaxAttempts != 3 {
		t.Fatalf("claimed cleanup = %#v", cleanup)
	}
	if _, secondFound, secondErr := store.ClaimObjectCleanup(ctx, "other-worker", time.Minute); secondErr != nil || secondFound {
		t.Fatalf("leased cleanup was claimed twice: found=%v err=%v", secondFound, secondErr)
	}
	if err := store.CompleteObjectCleanup(ctx, cleanup); err != nil {
		t.Fatal(err)
	}
	var completedState string
	var completedStoredAt, completedLease any
	if err := pool.QueryRow(ctx, `
SELECT state, stored_at, cleanup_lease_token
FROM knowledge.document_source_objects
WHERE tenant_id = $1::uuid AND version_id = $2::uuid`,
		tenantID, completedReservation.VersionID,
	).Scan(&completedState, &completedStoredAt, &completedLease); err != nil {
		t.Fatal(err)
	}
	if completedState != "deleted" || completedStoredAt != nil || completedLease != nil {
		t.Fatalf("completed cleanup state = %s stored=%v lease=%v",
			completedState, completedStoredAt, completedLease)
	}

	failedReservation := newFailedReservation("terminal")
	for attempt := 1; attempt <= 3; attempt++ {
		cleanup, found, err = store.ClaimObjectCleanup(ctx, "cleanup-worker", time.Minute)
		if err != nil || !found || cleanup.VersionID != failedReservation.VersionID {
			t.Fatalf("claim cleanup attempt %d: %#v found=%v err=%v", attempt, cleanup, found, err)
		}
		if err := store.FailObjectCleanup(ctx, cleanup, errors.New("MinIO unavailable")); err != nil {
			t.Fatal(err)
		}
		if attempt < 3 {
			if _, err := pool.Exec(ctx, `
UPDATE knowledge.document_source_objects
SET cleanup_available_at = now()
WHERE tenant_id = $1::uuid AND version_id = $2::uuid`,
				tenantID, failedReservation.VersionID); err != nil {
				t.Fatal(err)
			}
		}
	}
	var terminalState string
	var attempts int
	if err := pool.QueryRow(ctx, `
SELECT state, cleanup_attempts
FROM knowledge.document_source_objects
WHERE tenant_id = $1::uuid AND version_id = $2::uuid`,
		tenantID, failedReservation.VersionID,
	).Scan(&terminalState, &attempts); err != nil {
		t.Fatal(err)
	}
	if terminalState != "delete_failed" || attempts != 3 {
		t.Fatalf("terminal cleanup = %s attempts=%d", terminalState, attempts)
	}
	if _, found, err := store.ClaimObjectCleanup(ctx, "cleanup-worker", time.Minute); err != nil || found {
		t.Fatalf("terminal cleanup remained claimable: found=%v err=%v", found, err)
	}
}
