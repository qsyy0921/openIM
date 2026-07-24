package retrieval

import (
	"context"
	"os"
	"testing"
	"time"

	"github.com/jackc/pgx/v5/pgxpool"
)

func TestRecordProductionEvaluationIsIdempotent(t *testing.T) {
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

	tenantID := testUUID(t)
	if _, err := pool.Exec(ctx, `
INSERT INTO identity.tenants (id, external_id, display_name, status)
VALUES ($1::uuid, $2, 'Evaluation integration tenant', 'active')`,
		tenantID, "evaluation-"+tenantID); err != nil {
		t.Fatal(err)
	}
	config := validProductionEvaluationConfig()
	config.TenantID = tenantID
	report, err := BuildProductionEvaluationReport(
		config,
		validRetrievalEvaluationReport(),
		validGenerationEvaluationReport(),
	)
	if err != nil {
		t.Fatal(err)
	}
	t.Cleanup(func() {
		_, _ = pool.Exec(context.Background(),
			"DELETE FROM knowledge.evaluation_runs WHERE id = $1::uuid",
			report.EvaluationRunID,
		)
		_, _ = pool.Exec(context.Background(),
			"DELETE FROM identity.tenants WHERE id = $1::uuid",
			tenantID,
		)
	})
	first, err := RecordProductionEvaluation(ctx, pool, report)
	if err != nil {
		t.Fatal(err)
	}
	second, err := RecordProductionEvaluation(ctx, pool, report)
	if err != nil {
		t.Fatal(err)
	}
	if first.EvaluationRunID != second.EvaluationRunID || first.RecordedAt != second.RecordedAt {
		t.Fatalf("idempotent records differ: first=%#v second=%#v", first, second)
	}
	var count int
	if err := pool.QueryRow(ctx,
		"SELECT count(*) FROM knowledge.evaluation_runs WHERE id = $1::uuid",
		report.EvaluationRunID,
	).Scan(&count); err != nil {
		t.Fatal(err)
	}
	if count != 1 {
		t.Fatalf("evaluation record count = %d, want 1", count)
	}
}
