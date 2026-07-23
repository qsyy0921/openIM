package telegram

import (
	"context"
	"errors"
	"fmt"
	"regexp"
	"time"

	"github.com/jackc/pgx/v5"
	"github.com/jackc/pgx/v5/pgconn"
	"github.com/jackc/pgx/v5/pgxpool"
)

var ErrUnbound = errors.New("Telegram principal or chat is not bound")

type Binding struct {
	TenantID    string
	MemberID    string
	SessionType int32
}

type Store struct{ pool *pgxpool.Pool }

func NewStore(pool *pgxpool.Pool) *Store { return &Store{pool: pool} }

var sha256DigestPattern = regexp.MustCompile(`^[0-9a-f]{64}$`)

func (s *Store) GetLinkStatus(ctx context.Context, tenantID, memberID string) (LinkStatus, error) {
	if tenantID == "" || memberID == "" {
		return LinkStatus{}, errors.New("Telegram link status identity is invalid")
	}
	var bound bool
	if err := s.pool.QueryRow(ctx, `
SELECT EXISTS (
    SELECT 1
    FROM channel.telegram_principals
    WHERE tenant_id = $1::uuid AND member_id = $2::uuid
)`, tenantID, memberID).Scan(&bound); err != nil {
		return LinkStatus{}, fmt.Errorf("read Telegram binding status: %w", err)
	}
	if bound {
		return LinkStatus{State: "bound"}, nil
	}
	var expiresAt *time.Time
	if err := s.pool.QueryRow(ctx, `
SELECT (
    SELECT expires_at
    FROM channel.telegram_link_challenges
    WHERE tenant_id = $1::uuid AND member_id = $2::uuid
      AND consumed_at IS NULL AND revoked_at IS NULL AND expires_at > now()
    ORDER BY created_at DESC
    LIMIT 1
)`, tenantID, memberID).Scan(&expiresAt); err != nil {
		return LinkStatus{}, fmt.Errorf("read Telegram challenge status: %w", err)
	}
	if expiresAt != nil {
		value := expiresAt.UTC()
		return LinkStatus{State: "pending", ExpiresAt: &value}, nil
	}
	return LinkStatus{State: "unbound"}, nil
}

func (s *Store) CreateLinkChallenge(ctx context.Context, challenge ChallengeRecord, cooldown time.Duration) error {
	if challenge.ID == "" || challenge.TenantID == "" || challenge.MemberID == "" ||
		!sha256DigestPattern.MatchString(challenge.CodeDigest) || challenge.ExpiresAt.IsZero() || cooldown <= 0 {
		return errors.New("Telegram link challenge fields are invalid")
	}
	tx, err := s.pool.BeginTx(ctx, pgx.TxOptions{IsoLevel: pgx.Serializable})
	if err != nil {
		return fmt.Errorf("begin Telegram link challenge: %w", err)
	}
	defer func() { _ = tx.Rollback(ctx) }()
	var active bool
	if err := tx.QueryRow(ctx, `
SELECT EXISTS (
    SELECT 1 FROM identity.members AS m
    JOIN identity.tenants AS t ON t.id = m.tenant_id
    WHERE m.tenant_id = $1::uuid AND m.id = $2::uuid
      AND m.status = 'active' AND t.status = 'active'
    FOR UPDATE
)`, challenge.TenantID, challenge.MemberID).Scan(&active); err != nil {
		return fmt.Errorf("validate Telegram link member: %w", err)
	}
	if !active {
		return errors.New("Telegram link member is not active")
	}
	var bound bool
	if err := tx.QueryRow(ctx, `
SELECT EXISTS (
    SELECT 1 FROM channel.telegram_principals
    WHERE tenant_id = $1::uuid AND member_id = $2::uuid
)`, challenge.TenantID, challenge.MemberID).Scan(&bound); err != nil {
		return fmt.Errorf("check existing Telegram member binding: %w", err)
	}
	if bound {
		return ErrAlreadyBound
	}
	cooldownSeconds := int64((cooldown + time.Second - 1) / time.Second)
	var rateLimited bool
	if err := tx.QueryRow(ctx, `
SELECT EXISTS (
    SELECT 1 FROM channel.telegram_link_challenges
    WHERE tenant_id = $1::uuid AND member_id = $2::uuid
      AND created_at > now() - make_interval(secs => $3)
)`, challenge.TenantID, challenge.MemberID, cooldownSeconds).Scan(&rateLimited); err != nil {
		return fmt.Errorf("check Telegram challenge cooldown: %w", err)
	}
	if rateLimited {
		return ErrChallengeRateLimited
	}
	if _, err := tx.Exec(ctx, `
WITH revoked AS (
    UPDATE channel.telegram_link_challenges
    SET revoked_at = now()
    WHERE tenant_id = $1::uuid AND member_id = $2::uuid
      AND consumed_at IS NULL AND revoked_at IS NULL
    RETURNING id, tenant_id, member_id
)
INSERT INTO audit.telegram_link_events (challenge_id, tenant_id, member_id, event_type, evidence)
SELECT id, tenant_id, member_id, 'revoked', '{"reason":"rotated"}'::jsonb
FROM revoked`, challenge.TenantID, challenge.MemberID); err != nil {
		return fmt.Errorf("revoke prior Telegram challenge: %w", err)
	}
	if _, err := tx.Exec(ctx, `
INSERT INTO channel.telegram_link_challenges (id, tenant_id, member_id, code_digest, expires_at)
VALUES ($1::uuid, $2::uuid, $3::uuid, $4, $5)`, challenge.ID, challenge.TenantID, challenge.MemberID, challenge.CodeDigest, challenge.ExpiresAt.UTC()); err != nil {
		return fmt.Errorf("insert Telegram link challenge: %w", err)
	}
	if _, err := tx.Exec(ctx, `
INSERT INTO audit.telegram_link_events (challenge_id, tenant_id, member_id, event_type, evidence)
VALUES ($1::uuid, $2::uuid, $3::uuid, 'issued', '{"source":"member_api"}'::jsonb)`, challenge.ID, challenge.TenantID, challenge.MemberID); err != nil {
		return fmt.Errorf("audit Telegram link challenge: %w", err)
	}
	if err := tx.Commit(ctx); err != nil {
		return fmt.Errorf("commit Telegram link challenge: %w", err)
	}
	return nil
}

