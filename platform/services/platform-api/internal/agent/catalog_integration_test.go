package agent

import (
	"context"
	"os"
	"testing"
	"time"

	"github.com/jackc/pgx/v5"
	"github.com/jackc/pgx/v5/pgxpool"
)

const (
	catalogTestTenant = "aaaaaaaa-aaaa-4aaa-8aaa-aaaaaaaaaaaa"
	catalogTestMember = "bbbbbbbb-bbbb-4bbb-8bbb-bbbbbbbbbbbb"
)

func TestCatalogPinsSwitchesAndRollsBackVersions(t *testing.T) {
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
	tx, err := pool.BeginTx(ctx, pgx.TxOptions{IsoLevel: pgx.Serializable})
	if err != nil {
		t.Fatal(err)
	}
	defer func() { _ = tx.Rollback(context.Background()) }()

	var agentID, versionOneID, deploymentID, triggerID, senderID string
	var revision int64
	if err := tx.QueryRow(ctx, `
SELECT d.id::text, v.id::text, dep.id::text, tr.id::text, dep.revision, l.openim_user_id
FROM agent.definitions d
JOIN agent.deployments dep
  ON dep.tenant_id = d.tenant_id AND dep.agent_id = d.id AND dep.slot = 'production'
JOIN agent.versions v
  ON v.tenant_id = d.tenant_id AND v.agent_id = d.id AND v.id = dep.active_version_id
JOIN agent.triggers tr
  ON tr.tenant_id = d.tenant_id AND tr.agent_id = d.id AND tr.trigger_value = '@agent'
JOIN identity.identity_links l
  ON l.tenant_id = d.tenant_id AND l.member_id = $2::uuid AND l.provisioning_state = 'ready'
WHERE d.tenant_id = $1::uuid AND d.slug = 'knowledge-agent'`, catalogTestTenant, catalogTestMember).Scan(
		&agentID, &versionOneID, &deploymentID, &triggerID, &revision, &senderID,
	); err != nil {
		t.Fatal(err)
	}

	versionTwoSpec := AgentSpec{
		RuntimeKind:        KnowledgeTicketRuntime,
		Instructions:       "Answer only from authorized evidence and identify this as catalog version two.",
		ModelRoute:         DeepSeekV4ProRoute,
		Retrieval:          RetrievalSpec{Purpose: "agent_answer", Limit: 5},
		AllowedActionTypes: []string{"create_ticket"},
		MaxModelAttempts:   3,
	}
	versionTwo, err := publishVersionTx(ctx, tx, catalogTestTenant, agentID, catalogTestMember, 2, versionTwoSpec)
	if err != nil {
		t.Fatal(err)
	}

	eventPrefix := "catalog-integration-" + time.Now().Format("20060102150405.000000000")
	newTrigger := func(suffix, alias, prompt string) Trigger {
		return Trigger{
			EventID: eventPrefix + "-" + suffix, TenantID: catalogTestTenant,
			ConversationID: "si_catalog_test", SenderID: senderID, SessionType: 1,
			Mentions: []Mention{{Alias: alias, Prompt: prompt}},
		}
	}
	newSource := func(offset int64) Source {
		return Source{Topic: "catalog-integration", Partition: 0, Offset: time.Now().UnixNano() + offset}
	}

	versionOneRun, err := enqueueTx(ctx, tx, newSource(1), newTrigger("v1", "@agent", "version one"))
	if err != nil || versionOneRun.RunID == "" {
		t.Fatalf("enqueue v1 = %#v, %v", versionOneRun, err)
	}
	duplicate, err := enqueueTx(ctx, tx, newSource(2), newTrigger("v1", "@agent", "changed prompt"))
	if err != nil || duplicate.RunID != versionOneRun.RunID {
		t.Fatalf("duplicate = %#v, %v", duplicate, err)
	}
	assertPinnedVersion(t, ctx, tx, versionOneRun.RunID, agentID, versionOneID, deploymentID, triggerID, seedAgentSpecChecksum)

	unknown, err := enqueueTx(ctx, tx, newSource(3), newTrigger("unknown", "@unknown", "ignored"))
	if err != nil || unknown.RunID != "" || unknown.RejectionReason != "" {
		t.Fatalf("unknown mention = %#v, %v", unknown, err)
	}

	if err := activateVersionTx(ctx, tx, catalogTestTenant, agentID, versionTwo.VersionID, catalogTestMember, revision); err != nil {
		t.Fatal(err)
	}
	versionTwoRun, err := enqueueTx(ctx, tx, newSource(4), newTrigger("v2", "@agent", "version two"))
	if err != nil || versionTwoRun.RunID == "" {
		t.Fatalf("enqueue v2 = %#v, %v", versionTwoRun, err)
	}
	assertPinnedVersion(t, ctx, tx, versionTwoRun.RunID, agentID, versionTwo.VersionID, deploymentID, triggerID, versionTwo.Checksum)
	assertPinnedVersion(t, ctx, tx, versionOneRun.RunID, agentID, versionOneID, deploymentID, triggerID, seedAgentSpecChecksum)

	if err := activateVersionTx(ctx, tx, catalogTestTenant, agentID, versionOneID, catalogTestMember, revision+1); err != nil {
		t.Fatal(err)
	}
	rolledBackRun, err := enqueueTx(ctx, tx, newSource(5), newTrigger("rollback", "@agent", "version one again"))
	if err != nil || rolledBackRun.RunID == "" {
		t.Fatalf("enqueue rollback = %#v, %v", rolledBackRun, err)
	}
	assertPinnedVersion(t, ctx, tx, rolledBackRun.RunID, agentID, versionOneID, deploymentID, triggerID, seedAgentSpecChecksum)

	if _, err := tx.Exec(ctx, `UPDATE agent.triggers SET enabled = false WHERE id = $1::uuid`, triggerID); err != nil {
		t.Fatal(err)
	}
	disabled, err := enqueueTx(ctx, tx, newSource(6), newTrigger("disabled", "@agent", "must reject"))
	if err != nil || disabled.RejectionReason != "trigger_disabled" || disabled.RunID != "" {
		t.Fatalf("disabled trigger = %#v, %v", disabled, err)
	}
	if _, err := tx.Exec(ctx, `UPDATE agent.triggers SET enabled = true WHERE id = $1::uuid`, triggerID); err != nil {
		t.Fatal(err)
	}

	var auditCount, runCount int
	if err := tx.QueryRow(ctx, `SELECT count(*) FROM audit.agent_catalog_events WHERE deployment_id = $1::uuid`, deploymentID).Scan(&auditCount); err != nil {
		t.Fatal(err)
	}
	if auditCount != 2 {
		t.Fatalf("activation audit events = %d, want 2", auditCount)
	}
	if err := tx.QueryRow(ctx, `SELECT count(*) FROM agent.runs WHERE source_event_id = $1`, eventPrefix+"-v1").Scan(&runCount); err != nil {
		t.Fatal(err)
	}
	if runCount != 1 {
		t.Fatalf("deduplicated runs = %d, want 1", runCount)
	}

	assertVersionImmutable(t, ctx, tx, versionTwo.VersionID)
}

