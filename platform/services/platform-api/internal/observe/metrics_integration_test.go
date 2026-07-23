package observe

import (
	"context"
	"os"
	"testing"

	"github.com/jackc/pgx/v5/pgxpool"
	"github.com/prometheus/client_golang/prometheus"
	"github.com/prometheus/client_golang/prometheus/testutil"
)

func TestOperationalCollectorAgainstMigratedDatabase(t *testing.T) {
	databaseURL := os.Getenv("PLATFORM_TEST_DATABASE_URL")
	if databaseURL == "" {
		t.Skip("PLATFORM_TEST_DATABASE_URL is not set")
	}
	pool, err := pgxpool.New(context.Background(), databaseURL)
	if err != nil {
		t.Fatalf("configure PostgreSQL: %v", err)
	}
	defer pool.Close()
	if err := pool.Ping(context.Background()); err != nil {
		t.Fatalf("connect PostgreSQL: %v", err)
	}

	before := testutil.ToFloat64(collectorErrors)
	metrics := make(chan prometheus.Metric, 1024)
	newOperationalCollector(pool).Collect(metrics)
	close(metrics)
	after := testutil.ToFloat64(collectorErrors)
	if after != before {
		t.Fatalf("collector errors increased from %v to %v", before, after)
	}
	if count := len(metrics); count == 0 {
		t.Fatal("operational collector emitted no metrics")
	}
}
