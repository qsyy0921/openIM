package telegram

import (
	"context"
	"errors"
	"fmt"
	"os"
	"testing"
	"time"

	"github.com/jackc/pgx/v5/pgxpool"
)

func TestStoreCreatesAndAtomicallyConsumesLinkChallenge(t *testing.T) {
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

	const tenantID = "e1111111-1111-4111-8111-111111111111"
	const memberID = "e2222222-2222-4222-8222-222222222222"
	const conflictMemberID = "e3333333-3333-4333-8333-333333333333"
	suffix := time.Now().UnixNano()
	telegramUserID := suffix%1_000_000_000 + 8_000_000_000
	telegramChatID := telegramUserID

	if _, err := pool.Exec(ctx, `
INSERT INTO identity.tenants (id, external_id, display_name, status)
VALUES ($1::uuid, $2, 'Telegram Link Integration', 'active')
ON CONFLICT (id) DO UPDATE SET status = 'active'`, tenantID, fmt.Sprintf("telegram-link-%d", suffix)); err != nil {
		t.Fatalf("seed tenant: %v", err)
	}
	if _, err := pool.Exec(ctx, `
INSERT INTO identity.members (id, tenant_id, issuer, subject, display_name, status)
VALUES
  ($2::uuid, $1::uuid, 'https://telegram-link.invalid', $4, 'Telegram Link Member', 'active'),
  ($3::uuid, $1::uuid, 'https://telegram-link.invalid', $5, 'Telegram Conflict Member', 'active')
ON CONFLICT (id) DO UPDATE SET status = 'active'`, tenantID, memberID, conflictMemberID, fmt.Sprintf("member-%d", suffix), fmt.Sprintf("conflict-%d", suffix)); err != nil {
		t.Fatalf("seed members: %v", err)
	}
	t.Cleanup(func() {
		cleanup := context.Background()
		_, _ = pool.Exec(cleanup, "DELETE FROM audit.telegram_link_events WHERE tenant_id = $1::uuid", tenantID)
		_, _ = pool.Exec(cleanup, "DELETE FROM channel.telegram_link_challenges WHERE tenant_id = $1::uuid", tenantID)
		_, _ = pool.Exec(cleanup, "DELETE FROM channel.telegram_chats WHERE telegram_chat_id = $1", telegramChatID)
		_, _ = pool.Exec(cleanup, "DELETE FROM channel.telegram_principals WHERE telegram_user_id = $1", telegramUserID)
		_, _ = pool.Exec(cleanup, "DELETE FROM identity.members WHERE tenant_id = $1::uuid", tenantID)
		_, _ = pool.Exec(cleanup, "DELETE FROM identity.tenants WHERE id = $1::uuid", tenantID)
	})

	store := NewStore(pool)
	first := ChallengeRecord{
		ID: "e4444444-4444-4444-8444-444444444444", TenantID: tenantID, MemberID: memberID,
		CodeDigest: challengeDigest("ABCDEFGHIJKLMNOPQRSTUVWXYZ234567"), ExpiresAt: time.Now().UTC().Add(5 * time.Minute),
	}
	if err := store.CreateLinkChallenge(ctx, first, time.Second); err != nil {
		t.Fatalf("create first challenge: %v", err)
	}
	status, err := store.GetLinkStatus(ctx, tenantID, memberID)
	if err != nil || status.State != "pending" || status.ExpiresAt == nil {
		t.Fatalf("first status = %#v, %v", status, err)
	}
	if err := store.CreateLinkChallenge(ctx, first, time.Second); !errors.Is(err, ErrChallengeRateLimited) {
		t.Fatalf("cooldown error = %v", err)
	}
	if _, err := pool.Exec(ctx, "UPDATE channel.telegram_link_challenges SET created_at = now() - interval '2 seconds' WHERE id = $1::uuid", first.ID); err != nil {
		t.Fatalf("age first challenge: %v", err)
	}
	second := ChallengeRecord{
		ID: "e5555555-5555-4555-8555-555555555555", TenantID: tenantID, MemberID: memberID,
		CodeDigest: challengeDigest("77777777777777777777777777777777"), ExpiresAt: time.Now().UTC().Add(5 * time.Minute),
	}
	if err := store.CreateLinkChallenge(ctx, second, time.Second); err != nil {
		t.Fatalf("rotate challenge: %v", err)
	}
	var revoked bool
	if err := pool.QueryRow(ctx, "SELECT revoked_at IS NOT NULL FROM channel.telegram_link_challenges WHERE id = $1::uuid", first.ID).Scan(&revoked); err != nil || !revoked {
		t.Fatalf("first revoked = %v, %v", revoked, err)
	}

	binding, err := store.ConsumeLinkChallenge(ctx, "7777-7777-7777-7777-7777-7777-7777-7777", telegramUserID, telegramChatID)
	if err != nil {
		t.Fatalf("consume challenge: %v", err)
	}
	if binding.TenantID != tenantID || binding.MemberID != memberID || binding.SessionType != 1 {
		t.Fatalf("binding = %#v", binding)
	}
	status, err = store.GetLinkStatus(ctx, tenantID, memberID)
	if err != nil || status.State != "bound" || status.ExpiresAt != nil {
		t.Fatalf("bound status = %#v, %v", status, err)
	}
	if _, err := store.ConsumeLinkChallenge(ctx, "77777777777777777777777777777777", telegramUserID, telegramChatID); !errors.Is(err, ErrInvalidChallenge) {
		t.Fatalf("replay error = %v", err)
	}
	var issued, revokedEvents, consumed int
	if err := pool.QueryRow(ctx, `
SELECT count(*) FILTER (WHERE event_type = 'issued'),
       count(*) FILTER (WHERE event_type = 'revoked'),
       count(*) FILTER (WHERE event_type = 'consumed')
FROM audit.telegram_link_events
WHERE tenant_id = $1::uuid AND member_id = $2::uuid`, tenantID, memberID).Scan(&issued, &revokedEvents, &consumed); err != nil {
		t.Fatalf("read audit events: %v", err)
	}
	if issued != 2 || revokedEvents != 1 || consumed != 1 {
		t.Fatalf("audit counts = %d/%d/%d", issued, revokedEvents, consumed)
	}

	conflict := ChallengeRecord{
		ID: "e6666666-6666-4666-8666-666666666666", TenantID: tenantID, MemberID: conflictMemberID,
		CodeDigest: challengeDigest("234567ABCDEFGHIJKLMNOPQRSTUVWXYZ"), ExpiresAt: time.Now().UTC().Add(5 * time.Minute),
	}
	if err := store.CreateLinkChallenge(ctx, conflict, time.Second); err != nil {
		t.Fatalf("create conflict challenge: %v", err)
	}
	if _, err := store.ConsumeLinkChallenge(ctx, "234567ABCDEFGHIJKLMNOPQRSTUVWXYZ", telegramUserID, telegramChatID); !errors.Is(err, ErrBindingConflict) {
		t.Fatalf("binding conflict error = %v", err)
	}
	status, err = store.GetLinkStatus(ctx, tenantID, conflictMemberID)
	if err != nil || status.State != "pending" {
		t.Fatalf("conflict rollback status = %#v, %v", status, err)
	}
}
