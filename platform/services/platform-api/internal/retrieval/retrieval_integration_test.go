package retrieval

import (
	"context"
	"crypto/rand"
	"encoding/hex"
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

	const tenantID = "aaaaaaaa-aaaa-4aaa-8aaa-aaaaaaaaaaaa"
	const memberID = "bbbbbbbb-bbbb-4bbb-8bbb-bbbbbbbbbbbb"
	otherTenant, otherMember := testUUID(t), testUUID(t)
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

	store := NewStore(pool)
	items, err := store.Search(ctx, Query{TenantID: tenantID, MemberID: memberID, Purpose: "agent_answer", Text: "retention policy", Limit: 8})
	if err != nil {
		t.Fatal(err)
	}
	if len(items) != 1 || items[0].DocumentID != authorized.documentID || items[0].CitationID != "C1" {
		t.Fatalf("authorized search leaked or missed data: %#v", items)
	}
	if _, err := pool.Exec(ctx, "DELETE FROM authz.document_grants WHERE tenant_id = $1::uuid AND document_id = $2::uuid AND member_id = $3::uuid", tenantID, authorized.documentID, memberID); err != nil {
		t.Fatal(err)
	}
	items, err = store.Search(ctx, Query{TenantID: tenantID, MemberID: memberID, Purpose: "agent_answer", Text: "retention policy", Limit: 8})
	if err != nil {
		t.Fatal(err)
	}
	if len(items) != 0 {
		t.Fatalf("revoked document remained visible: %#v", items)
	}
}

type testDocument struct{ documentID string }

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
	if _, err := tx.Exec(ctx, `INSERT INTO knowledge.document_versions (id, tenant_id, document_id, version_number, checksum, status, published_at) VALUES ($1::uuid, $2::uuid, $3::uuid, 1, $4, 'published', now())`, versionID, tenantID, documentID, "version-"+versionID); err != nil {
		t.Fatal(err)
	}
	if _, err := tx.Exec(ctx, `INSERT INTO knowledge.chunks (id, tenant_id, document_id, version_id, ordinal, content, checksum) VALUES ($1::uuid, $2::uuid, $3::uuid, $4::uuid, 0, $5, $6)`, chunkID, tenantID, documentID, versionID, content, "chunk-"+chunkID); err != nil {
		t.Fatal(err)
	}
	if _, err := tx.Exec(ctx, `UPDATE knowledge.documents SET current_version_id = $2::uuid WHERE id = $1::uuid`, documentID, versionID); err != nil {
		t.Fatal(err)
	}
	if err := tx.Commit(ctx); err != nil {
		t.Fatal(err)
	}
	return testDocument{documentID: documentID}
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
