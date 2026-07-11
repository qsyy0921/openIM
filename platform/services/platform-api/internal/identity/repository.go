package identity

import (
	"context"
	"crypto/rand"
	"crypto/sha256"
	"encoding/base32"
	"encoding/hex"
	"errors"
	"fmt"
	"strings"
	"time"

	"github.com/jackc/pgx/v5"
	"github.com/jackc/pgx/v5/pgxpool"
)

type PostgresStore struct {
	pool *pgxpool.Pool
}

func NewPostgresStore(pool *pgxpool.Pool) *PostgresStore {
	return &PostgresStore{pool: pool}
}

func (s *PostgresStore) ResolveActiveMember(
	ctx context.Context,
	principal Principal,
	deviceID string,
	platformID int32,
) (Member, error) {
	const query = `
SELECT m.id::text, m.tenant_id::text, m.display_name
FROM identity.members AS m
JOIN identity.tenants AS t ON t.id = m.tenant_id
JOIN identity.member_devices AS d ON d.member_id = m.id
WHERE m.issuer = $1
  AND m.subject = $2
  AND t.external_id = $3
  AND m.status = 'active'
  AND t.status = 'active'
  AND d.device_id = $4
  AND d.platform_id = $5
  AND d.status = 'active'`

	var member Member
	err := s.pool.QueryRow(ctx, query,
		principal.Issuer,
		principal.Subject,
		principal.TenantExternalID,
		deviceID,
		platformID,
	).Scan(&member.ID, &member.TenantID, &member.DisplayName)
	if errors.Is(err, pgx.ErrNoRows) {
		return Member{}, ErrForbidden
	}
	if err != nil {
		return Member{}, fmt.Errorf("resolve active member: %w", err)
	}
	return member, nil
}

func (s *PostgresStore) AcquireLink(
	ctx context.Context,
	member Member,
	leaseDuration time.Duration,
) (Link, bool, error) {
	userID := deterministicOpenIMUserID(member.TenantID, member.ID)
	const insert = `
INSERT INTO identity.identity_links (
  tenant_id, member_id, openim_user_id, provisioning_state
) VALUES ($1::uuid, $2::uuid, $3, 'pending')
ON CONFLICT (member_id) DO NOTHING`
	if _, err := s.pool.Exec(ctx, insert, member.TenantID, member.ID, userID); err != nil {
		return Link{}, false, fmt.Errorf("ensure identity link: %w", err)
	}

	leaseToken, err := newLeaseToken()
	if err != nil {
		return Link{}, false, err
	}
	leaseSeconds := int64((leaseDuration + time.Second - 1) / time.Second)
	const acquire = `
UPDATE identity.identity_links
SET provisioning_state = 'provisioning',
    lease_until = now() + make_interval(secs => $2),
    lease_token = $3,
    updated_at = now()
WHERE member_id = $1::uuid
  AND (
    provisioning_state = 'pending'
    OR (provisioning_state = 'provisioning' AND lease_until < now())
  )
RETURNING openim_user_id, provisioning_state, lease_token`
	var link Link
	err = s.pool.QueryRow(ctx, acquire, member.ID, leaseSeconds, leaseToken).Scan(&link.OpenIMUserID, &link.State, &link.LeaseToken)
	if err == nil {
		return link, true, nil
	}
	if !errors.Is(err, pgx.ErrNoRows) {
		return Link{}, false, fmt.Errorf("acquire identity link: %w", err)
	}

	const read = `
SELECT openim_user_id, provisioning_state
FROM identity.identity_links
WHERE member_id = $1::uuid`
	if err := s.pool.QueryRow(ctx, read, member.ID).Scan(&link.OpenIMUserID, &link.State); err != nil {
		return Link{}, false, fmt.Errorf("read identity link: %w", err)
	}
	return link, false, nil
}

func (s *PostgresStore) MarkLinkReady(ctx context.Context, memberID, leaseToken string) error {
	const query = `
UPDATE identity.identity_links
SET provisioning_state = 'ready', provisioned_at = now(), lease_until = NULL, lease_token = NULL, updated_at = now()
WHERE member_id = $1::uuid
  AND provisioning_state = 'provisioning'
  AND lease_token = $2
  AND lease_until >= now()`
	result, err := s.pool.Exec(ctx, query, memberID, leaseToken)
	if err != nil {
		return fmt.Errorf("mark identity link ready: %w", err)
	}
	if result.RowsAffected() != 1 {
		return errors.New("mark identity link ready: provisioning lease is not held")
	}
	return nil
}

func (s *PostgresStore) ReleaseLink(ctx context.Context, memberID, leaseToken string) error {
	const query = `
UPDATE identity.identity_links
SET provisioning_state = 'pending', lease_until = NULL, lease_token = NULL, updated_at = now()
WHERE member_id = $1::uuid AND provisioning_state = 'provisioning' AND lease_token = $2`
	_, err := s.pool.Exec(ctx, query, memberID, leaseToken)
	if err != nil {
		return fmt.Errorf("release identity link: %w", err)
	}
	return nil
}

func newLeaseToken() (string, error) {
	var value [16]byte
	if _, err := rand.Read(value[:]); err != nil {
		return "", fmt.Errorf("generate provisioning lease token: %w", err)
	}
	return hex.EncodeToString(value[:]), nil
}

func deterministicOpenIMUserID(tenantID, memberID string) string {
	digest := sha256.Sum256([]byte(tenantID + "\x00" + memberID))
	encoded := base32.StdEncoding.WithPadding(base32.NoPadding).EncodeToString(digest[:20])
	return "ent_" + strings.ToLower(encoded)
}
