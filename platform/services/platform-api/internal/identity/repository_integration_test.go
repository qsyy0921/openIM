package identity

import (
	"context"
	"os"
	"sync"
	"sync/atomic"
	"testing"
	"time"

	"github.com/jackc/pgx/v5/pgxpool"
)

func TestPostgresStoreConcurrentLinkAcquisition(t *testing.T) {
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

	const tenantID = "cccccccc-cccc-4ccc-8ccc-cccccccccccc"
	const memberID = "dddddddd-dddd-4ddd-8ddd-dddddddddddd"
	_, err = pool.Exec(ctx, `
INSERT INTO identity.tenants (id, external_id, display_name, status)
VALUES ($1, 'repository-integration-tenant', 'Repository Integration Tenant', 'active')

ON CONFLICT (id) DO NOTHING`, tenantID)
	if err != nil {
		t.Fatalf("seed integration tenant: %v", err)
	}
	_, err = pool.Exec(ctx, `
INSERT INTO identity.members (id, tenant_id, issuer, subject, display_name, status)
VALUES ($2, $1, 'https://integration.invalid', 'subject', 'Integration Member', 'active')
ON CONFLICT (id) DO NOTHING`, tenantID, memberID)
	if err != nil {
		t.Fatalf("seed integration member: %v", err)
	}
	if _, err := pool.Exec(ctx, "DELETE FROM identity.identity_links WHERE member_id = $1", memberID); err != nil {
		t.Fatalf("reset integration link: %v", err)
	}
	t.Cleanup(func() {
		_, _ = pool.Exec(context.Background(), "DELETE FROM identity.members WHERE id = $1", memberID)
		_, _ = pool.Exec(context.Background(), "DELETE FROM identity.tenants WHERE id = $1", tenantID)
	})

	store := NewPostgresStore(pool)
	member := Member{ID: memberID, TenantID: tenantID, DisplayName: "Integration Member"}
	const workers = 16
	var acquired atomic.Int32
	var winner Link
	var winnerMu sync.Mutex
	start := make(chan struct{})
	errs := make(chan error, workers)
	var wg sync.WaitGroup
	for range workers {
		wg.Add(1)
		go func() {
			defer wg.Done()
			<-start
			link, ownsLease, err := store.AcquireLink(ctx, member, 15*time.Second)
			if err != nil {
				errs <- err
				return
			}
			if ownsLease {
				acquired.Add(1)
				winnerMu.Lock()
				winner = link
				winnerMu.Unlock()
			}
		}()
	}
	close(start)
	wg.Wait()
	close(errs)
	for err := range errs {
		t.Errorf("AcquireLink() error = %v", err)
	}
	if acquired.Load() != 1 {
		t.Fatalf("lease owners = %d, want 1", acquired.Load())
	}
	if err := store.MarkLinkReady(ctx, memberID, "stale-lease-token"); err == nil {
		t.Fatal("stale lease token committed the identity link")
	}
	if err := store.MarkLinkReady(ctx, memberID, winner.LeaseToken); err != nil {
		t.Fatalf("winner MarkLinkReady() error = %v", err)
	}
}
