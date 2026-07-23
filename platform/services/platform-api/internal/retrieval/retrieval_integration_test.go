package retrieval

import (
	"context"
	"crypto/rand"
	"crypto/sha256"
	"encoding/hex"
	"fmt"
	"os"
	"testing"
	"time"

	"github.com/jackc/pgx/v5/pgxpool"
)

func TestSearchEnforcesTenantGrantClassificationAndRevocation(t *testing.T) {
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

	tenantID, memberID := testUUID(t), testUUID(t)
	otherTenant, otherMember := testUUID(t), testUUID(t)
	if _, err := pool.Exec(ctx, `INSERT INTO identity.tenants (id, external_id, display_name, status) VALUES ($1::uuid, $2, 'Retrieval test', 'active')`, tenantID, "retrieval-"+tenantID); err != nil {
		t.Fatal(err)
	}
	if _, err := pool.Exec(ctx, `INSERT INTO identity.members (id, tenant_id, issuer, subject, display_name, status) VALUES ($1::uuid, $2::uuid, 'test', $3, 'Retrieval member', 'active')`, memberID, tenantID, "subject-"+memberID); err != nil {
		t.Fatal(err)
	}
	if _, err := pool.Exec(ctx, `INSERT INTO identity.tenants (id, external_id, display_name, status) VALUES ($1::uuid, $2, 'Other', 'active')`, otherTenant, "other-"+otherTenant); err != nil {
		t.Fatal(err)
	}
	if _, err := pool.Exec(ctx, `INSERT INTO identity.members (id, tenant_id, issuer, subject, display_name, status) VALUES ($1::uuid, $2::uuid, 'test', $3, 'Other', 'active')`, otherMember, otherTenant, "subject-"+otherMember); err != nil {
		t.Fatal(err)
	}

	authorized := seedDocument(t, ctx, pool, tenantID, "Authorized policy", "internal", "retention policy is seven years")
	unauthorized := seedDocument(t, ctx, pool, tenantID, "Private policy", "internal", "retention policy is ninety years")
	restricted := seedDocument(t, ctx, pool, tenantID, "Restricted policy", "restricted", "retention policy is secret")
	crossTenant := seedDocument(t, ctx, pool, otherTenant, "Other tenant policy", "internal", "retention policy belongs elsewhere")
	t.Cleanup(func() {
		for _, doc := range []testDocument{authorized, unauthorized, restricted, crossTenant} {
			_, _ = pool.Exec(context.Background(), "UPDATE knowledge.documents SET current_version_id = NULL WHERE id = $1::uuid", doc.documentID)
			_, _ = pool.Exec(context.Background(), "DELETE FROM knowledge.documents WHERE id = $1::uuid", doc.documentID)
		}
		_, _ = pool.Exec(context.Background(), "DELETE FROM knowledge.index_generations WHERE tenant_id IN ($1::uuid, $2::uuid)", tenantID, otherTenant)
		_, _ = pool.Exec(context.Background(), "DELETE FROM identity.tenants WHERE id = $1::uuid", tenantID)
		_, _ = pool.Exec(context.Background(), "DELETE FROM identity.tenants WHERE id = $1::uuid", otherTenant)
	})
	for _, doc := range []testDocument{authorized, restricted} {
		if _, err := pool.Exec(ctx, `INSERT INTO authz.document_grants (tenant_id, document_id, member_id, permission) VALUES ($1::uuid, $2::uuid, $3::uuid, 'read')`, tenantID, doc.documentID, memberID); err != nil {
			t.Fatal(err)
		}
	}
	if _, err := pool.Exec(ctx, `INSERT INTO authz.document_grants (tenant_id, document_id, member_id, permission) VALUES ($1::uuid, $2::uuid, $3::uuid, 'read')`, otherTenant, crossTenant.documentID, otherMember); err != nil {
		t.Fatal(err)
	}
	if _, err := pool.Exec(ctx, `INSERT INTO authz.document_grants (tenant_id, document_id, member_id, permission) VALUES ($1::uuid, $2::uuid, $3::uuid, 'read')`, tenantID, authorized.documentID, otherMember); err == nil {
		t.Fatal("cross-tenant member grant was accepted")
	}

	generationID := seedGeneration(t, ctx, pool, tenantID, 3)
	otherGenerationID := seedGeneration(t, ctx, pool, otherTenant, 1)
	for _, doc := range []testDocument{authorized, unauthorized, restricted} {
		seedProjection(t, ctx, pool, generationID, tenantID, doc)
	}
	seedProjection(t, ctx, pool, otherGenerationID, otherTenant, crossTenant)

	store, err := NewStore(pool, fixtureEmbedder{}, fixtureReranker{baseScore: 1}, Config{
		ModelRevision: "qwen3-embedding:4b", Dimension: 2560,
		DenseMinSimilarity: 0.9, MaxCandidates: 32,
		RerankerModel: LockedRerankerModel, RerankerRevision: LockedRerankerRevision,
		HNSWEFSearch: 100,
	})
	if err != nil {
		t.Fatal(err)
	}
	items, err := store.Search(ctx, Query{TenantID: tenantID, MemberID: memberID, Purpose: "agent_answer", Text: "retention policy", Limit: 8})
	if err != nil {
		t.Fatal(err)
	}
	if len(items) != 1 || items[0].DocumentID != authorized.documentID || items[0].CitationID != "C1" {
		t.Fatalf("authorized search leaked or missed data: %#v", items)
	}
	validated, err := store.ValidateCitations(ctx, Query{
		TenantID: tenantID, MemberID: memberID, Purpose: "agent_answer",
	}, "The retention policy is seven years [C1].", items)
	if err != nil || len(validated) != 1 || validated[0].Checksum != items[0].Checksum {
		t.Fatalf("validated citations = %#v, %v", validated, err)
	}
	if _, err := pool.Exec(ctx, "UPDATE knowledge.chunks SET content = 'tampered' WHERE id = $1::uuid", authorized.chunkID); err != nil {
		t.Fatal(err)
	}
	if _, err := store.ValidateCitations(ctx, Query{
		TenantID: tenantID, MemberID: memberID, Purpose: "agent_answer",
	}, "The retention policy is seven years [C1].", items); err == nil {
		t.Fatal("checksum-tampered citation was accepted")
	}
	if _, err := pool.Exec(ctx, "UPDATE knowledge.chunks SET content = $2 WHERE id = $1::uuid", authorized.chunkID, authorized.content); err != nil {
		t.Fatal(err)
	}
	unsupportedStore, err := NewStore(pool, fixtureEmbedder{}, fixtureReranker{baseScore: -1}, store.config)
	if err != nil {
		t.Fatal(err)
	}
	if _, err := unsupportedStore.ValidateCitations(ctx, Query{
		TenantID: tenantID, MemberID: memberID, Purpose: "agent_answer",
	}, "The retention policy is seven years [C1].", items); err == nil {
		t.Fatal("unsupported citation was accepted")
	}
	nextModel := "qwen3-embedding:4b-generation-2"
	nextStore, err := NewStore(pool, fixtureEmbedder{model: nextModel}, fixtureReranker{baseScore: 1}, Config{
		ModelRevision: nextModel, Dimension: 2560,
		DenseMinSimilarity: 0.9, MaxCandidates: 32,
		RerankerModel: LockedRerankerModel, RerankerRevision: LockedRerankerRevision,
		HNSWEFSearch: 100,
	})
	if err != nil {
		t.Fatal(err)
	}
	reindexed, err := nextStore.BuildIndexGeneration(ctx, tenantID, 2, 1)
	if err != nil || !reindexed.Activated || reindexed.Expected != 3 || reindexed.Indexed != 3 {
		t.Fatalf("reindex generation = %#v, %v", reindexed, err)
	}
	items, err = nextStore.Search(ctx, Query{
		TenantID: tenantID, MemberID: memberID, Purpose: "agent_answer",
		Text: "retention policy", Limit: 8,
	})
	if err != nil || len(items) != 1 || items[0].DocumentID != authorized.documentID {
		t.Fatalf("search after index activation = %#v, %v", items, err)
	}
	store = nextStore
	if _, err := pool.Exec(ctx, "DELETE FROM authz.document_grants WHERE tenant_id = $1::uuid AND document_id = $2::uuid AND member_id = $3::uuid", tenantID, authorized.documentID, memberID); err != nil {
		t.Fatal(err)
	}
	if _, err := store.ValidateCitations(ctx, Query{
		TenantID: tenantID, MemberID: memberID, Purpose: "agent_answer",
	}, "The retention policy is seven years [C1].", items); err == nil {
		t.Fatal("revoked citation remained valid")
	}
	items, err = store.Search(ctx, Query{TenantID: tenantID, MemberID: memberID, Purpose: "agent_answer", Text: "retention policy", Limit: 8})
	if err != nil {
		t.Fatal(err)
	}
	if len(items) != 0 {
		t.Fatalf("revoked document remained visible: %#v", items)
	}
}

