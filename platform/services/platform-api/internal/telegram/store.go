package telegram

import (
	"context"
	"errors"
	"fmt"

	"github.com/jackc/pgx/v5"
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