func (s *Store) ConsumeLinkChallenge(ctx context.Context, code string, userID, chatID int64) (Binding, error) {
	canonical, ok := normalizeChallengeCode(code)
	if !ok || userID <= 0 || chatID == 0 {
		return Binding{}, ErrInvalidChallenge
	}
	digest := challengeDigest(canonical)
	tx, err := s.pool.BeginTx(ctx, pgx.TxOptions{IsoLevel: pgx.Serializable})
	if err != nil {
		return Binding{}, fmt.Errorf("begin Telegram link consumption: %w", err)
	}
	defer func() { _ = tx.Rollback(ctx) }()
	binding := Binding{SessionType: 1}
	var challengeID string
	if err := tx.QueryRow(ctx, `
SELECT c.id::text, c.tenant_id::text, c.member_id::text
FROM channel.telegram_link_challenges AS c
JOIN identity.tenants AS t ON t.id = c.tenant_id AND t.status = 'active'
JOIN identity.members AS m ON m.tenant_id = c.tenant_id AND m.id = c.member_id AND m.status = 'active'
WHERE c.code_digest = $1
  AND c.consumed_at IS NULL AND c.revoked_at IS NULL AND c.expires_at > now()
FOR UPDATE OF c`, digest).Scan(&challengeID, &binding.TenantID, &binding.MemberID); err != nil {
		if errors.Is(err, pgx.ErrNoRows) {
			return Binding{}, ErrInvalidChallenge
		}
		return Binding{}, fmt.Errorf("read Telegram link challenge: %w", err)
	}
	if _, err := tx.Exec(ctx, `
INSERT INTO channel.telegram_principals (telegram_user_id, tenant_id, member_id)
VALUES ($1, $2::uuid, $3::uuid)`, userID, binding.TenantID, binding.MemberID); err != nil {
		if isUniqueViolation(err) {
			return Binding{}, ErrBindingConflict
		}
		return Binding{}, fmt.Errorf("bind Telegram principal from challenge: %w", err)
	}
	if _, err := tx.Exec(ctx, `
INSERT INTO channel.telegram_chats (telegram_chat_id, tenant_id, session_type)
VALUES ($1, $2::uuid, 1)`, chatID, binding.TenantID); err != nil {
		if isUniqueViolation(err) {
			return Binding{}, ErrBindingConflict
		}
		return Binding{}, fmt.Errorf("bind Telegram private chat from challenge: %w", err)
	}
	result, err := tx.Exec(ctx, `
UPDATE channel.telegram_link_challenges
SET consumed_at = now(), telegram_user_id = $2, telegram_chat_id = $3
WHERE id = $1::uuid AND consumed_at IS NULL AND revoked_at IS NULL AND expires_at > now()`, challengeID, userID, chatID)
	if err != nil {
		return Binding{}, fmt.Errorf("consume Telegram link challenge: %w", err)
	}
	if result.RowsAffected() != 1 {
		return Binding{}, ErrInvalidChallenge
	}
	if _, err := tx.Exec(ctx, `
INSERT INTO audit.telegram_link_events (challenge_id, tenant_id, member_id, event_type, evidence)
VALUES ($1::uuid, $2::uuid, $3::uuid, 'consumed', '{"source":"telegram_private_chat"}'::jsonb)`, challengeID, binding.TenantID, binding.MemberID); err != nil {
		return Binding{}, fmt.Errorf("audit Telegram link consumption: %w", err)
	}
	if err := tx.Commit(ctx); err != nil {
		return Binding{}, fmt.Errorf("commit Telegram link consumption: %w", err)
	}
	return binding, nil
}

