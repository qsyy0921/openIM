package capability

import (
	"context"
	"crypto/rand"
	"crypto/sha256"
	"encoding/hex"
	"encoding/json"
	"errors"
	"fmt"
	"regexp"
	"strings"

	"github.com/jackc/pgx/v5"
	"github.com/jackc/pgx/v5/pgconn"
)

var skillVersionPattern = regexp.MustCompile(`^[1-9][0-9]{0,8}$`)

type Skill struct {
	ID             string   `json:"id"`
	TenantID       string   `json:"-"`
	SkillID        string   `json:"skill_id"`
	Version        string   `json:"version"`
	Name           string   `json:"name"`
	Summary        string   `json:"summary"`
	Instructions   string   `json:"instructions,omitempty"`
	ToolOperations []string `json:"tool_operations"`
	Audience       string   `json:"audience"`
	ContentDigest  string   `json:"content_digest"`
}

func (s Skill) Validate() error {
	if !operationPattern.MatchString(s.SkillID) || !skillVersionPattern.MatchString(s.Version) ||
		s.Name == "" || len(s.Name) > 120 || s.Summary == "" || len(s.Summary) > 512 ||
		s.Instructions == "" || strings.TrimSpace(s.Instructions) != s.Instructions ||
		len(s.Instructions) > 20000 || len(s.ToolOperations) > 32 ||
		!slicesContains([]string{"passive", "proactive_source", "internal", "admin"}, s.Audience) {
		return errors.New("Skill definition is invalid")
	}
	seen := make(map[string]struct{}, len(s.ToolOperations))
	for _, operation := range s.ToolOperations {
		if !operationPattern.MatchString(operation) {
			return errors.New("Skill tool operation is invalid")
		}
		if _, exists := seen[operation]; exists {
			return errors.New("Skill tool operations contain duplicates")
		}
		seen[operation] = struct{}{}
	}
	return nil
}

func (s Skill) Digest() (string, error) {
	if err := s.Validate(); err != nil {
		return "", err
	}
	value := struct {
		SkillID        string   `json:"skill_id"`
		Version        string   `json:"version"`
		Name           string   `json:"name"`
		Summary        string   `json:"summary"`
		Instructions   string   `json:"instructions"`
		ToolOperations []string `json:"tool_operations"`
		Audience       string   `json:"audience"`
	}{s.SkillID, s.Version, s.Name, s.Summary, s.Instructions, s.ToolOperations, s.Audience}
	encoded, err := json.Marshal(value)
	if err != nil {
		return "", err
	}
	digest := sha256.Sum256(encoded)
	return "sha256:" + hex.EncodeToString(digest[:]), nil
}

func (s *Store) PublishSkill(ctx context.Context, skill Skill) (Skill, error) {
	return publishSkill(ctx, s.pool, skill)
}

type skillQueryer interface {
	QueryRow(context.Context, string, ...any) pgx.Row
	Exec(context.Context, string, ...any) (pgconn.CommandTag, error)
}

func publishSkill(ctx context.Context, database skillQueryer, skill Skill) (Skill, error) {
	if skill.TenantID == "" {
		return Skill{}, errors.New("Skill tenant is required")
	}
	digest, err := skill.Digest()
	if err != nil {
		return Skill{}, err
	}
	if skill.ID == "" {
		skill.ID, err = capabilityUUID()
		if err != nil {
			return Skill{}, err
		}
	}
	var operationCount int
	if err := database.QueryRow(ctx, `
SELECT count(DISTINCT descriptor.operation_id)::integer
FROM capability.tool_descriptors AS descriptor
WHERE descriptor.tenant_id = $1::uuid
  AND descriptor.operation_id = ANY($2::text[])
  AND descriptor.audience = $3`, skill.TenantID, skill.ToolOperations, skill.Audience).Scan(&operationCount); err != nil {
		return Skill{}, fmt.Errorf("validate Skill operations: %w", err)
	}
	if operationCount != len(skill.ToolOperations) {
		return Skill{}, errors.New("Skill references a missing or differently scoped operation")
	}
	const insert = `
INSERT INTO capability.skills (
    id, tenant_id, skill_id, version, name, summary, instructions,
    tool_operations, audience, content_digest
) VALUES ($1::uuid, $2::uuid, $3, $4, $5, $6, $7, $8, $9, $10)
ON CONFLICT (tenant_id, skill_id, version) DO NOTHING`
	result, err := database.Exec(ctx, insert, skill.ID, skill.TenantID, skill.SkillID, skill.Version,
		skill.Name, skill.Summary, skill.Instructions, skill.ToolOperations, skill.Audience, digest)
	if err != nil {
		return Skill{}, fmt.Errorf("publish Skill: %w", err)
	}
	if result.RowsAffected() != 1 {
		return Skill{}, errors.New("Skill version already exists")
	}
	skill.ContentDigest = digest
	return skill, nil
}

func (s *Store) PublishSkillAsAdmin(ctx context.Context, actorMemberID string, skill Skill) (Skill, error) {
	tx, err := s.pool.BeginTx(ctx, pgx.TxOptions{IsoLevel: pgx.Serializable})
	if err != nil {
		return Skill{}, err
	}
	defer func() { _ = tx.Rollback(ctx) }()
	if err := requirePlatformAdmin(ctx, tx, skill.TenantID, actorMemberID); err != nil {
		return Skill{}, err
	}
	published, err := publishSkill(ctx, tx, skill)
	if err != nil {
		return Skill{}, err
	}
	if _, err := tx.Exec(ctx, `
INSERT INTO audit.catalog_admin_events
    (tenant_id, actor_member_id, resource_type, resource_id, event_type, evidence)
VALUES ($1::uuid, $2::uuid, 'skill', $3, 'published',
        jsonb_build_object('skill_id', $4::text, 'version', $5::text, 'content_digest', $6::text))`,
		skill.TenantID, actorMemberID, published.ID, published.SkillID, published.Version, published.ContentDigest); err != nil {
		return Skill{}, err
	}
	if err := tx.Commit(ctx); err != nil {
		return Skill{}, err
	}
	return published, nil
}

func capabilityUUID() (string, error) {
	var value [16]byte
	if _, err := rand.Read(value[:]); err != nil {
		return "", err
	}
	value[6] = (value[6] & 0x0f) | 0x40
	value[8] = (value[8] & 0x3f) | 0x80
	x := hex.EncodeToString(value[:])
	return x[:8] + "-" + x[8:12] + "-" + x[12:16] + "-" + x[16:20] + "-" + x[20:], nil
}

func slicesContains(values []string, target string) bool {
	for _, value := range values {
		if value == target {
			return true
		}
	}
	return false
}