type fixtureEmbedder struct{ model string }

func (s fixtureEmbedder) Embed(_ context.Context, texts []string) (EmbeddingBatch, error) {
	model := s.model
	if model == "" {
		model = "qwen3-embedding:4b"
	}
	vectors := make([][]float32, len(texts))
	for index := range texts {
		vectors[index] = testVector()
	}
	return EmbeddingBatch{Model: model, Dimension: 2560, Vectors: vectors}, nil
}

type fixtureReranker struct{ baseScore float64 }

func (s fixtureReranker) Rerank(_ context.Context, _ string, candidates []RerankCandidate) (RerankResponse, error) {
	scores := make([]RerankScore, len(candidates))
	for index, candidate := range candidates {
		scores[index] = RerankScore{CandidateID: candidate.CandidateID, Score: s.baseScore - float64(index)/100}
	}
	return RerankResponse{Model: LockedRerankerModel, Revision: LockedRerankerRevision, Scores: scores}, nil
}

type testDocument struct {
	documentID string
	chunkID    string
	checksum   string
	content    string
}

func seedDocument(t *testing.T, ctx context.Context, pool *pgxpool.Pool, tenantID, title, classification, content string) testDocument {
	t.Helper()
	documentID, versionID, chunkID := testUUID(t), testUUID(t), testUUID(t)
	tx, err := pool.Begin(ctx)
	if err != nil {
		t.Fatal(err)
	}
	defer func() { _ = tx.Rollback(ctx) }()
	if _, err := tx.Exec(ctx, `INSERT INTO knowledge.documents (id, tenant_id, title, source_uri, classification) VALUES ($1::uuid, $2::uuid, $3, $4, $5)`, documentID, tenantID, title, "test://"+documentID, classification); err != nil {
		t.Fatal(err)
	}
	if _, err := tx.Exec(ctx, `INSERT INTO knowledge.document_versions (id, tenant_id, document_id, version_number, checksum, status, published_at, ingestion_state) VALUES ($1::uuid, $2::uuid, $3::uuid, 1, $4, 'published', now(), 'indexed')`, versionID, tenantID, documentID, checksum("version-"+versionID)); err != nil {
		t.Fatal(err)
	}
	chunkChecksum := checksum(content)
	if _, err := tx.Exec(ctx, `INSERT INTO knowledge.chunks (id, tenant_id, document_id, version_id, ordinal, content, checksum) VALUES ($1::uuid, $2::uuid, $3::uuid, $4::uuid, 0, $5, $6)`, chunkID, tenantID, documentID, versionID, content, chunkChecksum); err != nil {
		t.Fatal(err)
	}
	if _, err := tx.Exec(ctx, `UPDATE knowledge.documents SET current_version_id = $2::uuid WHERE id = $1::uuid`, documentID, versionID); err != nil {
		t.Fatal(err)
	}
	if err := tx.Commit(ctx); err != nil {
		t.Fatal(err)
	}
	return testDocument{documentID: documentID, chunkID: chunkID, checksum: chunkChecksum, content: content}
}