func isUniqueViolation(err error) bool {
	var databaseError *pgconn.PgError
	return errors.As(err, &databaseError) && databaseError.Code == "23505"
}

func (s *Store) ResolveBinding(ctx context.Context, userID, chatID int64) (Binding, error) {
	const query = `
SELECT p.tenant_id::text, p.member_id::text, c.session_type
FROM channel.telegram_principals AS p
JOIN channel.telegram_chats AS c ON c.tenant_id = p.tenant_id
JOIN identity.tenants AS t ON t.id = p.tenant_id AND t.status = 'active'
JOIN identity.members AS m ON m.tenant_id = p.tenant_id AND m.id = p.member_id AND m.status = 'active'
WHERE p.telegram_user_id = $1 AND c.telegram_chat_id = $2
  AND p.enabled AND c.enabled`
	var binding Binding
	if err := s.pool.QueryRow(ctx, query, userID, chatID).Scan(&binding.TenantID, &binding.MemberID, &binding.SessionType); err != nil {
		if errors.Is(err, pgx.ErrNoRows) {
			return Binding{}, ErrUnbound
		}
		return Binding{}, fmt.Errorf("resolve Telegram binding: %w", err)
	}
	return binding, nil
}

func (s *Store) LoadOffset(ctx context.Context, botID int64) (int64, error) {
	var offset int64
	err := s.pool.QueryRow(ctx, `
INSERT INTO channel.telegram_offsets (telegram_bot_id)
VALUES ($1)
ON CONFLICT (telegram_bot_id) DO UPDATE SET telegram_bot_id = EXCLUDED.telegram_bot_id
RETURNING next_update_id`, botID).Scan(&offset)
	if err != nil {
		return 0, fmt.Errorf("load Telegram offset: %w", err)
	}
	return offset, nil
}

func (s *Store) SaveOffset(ctx context.Context, botID, nextUpdateID int64) error {
	result, err := s.pool.Exec(ctx, `
UPDATE channel.telegram_offsets
SET next_update_id = GREATEST(next_update_id, $2), updated_at = now()
WHERE telegram_bot_id = $1`, botID, nextUpdateID)
	if err != nil {
		return fmt.Errorf("save Telegram offset: %w", err)
	}
	if result.RowsAffected() != 1 {
		return errors.New("save Telegram offset: Bot offset row is missing")
	}
	return nil
}

func (s *Store) Bind(ctx context.Context, userID, chatID int64, tenantID, memberID string, sessionType int32) error {
	if userID <= 0 || chatID == 0 || tenantID == "" || memberID == "" || (sessionType != 1 && sessionType != 2) {
		return errors.New("Telegram binding fields are invalid")
	}
	tx, err := s.pool.BeginTx(ctx, pgx.TxOptions{IsoLevel: pgx.Serializable})
	if err != nil {
		return fmt.Errorf("begin Telegram binding: %w", err)
	}
	defer func() { _ = tx.Rollback(ctx) }()
	var active bool
	if err := tx.QueryRow(ctx, `
SELECT EXISTS (
    SELECT 1 FROM identity.members m
    JOIN identity.tenants t ON t.id = m.tenant_id
    WHERE m.id = $1::uuid AND m.tenant_id = $2::uuid
      AND m.status = 'active' AND t.status = 'active'
)`, memberID, tenantID).Scan(&active); err != nil {
		return fmt.Errorf("validate Telegram binding member: %w", err)
	}
	if !active {
		return errors.New("Telegram binding member is not active")
	}
	principal, err := tx.Exec(ctx, `
INSERT INTO channel.telegram_principals (telegram_user_id, tenant_id, member_id)
VALUES ($1, $2::uuid, $3::uuid)
ON CONFLICT (telegram_user_id) DO UPDATE
SET updated_at = now()
WHERE channel.telegram_principals.tenant_id = EXCLUDED.tenant_id
  AND channel.telegram_principals.member_id = EXCLUDED.member_id`, userID, tenantID, memberID)
	if err != nil {
		return fmt.Errorf("bind Telegram principal: %w", err)
	}
	if principal.RowsAffected() != 1 {
		return errors.New("Telegram principal is already bound to a different member")
	}
	chat, err := tx.Exec(ctx, `
INSERT INTO channel.telegram_chats (telegram_chat_id, tenant_id, session_type)
VALUES ($1, $2::uuid, $3)
ON CONFLICT (telegram_chat_id) DO UPDATE
SET updated_at = now()
WHERE channel.telegram_chats.tenant_id = EXCLUDED.tenant_id
  AND channel.telegram_chats.session_type = EXCLUDED.session_type`, chatID, tenantID, sessionType)
	if err != nil {
		return fmt.Errorf("bind Telegram chat: %w", err)
	}
	if chat.RowsAffected() != 1 {
		return errors.New("Telegram chat is already bound to a different tenant or session type")
	}
	if err := tx.Commit(ctx); err != nil {
		return fmt.Errorf("commit Telegram binding: %w", err)
	}
	return nil
}