func assertPinnedVersion(t *testing.T, ctx context.Context, tx pgx.Tx, runID, agentID, versionID, deploymentID, triggerID, checksum string) {
	t.Helper()
	var gotAgent, gotVersion, gotDeployment, gotTrigger, gotChecksum string
	if err := tx.QueryRow(ctx, `
SELECT agent_id::text, agent_version_id::text, agent_deployment_id::text,
       agent_trigger_id::text, agent_spec_checksum
FROM agent.runs WHERE id = $1::uuid AND tenant_id = $2::uuid`, runID, catalogTestTenant).Scan(
		&gotAgent, &gotVersion, &gotDeployment, &gotTrigger, &gotChecksum,
	); err != nil {
		t.Fatal(err)
	}
	if gotAgent != agentID || gotVersion != versionID || gotDeployment != deploymentID || gotTrigger != triggerID || gotChecksum != checksum {
		t.Fatalf("pinned catalog = %q %q %q %q %q", gotAgent, gotVersion, gotDeployment, gotTrigger, gotChecksum)
	}
	var crossTenantCount int
	if err := tx.QueryRow(ctx, `
SELECT count(*) FROM agent.runs r
WHERE r.id = $1::uuid AND r.tenant_id <> $2::uuid`, runID, catalogTestTenant).Scan(&crossTenantCount); err != nil {
		t.Fatal(err)
	}
	if crossTenantCount != 0 {
		t.Fatal("Run escaped its tenant boundary")
	}
}

func assertVersionImmutable(t *testing.T, ctx context.Context, tx pgx.Tx, versionID string) {
	t.Helper()
	if _, err := tx.Exec(ctx, "SAVEPOINT immutable_update"); err != nil {
		t.Fatal(err)
	}
	if _, err := tx.Exec(ctx, `UPDATE agent.versions SET spec_checksum = spec_checksum WHERE id = $1::uuid`, versionID); err == nil {
		t.Fatal("published Agent version accepted an update")
	}
	if _, err := tx.Exec(ctx, "ROLLBACK TO SAVEPOINT immutable_update"); err != nil {
		t.Fatal(err)
	}
	if _, err := tx.Exec(ctx, "SAVEPOINT immutable_delete"); err != nil {
		t.Fatal(err)
	}
	if _, err := tx.Exec(ctx, `DELETE FROM agent.versions WHERE id = $1::uuid`, versionID); err == nil {
		t.Fatal("published Agent version accepted a delete")
	}
	if _, err := tx.Exec(ctx, "ROLLBACK TO SAVEPOINT immutable_delete"); err != nil {
		t.Fatal(err)
	}
}