func seedGeneration(t *testing.T, ctx context.Context, pool *pgxpool.Pool, tenantID string, chunkCount int) string {
	t.Helper()
	generationID := testUUID(t)
	if _, err := pool.Exec(ctx, `
INSERT INTO knowledge.index_generations
    (id, tenant_id, model_revision, dimension, storage_type, distance_metric,
     state, expected_chunk_count, indexed_chunk_count, activated_at)
VALUES ($1::uuid, $2::uuid, 'qwen3-embedding:4b', 2560, 'halfvec', 'cosine',
        'active', $3, $3, now())`, generationID, tenantID, chunkCount); err != nil {
		t.Fatal(err)
	}
	return generationID
}

func seedProjection(t *testing.T, ctx context.Context, pool *pgxpool.Pool, generationID, tenantID string, document testDocument) {
	t.Helper()
	if _, err := pool.Exec(ctx, `
INSERT INTO knowledge.chunk_search_indexes
    (generation_id, tenant_id, chunk_id, model_revision, dimension,
     content_checksum, embedding, search_vector, normalized)
VALUES ($1::uuid, $2::uuid, $3::uuid, 'qwen3-embedding:4b', 2560,
        $4, $5::halfvec, to_tsvector('simple', 'retention policy'), true)`,
		generationID, tenantID, document.chunkID, document.checksum,
		halfVectorLiteral(testVector())); err != nil {
		t.Fatal(err)
	}
}

func testVector() []float32 {
	vector := make([]float32, 2560)
	vector[0] = 1
	return vector
}

func checksum(value string) string {
	sum := sha256.Sum256([]byte(value))
	return fmt.Sprintf("sha256:%x", sum[:])
}

func testUUID(t *testing.T) string {
	t.Helper()
	var value [16]byte
	if _, err := rand.Read(value[:]); err != nil {
		t.Fatal(err)
	}
	value[6] = (value[6] & 0x0f) | 0x40
	value[8] = (value[8] & 0x3f) | 0x80
	x := hex.EncodeToString(value[:])
	return x[:8] + "-" + x[8:12] + "-" + x[12:16] + "-" + x[16:20] + "-" + x[20:]
}
