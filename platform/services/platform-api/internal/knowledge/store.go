package knowledge

import (
	"context"
	"crypto/rand"
	"crypto/sha256"
	"encoding/hex"
	"errors"
	"fmt"
	"math"
	"slices"
	"strconv"
	"strings"
	"time"

	"github.com/jackc/pgx/v5"
	"github.com/jackc/pgx/v5/pgxpool"
)

type StoreConfig struct {
	Bucket             string
	ParserRevision     string
	EmbeddingRevision  string
	EmbeddingDimension int
	MaxAttempts        int
}

type Store struct {
	pool   *pgxpool.Pool
	config StoreConfig
}

func NewStore(pool *pgxpool.Pool, config StoreConfig) (*Store, error) {
	if pool == nil || len(config.Bucket) < 3 || len(config.Bucket) > 63 ||
		strings.TrimSpace(config.ParserRevision) == "" ||
		strings.TrimSpace(config.EmbeddingRevision) == "" ||
		config.EmbeddingDimension != 2560 || config.MaxAttempts < 1 || config.MaxAttempts > 8 {
		return nil, errors.New("knowledge store configuration is invalid")
	}
	return &Store{pool: pool, config: config}, nil
}

func (s *Store) ReserveUpload(ctx context.Context, command UploadCommand) (UploadReservation, error) {
	command.Title = strings.TrimSpace(command.Title)
	command.Classification = strings.TrimSpace(command.Classification)
	command.IdempotencyKey = strings.TrimSpace(command.IdempotencyKey)
	if command.TenantID == "" || command.ActorMemberID == "" ||
		len(command.IdempotencyKey) < 16 || len(command.IdempotencyKey) > 200 ||
		len(command.Title) < 1 || len(command.Title) > 300 ||
		!slices.Contains([]string{"public", "internal", "confidential", "restricted"}, command.Classification) {
		return UploadReservation{}, ErrInvalidInput
	}
	if err := ValidateUploadMetadata(command.File); err != nil {
		return UploadReservation{}, err
	}
	keyDigest := digest(command.IdempotencyKey)
	requestDigest := digest(strings.Join([]string{
		command.DocumentID,
		command.Title,
		command.Classification,
		command.File.OriginalFilename,
		string(command.File.Format),
		strconv.FormatInt(command.File.Size, 10),
		command.File.Checksum,
	}, "\x00"))
	tx, err := s.pool.BeginTx(ctx, pgx.TxOptions{IsoLevel: pgx.Serializable})
	if err != nil {
		return UploadReservation{}, fmt.Errorf("begin knowledge upload reservation: %w", err)
	}
	defer func() { _ = tx.Rollback(ctx) }()

	existing, found, err := s.readIdempotentUpload(ctx, tx, command.TenantID, keyDigest)
	if err != nil {
		return UploadReservation{}, err
	}
	if found {
		if existing.RequestDigest != requestDigest {
			return UploadReservation{}, ErrIdempotencyConflict
		}
		existing.TenantID = command.TenantID
		existing.Idempotent = true
		if err := tx.Commit(ctx); err != nil {
			return UploadReservation{}, fmt.Errorf("commit idempotent knowledge upload: %w", err)
		}
		return existing, nil
	}

	documentID := strings.TrimSpace(command.DocumentID)
	versionNumber := 1
	if documentID == "" {
		documentID, err = randomUUID()
		if err != nil {
			return UploadReservation{}, err
		}
		if _, err := tx.Exec(ctx, `
INSERT INTO knowledge.documents (id, tenant_id, title, source_uri, classification, status)
VALUES ($1::uuid, $2::uuid, $3, $4, $5, 'active')`,
			documentID, command.TenantID, command.Title, "knowledge://"+documentID, command.Classification); err != nil {
			return UploadReservation{}, fmt.Errorf("create knowledge document: %w", err)
		}
	} else {
		var title, classification, status string
		if err := tx.QueryRow(ctx, `
SELECT title, classification, status
FROM knowledge.documents
WHERE tenant_id = $1::uuid AND id = $2::uuid
FOR UPDATE`, command.TenantID, documentID).Scan(&title, &classification, &status); err != nil {
			if errors.Is(err, pgx.ErrNoRows) {
				return UploadReservation{}, ErrNotFound
			}
			return UploadReservation{}, fmt.Errorf("lock knowledge document: %w", err)
		}
		if status != "active" || title != command.Title || classification != command.Classification {
			return UploadReservation{}, ErrConflict
		}
		var duplicate UploadReservation
		err := tx.QueryRow(ctx, `
SELECT v.document_id::text, v.id::text, v.version_number, v.ingestion_state,
       source.bucket, source.object_key
FROM knowledge.document_versions AS v
JOIN knowledge.document_source_objects AS source ON source.version_id = v.id
WHERE v.tenant_id = $1::uuid AND v.document_id = $2::uuid AND v.checksum = $3
ORDER BY v.version_number DESC
LIMIT 1`, command.TenantID, documentID, command.File.Checksum).Scan(
			&duplicate.DocumentID, &duplicate.VersionID, &duplicate.VersionNumber,
			&duplicate.IngestionState, &duplicate.Bucket, &duplicate.ObjectKey,
		)
		if err == nil {
			if _, err := tx.Exec(ctx, `
INSERT INTO knowledge.upload_idempotency
    (tenant_id, key_digest, request_digest, document_id, version_id)
VALUES ($1::uuid, $2, $3, $4::uuid, $5::uuid)`,
				command.TenantID, keyDigest, requestDigest, duplicate.DocumentID, duplicate.VersionID); err != nil {
				return UploadReservation{}, fmt.Errorf("record checksum-idempotent knowledge upload: %w", err)
			}
			duplicate.RequestDigest = requestDigest
			duplicate.TenantID = command.TenantID
			duplicate.Idempotent = true
			if err := tx.Commit(ctx); err != nil {
				return UploadReservation{}, fmt.Errorf("commit checksum-idempotent knowledge upload: %w", err)
			}
			return duplicate, nil
		}
		if !errors.Is(err, pgx.ErrNoRows) {
			return UploadReservation{}, fmt.Errorf("inspect duplicate knowledge version: %w", err)
		}
		if err := tx.QueryRow(ctx, `
SELECT COALESCE(max(version_number), 0) + 1
FROM knowledge.document_versions
WHERE tenant_id = $1::uuid AND document_id = $2::uuid`, command.TenantID, documentID).Scan(&versionNumber); err != nil {
			return UploadReservation{}, fmt.Errorf("allocate knowledge version number: %w", err)
		}
	}
	versionID, err := randomUUID()
	if err != nil {
		return UploadReservation{}, err
	}
	objectKey := strings.Join([]string{command.TenantID, documentID, versionID, "source"}, "/")
	if _, err := tx.Exec(ctx, `
INSERT INTO knowledge.document_versions
    (id, tenant_id, document_id, version_number, checksum, status, ingestion_state)
VALUES ($1::uuid, $2::uuid, $3::uuid, $4, $5, 'draft', 'uploading')`,
		versionID, command.TenantID, documentID, versionNumber, command.File.Checksum); err != nil {
		return UploadReservation{}, fmt.Errorf("create knowledge document version: %w", err)
	}
	if _, err := tx.Exec(ctx, `
INSERT INTO knowledge.document_source_objects
    (version_id, tenant_id, document_id, bucket, object_key, original_filename,
     media_type, source_format, size_bytes, checksum, state)
VALUES ($1::uuid, $2::uuid, $3::uuid, $4, $5, $6, $7, $8, $9, $10, 'reserved')`,
		versionID, command.TenantID, documentID, s.config.Bucket, objectKey,
		command.File.OriginalFilename, command.File.DeclaredType, command.File.Format,
		command.File.Size, command.File.Checksum); err != nil {
		return UploadReservation{}, fmt.Errorf("reserve knowledge source object: %w", err)
	}
	if _, err := tx.Exec(ctx, `
INSERT INTO knowledge.upload_idempotency
    (tenant_id, key_digest, request_digest, document_id, version_id)
VALUES ($1::uuid, $2, $3, $4::uuid, $5::uuid)`,
		command.TenantID, keyDigest, requestDigest, documentID, versionID); err != nil {
		return UploadReservation{}, fmt.Errorf("record knowledge upload idempotency: %w", err)
	}
	if err := auditKnowledge(ctx, tx, command.TenantID, command.ActorMemberID, documentID, versionID, "", "upload_reserved",
		`jsonb_build_object('format', $6::text, 'size_bytes', $7::bigint)`, command.File.Format, command.File.Size); err != nil {
		return UploadReservation{}, err
	}
	if err := tx.Commit(ctx); err != nil {
		return UploadReservation{}, fmt.Errorf("commit knowledge upload reservation: %w", err)
	}
	return UploadReservation{
		UploadResult: UploadResult{
			DocumentID: documentID, VersionID: versionID, VersionNumber: versionNumber, IngestionState: "uploading",
		},
		TenantID: command.TenantID, Bucket: s.config.Bucket, ObjectKey: objectKey, RequestDigest: requestDigest,
	}, nil
}

func (s *Store) CompleteUpload(ctx context.Context, reservation UploadReservation, actorMemberID string) error {
	jobID, err := randomUUID()
	if err != nil {
		return err
	}
	tx, err := s.pool.BeginTx(ctx, pgx.TxOptions{IsoLevel: pgx.Serializable})
	if err != nil {
		return fmt.Errorf("begin knowledge upload completion: %w", err)
	}
	defer func() { _ = tx.Rollback(ctx) }()
	var state, ingestionState string
	var jobExists bool
	if err := tx.QueryRow(ctx, `
SELECT source.state, version.ingestion_state,
       EXISTS (SELECT 1 FROM knowledge.ingestion_jobs AS job WHERE job.version_id = version.id)
FROM knowledge.document_source_objects AS source
JOIN knowledge.document_versions AS version ON version.id = source.version_id
WHERE source.tenant_id = $1::uuid AND source.document_id = $2::uuid AND source.version_id = $3::uuid
FOR UPDATE OF source, version`,
		reservation.TenantID, reservation.DocumentID, reservation.VersionID).Scan(&state, &ingestionState, &jobExists); err != nil {
		return fmt.Errorf("lock knowledge source object: %w", err)
	}
	tenantID := reservation.TenantID
	if state == "stored" && jobExists &&
		(ingestionState == "queued" || ingestionState == "processing" || ingestionState == "indexed") {
		if err := tx.Commit(ctx); err != nil {
			return fmt.Errorf("commit reconciled knowledge upload: %w", err)
		}
		return nil
	}
	if state != "reserved" {
		return ErrConflict
	}
	if _, err := tx.Exec(ctx, `
UPDATE knowledge.document_source_objects
SET state = 'stored', stored_at = now(), updated_at = now()
WHERE tenant_id = $1::uuid AND version_id = $2::uuid AND state = 'reserved'`, tenantID, reservation.VersionID); err != nil {
		return fmt.Errorf("mark knowledge source stored: %w", err)
	}
	if _, err := tx.Exec(ctx, `
UPDATE knowledge.document_versions
SET ingestion_state = 'queued', updated_at = now()
WHERE tenant_id = $1::uuid AND id = $2::uuid AND ingestion_state = 'uploading'`, tenantID, reservation.VersionID); err != nil {
		return fmt.Errorf("queue knowledge version: %w", err)
	}
	if _, err := tx.Exec(ctx, `
INSERT INTO knowledge.ingestion_jobs
    (id, tenant_id, document_id, version_id, state, max_attempts,
     parser_revision, embedding_revision, embedding_dimension)
VALUES ($1::uuid, $2::uuid, $3::uuid, $4::uuid, 'queued', $5, $6, $7, $8)`,
		jobID, tenantID, reservation.DocumentID, reservation.VersionID, s.config.MaxAttempts,
		s.config.ParserRevision, s.config.EmbeddingRevision, s.config.EmbeddingDimension); err != nil {
		return fmt.Errorf("create knowledge ingestion job: %w", err)
	}
	if err := auditKnowledge(ctx, tx, tenantID, actorMemberID, reservation.DocumentID, reservation.VersionID, jobID,
		"upload_queued", `'{}'::jsonb`); err != nil {
		return err
	}
	if err := tx.Commit(ctx); err != nil {
		return fmt.Errorf("commit knowledge upload completion: %w", err)
	}
	return nil
}

func (s *Store) MarkUploadFailed(ctx context.Context, reservation UploadReservation, actorMemberID, code, detail string, objectMayExist bool) error {
	code, detail = sanitizeFailure(code, detail)
	objectState := "deleted"
	if objectMayExist {
		objectState = "delete_pending"
	}
	tx, err := s.pool.Begin(ctx)
	if err != nil {
		return fmt.Errorf("begin failed knowledge upload: %w", err)
	}
	defer func() { _ = tx.Rollback(ctx) }()
	var tenantID string
	if err := tx.QueryRow(ctx, `
SELECT tenant_id::text
FROM knowledge.document_source_objects
WHERE document_id = $1::uuid AND version_id = $2::uuid
FOR UPDATE`, reservation.DocumentID, reservation.VersionID).Scan(&tenantID); err != nil {
		return fmt.Errorf("lock failed knowledge upload: %w", err)
	}
	if _, err := tx.Exec(ctx, `
UPDATE knowledge.document_source_objects
SET state = $3,
    stored_at = CASE WHEN $4::boolean THEN now() ELSE NULL END,
    cleanup_attempts = 0,
    cleanup_max_attempts = $5,
    cleanup_available_at = CASE WHEN $4::boolean THEN now() ELSE NULL END,
    cleanup_lease_owner = NULL,
    cleanup_lease_token = NULL,
    cleanup_lease_expires_at = NULL,
    cleanup_failure_detail = NULL,
    updated_at = now()
WHERE tenant_id = $1::uuid AND version_id = $2::uuid`,
		tenantID, reservation.VersionID, objectState, objectMayExist, s.config.MaxAttempts); err != nil {
		return fmt.Errorf("mark knowledge source cleanup state: %w", err)
	}
	if _, err := tx.Exec(ctx, `
UPDATE knowledge.document_versions
SET ingestion_state = 'failed', ingestion_failure_code = $3,
    ingestion_failure_detail = $4, updated_at = now()
WHERE tenant_id = $1::uuid AND id = $2::uuid`, tenantID, reservation.VersionID, code, detail); err != nil {
		return fmt.Errorf("mark knowledge upload failed: %w", err)
	}
	if err := auditKnowledge(ctx, tx, tenantID, actorMemberID, reservation.DocumentID, reservation.VersionID, "",
		"upload_failed", `jsonb_build_object('code', $6::text)`, code); err != nil {
		return err
	}
	return tx.Commit(ctx)
}

func (s *Store) ListDocuments(ctx context.Context, tenantID string) ([]DocumentSummary, error) {
	rows, err := s.pool.Query(ctx, `
SELECT d.id::text, d.title, d.classification, d.status, d.created_at,
       COALESCE(current.id::text, ''), COALESCE(current.version_number, 0),
       COALESCE(current.ingestion_state, ''),
       COALESCE(latest.id::text, ''), COALESCE(latest.version_number, 0),
       COALESCE(latest.ingestion_state, ''),
       COALESCE(latest.ingestion_failure_code, ''),
       COALESCE(latest.ingestion_failure_detail, ''),
       (SELECT count(*)::integer FROM authz.document_grants AS document_grant
        WHERE document_grant.tenant_id = d.tenant_id
          AND document_grant.document_id = d.id
          AND document_grant.permission = 'read')
FROM knowledge.documents AS d
LEFT JOIN knowledge.document_versions AS current ON current.id = d.current_version_id
LEFT JOIN LATERAL (
    SELECT version.*
    FROM knowledge.document_versions AS version
    WHERE version.tenant_id = d.tenant_id AND version.document_id = d.id
    ORDER BY version.version_number DESC
    LIMIT 1
) AS latest ON true
WHERE d.tenant_id = $1::uuid
ORDER BY d.created_at DESC, d.id`, tenantID)
	if err != nil {
		return nil, fmt.Errorf("list knowledge documents: %w", err)
	}
	defer rows.Close()
	result := make([]DocumentSummary, 0)
	for rows.Next() {
		var item DocumentSummary
		if err := rows.Scan(&item.ID, &item.Title, &item.Classification, &item.Status, &item.CreatedAt,
			&item.CurrentVersionID, &item.CurrentVersion, &item.CurrentIngestion,
			&item.LatestVersionID, &item.LatestVersion, &item.LatestIngestion,
			&item.LatestFailureCode, &item.LatestFailureDetail, &item.GrantCount); err != nil {
			return nil, fmt.Errorf("scan knowledge document: %w", err)
		}
		result = append(result, item)
	}
	if err := rows.Err(); err != nil {
		return nil, fmt.Errorf("iterate knowledge documents: %w", err)
	}
	return result, nil
}

func (s *Store) ListMembers(ctx context.Context, tenantID, documentID string) ([]MemberSummary, error) {
	rows, err := s.pool.Query(ctx, `
SELECT member.id::text, member.display_name, member.status,
       document_grant.member_id IS NOT NULL
FROM identity.members AS member
LEFT JOIN authz.document_grants AS document_grant
  ON document_grant.tenant_id = member.tenant_id
 AND document_grant.member_id = member.id
 AND document_grant.document_id = NULLIF($2, '')::uuid
 AND document_grant.permission = 'read'
WHERE member.tenant_id = $1::uuid
ORDER BY member.display_name, member.id`, tenantID, documentID)
	if err != nil {
		return nil, fmt.Errorf("list knowledge grant members: %w", err)
	}
	defer rows.Close()
	result := make([]MemberSummary, 0)
	for rows.Next() {
		var item MemberSummary
		if err := rows.Scan(&item.ID, &item.DisplayName, &item.Status, &item.Granted); err != nil {
			return nil, fmt.Errorf("scan knowledge grant member: %w", err)
		}
		result = append(result, item)
	}
	if err := rows.Err(); err != nil {
		return nil, fmt.Errorf("iterate knowledge grant members: %w", err)
	}
	return result, nil
}

func (s *Store) ListVersions(ctx context.Context, tenantID, documentID string) ([]VersionSummary, error) {
	rows, err := s.pool.Query(ctx, `
SELECT version.id::text, version.version_number, version.checksum, version.status,
       version.ingestion_state, COALESCE(version.ingestion_failure_code, ''),
       COALESCE(version.ingestion_failure_detail, ''), version.created_at, version.published_at,
       COALESCE(source.source_format, ''), COALESCE(source.original_filename, ''),
       COALESCE(source.size_bytes, 0), COALESCE(job.state, ''), COALESCE(job.attempts, 0)
FROM knowledge.document_versions AS version
JOIN knowledge.documents AS document
  ON document.tenant_id = version.tenant_id AND document.id = version.document_id
LEFT JOIN knowledge.document_source_objects AS source ON source.version_id = version.id
LEFT JOIN knowledge.ingestion_jobs AS job ON job.version_id = version.id
WHERE version.tenant_id = $1::uuid AND version.document_id = $2::uuid
ORDER BY version.version_number DESC`, tenantID, documentID)
	if err != nil {
		return nil, fmt.Errorf("list knowledge versions: %w", err)
	}
	defer rows.Close()
	result := make([]VersionSummary, 0)
	for rows.Next() {
		var item VersionSummary
		if err := rows.Scan(&item.ID, &item.VersionNumber, &item.Checksum, &item.Status,
			&item.IngestionState, &item.FailureCode, &item.FailureDetail, &item.CreatedAt,
			&item.PublishedAt, &item.SourceFormat, &item.OriginalFilename, &item.SizeBytes,
			&item.JobState, &item.Attempts); err != nil {
			return nil, fmt.Errorf("scan knowledge version: %w", err)
		}
		result = append(result, item)
	}
	if err := rows.Err(); err != nil {
		return nil, fmt.Errorf("iterate knowledge versions: %w", err)
	}
	return result, nil
}

func (s *Store) SetGrant(ctx context.Context, tenantID, actorMemberID, documentID, memberID string, enabled bool) error {
	tx, err := s.pool.BeginTx(ctx, pgx.TxOptions{IsoLevel: pgx.Serializable})
	if err != nil {
		return fmt.Errorf("begin knowledge grant update: %w", err)
	}
	defer func() { _ = tx.Rollback(ctx) }()
	var documentValid, memberValid bool
	if err := tx.QueryRow(ctx, `
SELECT EXISTS (
    SELECT 1 FROM knowledge.documents
    WHERE tenant_id = $1::uuid AND id = $2::uuid AND status = 'active'
), EXISTS (
    SELECT 1 FROM identity.members
    WHERE tenant_id = $1::uuid AND id = $3::uuid AND status = 'active'
)`, tenantID, documentID, memberID).Scan(&documentValid, &memberValid); err != nil {
		return fmt.Errorf("authorize knowledge grant subject: %w", err)
	}
	if !documentValid || !memberValid {
		return ErrNotFound
	}
	eventType := "grant_revoked"
	if enabled {
		eventType = "grant_added"
		if _, err := tx.Exec(ctx, `
INSERT INTO authz.document_grants (tenant_id, document_id, member_id, permission)
VALUES ($1::uuid, $2::uuid, $3::uuid, 'read')
ON CONFLICT (tenant_id, document_id, member_id, permission) DO NOTHING`,
			tenantID, documentID, memberID); err != nil {
			return fmt.Errorf("grant knowledge document read access: %w", err)
		}
	} else {
		if _, err := tx.Exec(ctx, `
DELETE FROM authz.document_grants
WHERE tenant_id = $1::uuid AND document_id = $2::uuid
  AND member_id = $3::uuid AND permission = 'read'`, tenantID, documentID, memberID); err != nil {
			return fmt.Errorf("revoke knowledge document read access: %w", err)
		}
	}
	if err := auditKnowledge(ctx, tx, tenantID, actorMemberID, documentID, "", "", eventType,
		`jsonb_build_object('member_id', $6::text)`, memberID); err != nil {
		return err
	}
	if err := tx.Commit(ctx); err != nil {
		return fmt.Errorf("commit knowledge grant update: %w", err)
	}
	return nil
}

func (s *Store) Publish(ctx context.Context, tenantID, actorMemberID, documentID, versionID string) error {
	tx, err := s.pool.BeginTx(ctx, pgx.TxOptions{IsoLevel: pgx.Serializable})
	if err != nil {
		return fmt.Errorf("begin knowledge publication: %w", err)
	}
	defer func() { _ = tx.Rollback(ctx) }()
	if _, err := tx.Exec(ctx, `SELECT pg_advisory_xact_lock(hashtext($1))`, tenantID); err != nil {
		return fmt.Errorf("lock knowledge publication generation: %w", err)
	}
	var ingestionState, status, classification string
	if err := tx.QueryRow(ctx, `
SELECT version.ingestion_state, version.status, document.classification
FROM knowledge.document_versions AS version
JOIN knowledge.documents AS document
  ON document.tenant_id = version.tenant_id AND document.id = version.document_id
WHERE version.tenant_id = $1::uuid AND version.document_id = $2::uuid
  AND version.id = $3::uuid AND document.status = 'active'
FOR UPDATE OF version, document`, tenantID, documentID, versionID).Scan(&ingestionState, &status, &classification); err != nil {
		if errors.Is(err, pgx.ErrNoRows) {
			return ErrNotFound
		}
		return fmt.Errorf("lock knowledge publication: %w", err)
	}
	if ingestionState != "indexed" {
		return ErrConflict
	}
	if classification != "public" && classification != "internal" {
		return ErrConflict
	}
	var generationID, modelRevision string
	if err := tx.QueryRow(ctx, `
SELECT id::text, model_revision
FROM knowledge.index_generations
WHERE tenant_id = $1::uuid AND state = 'active'
FOR UPDATE`, tenantID).Scan(&generationID, &modelRevision); err != nil {
		return fmt.Errorf("load active knowledge index generation: %w", err)
	}
	var chunks, projections int
	if err := tx.QueryRow(ctx, `
SELECT count(*)::integer,
       count(index.chunk_id) FILTER (
           WHERE index.model_revision = $4
             AND index.dimension = $5
             AND index.content_checksum = chunk.checksum
       )::integer
FROM knowledge.chunks AS chunk
LEFT JOIN knowledge.chunk_search_indexes AS index
  ON index.tenant_id = chunk.tenant_id AND index.chunk_id = chunk.id
 AND index.generation_id = $3::uuid
WHERE chunk.tenant_id = $1::uuid AND chunk.version_id = $2::uuid`,
		tenantID, versionID, generationID, modelRevision, s.config.EmbeddingDimension).Scan(&chunks, &projections); err != nil {
		return fmt.Errorf("verify knowledge publication projection: %w", err)
	}
	if chunks < 1 || projections != chunks || modelRevision != s.config.EmbeddingRevision {
		return ErrConflict
	}
	if _, err := tx.Exec(ctx, `
UPDATE knowledge.document_versions
SET status = 'superseded', updated_at = now()
WHERE tenant_id = $1::uuid AND document_id = $2::uuid
  AND status = 'published' AND id <> $3::uuid`,
		tenantID, documentID, versionID); err != nil {
		return fmt.Errorf("supersede prior knowledge version: %w", err)
	}
	if _, err := tx.Exec(ctx, `
UPDATE knowledge.document_versions
SET status = 'published', published_at = COALESCE(published_at, now()), updated_at = now()
WHERE tenant_id = $1::uuid AND document_id = $2::uuid AND id = $3::uuid`,
		tenantID, documentID, versionID); err != nil {
		return fmt.Errorf("publish knowledge version: %w", err)
	}
	if _, err := tx.Exec(ctx, `
UPDATE knowledge.documents
SET current_version_id = $3::uuid
WHERE tenant_id = $1::uuid AND id = $2::uuid`, tenantID, documentID, versionID); err != nil {
		return fmt.Errorf("set current knowledge version: %w", err)
	}
	if err := refreshGenerationCounts(ctx, tx, tenantID, generationID); err != nil {
		return err
	}
	if err := auditKnowledge(ctx, tx, tenantID, actorMemberID, documentID, versionID, "", "version_published",
		`jsonb_build_object('generation_id', $6::text)`, generationID); err != nil {
		return err
	}
	if err := tx.Commit(ctx); err != nil {
		return fmt.Errorf("commit knowledge publication: %w", err)
	}
	return nil
}

func (s *Store) Unpublish(ctx context.Context, tenantID, actorMemberID, documentID string) error {
	tx, err := s.pool.BeginTx(ctx, pgx.TxOptions{IsoLevel: pgx.Serializable})
	if err != nil {
		return fmt.Errorf("begin knowledge unpublish: %w", err)
	}
	defer func() { _ = tx.Rollback(ctx) }()
	if _, err := tx.Exec(ctx, `SELECT pg_advisory_xact_lock(hashtext($1))`, tenantID); err != nil {
		return fmt.Errorf("lock knowledge unpublish generation: %w", err)
	}
	var versionID string
	if err := tx.QueryRow(ctx, `
SELECT current_version_id::text
FROM knowledge.documents
WHERE tenant_id = $1::uuid AND id = $2::uuid AND status = 'active'
  AND current_version_id IS NOT NULL
FOR UPDATE`, tenantID, documentID).Scan(&versionID); err != nil {
		if errors.Is(err, pgx.ErrNoRows) {
			return ErrConflict
		}
		return fmt.Errorf("lock knowledge document for unpublish: %w", err)
	}
	if _, err := tx.Exec(ctx, `
UPDATE knowledge.documents SET current_version_id = NULL
WHERE tenant_id = $1::uuid AND id = $2::uuid`, tenantID, documentID); err != nil {
		return fmt.Errorf("clear current knowledge version: %w", err)
	}
	if _, err := tx.Exec(ctx, `
UPDATE knowledge.document_versions
SET status = 'draft', updated_at = now()
WHERE tenant_id = $1::uuid AND id = $2::uuid`, tenantID, versionID); err != nil {
		return fmt.Errorf("unpublish knowledge document: %w", err)
	}
	var generationID string
	err = tx.QueryRow(ctx, `
SELECT id::text FROM knowledge.index_generations
WHERE tenant_id = $1::uuid AND state = 'active'
FOR UPDATE`, tenantID).Scan(&generationID)
	if err != nil && !errors.Is(err, pgx.ErrNoRows) {
		return fmt.Errorf("lock active knowledge generation: %w", err)
	}
	if generationID != "" {
		if err := refreshGenerationCounts(ctx, tx, tenantID, generationID); err != nil {
			return err
		}
	}
	if err := auditKnowledge(ctx, tx, tenantID, actorMemberID, documentID, versionID, "", "document_unpublished",
		`'{}'::jsonb`); err != nil {
		return err
	}
	return tx.Commit(ctx)
}

func (s *Store) ClaimObjectCleanup(ctx context.Context, owner string, lease time.Duration) (ObjectCleanup, bool, error) {
	owner = strings.TrimSpace(owner)
	if owner == "" || len(owner) > 128 || lease < time.Minute || lease > 30*time.Minute {
		return ObjectCleanup{}, false, ErrInvalidInput
	}
	leaseToken, err := randomUUID()
	if err != nil {
		return ObjectCleanup{}, false, err
	}
	leaseSeconds := int64((lease + time.Second - 1) / time.Second)
	var cleanup ObjectCleanup
	err = s.pool.QueryRow(ctx, `
WITH candidate AS (
    SELECT version_id
    FROM knowledge.document_source_objects
    WHERE state = 'delete_pending'
      AND cleanup_attempts < cleanup_max_attempts
      AND cleanup_available_at <= now()
      AND (cleanup_lease_token IS NULL OR cleanup_lease_expires_at < now())
    ORDER BY cleanup_available_at, updated_at, version_id
    FOR UPDATE SKIP LOCKED
    LIMIT 1
)
UPDATE knowledge.document_source_objects AS source
SET cleanup_attempts = cleanup_attempts + 1,
    cleanup_lease_owner = $1,
    cleanup_lease_token = $2::uuid,
    cleanup_lease_expires_at = now() + make_interval(secs => $3),
    updated_at = now()
FROM candidate
WHERE source.version_id = candidate.version_id
RETURNING source.tenant_id::text, source.document_id::text, source.version_id::text,
          source.bucket, source.object_key, source.cleanup_lease_token::text,
          source.cleanup_attempts, source.cleanup_max_attempts`,
		owner, leaseToken, leaseSeconds,
	).Scan(
		&cleanup.TenantID, &cleanup.DocumentID, &cleanup.VersionID,
		&cleanup.Bucket, &cleanup.ObjectKey, &cleanup.LeaseToken,
		&cleanup.Attempts, &cleanup.MaxAttempts,
	)
	if errors.Is(err, pgx.ErrNoRows) {
		return ObjectCleanup{}, false, nil
	}
	if err != nil {
		return ObjectCleanup{}, false, fmt.Errorf("claim knowledge object cleanup: %w", err)
	}
	return cleanup, true, nil
}

func (s *Store) CompleteObjectCleanup(ctx context.Context, cleanup ObjectCleanup) error {
	if cleanup.TenantID == "" || cleanup.DocumentID == "" || cleanup.VersionID == "" ||
		cleanup.LeaseToken == "" {
		return ErrInvalidInput
	}
	tx, err := s.pool.Begin(ctx)
	if err != nil {
		return fmt.Errorf("begin knowledge object cleanup completion: %w", err)
	}
	defer func() { _ = tx.Rollback(ctx) }()
	var found bool
	if err := tx.QueryRow(ctx, `
SELECT true
FROM knowledge.document_source_objects
WHERE tenant_id = $1::uuid AND document_id = $2::uuid AND version_id = $3::uuid
  AND state = 'delete_pending' AND cleanup_lease_token = $4::uuid
  AND cleanup_lease_expires_at >= now()
FOR UPDATE`,
		cleanup.TenantID, cleanup.DocumentID, cleanup.VersionID, cleanup.LeaseToken,
	).Scan(&found); err != nil {
		if errors.Is(err, pgx.ErrNoRows) {
			return ErrLeaseLost
		}
		return fmt.Errorf("lock knowledge object cleanup completion: %w", err)
	}
	if _, err := tx.Exec(ctx, `
UPDATE knowledge.document_source_objects
SET state = 'deleted',
    stored_at = NULL,
    cleanup_available_at = NULL,
    cleanup_lease_owner = NULL,
    cleanup_lease_token = NULL,
    cleanup_lease_expires_at = NULL,
    cleanup_failure_detail = NULL,
    updated_at = now()
WHERE tenant_id = $1::uuid AND version_id = $2::uuid AND cleanup_lease_token = $3::uuid`,
		cleanup.TenantID, cleanup.VersionID, cleanup.LeaseToken,
	); err != nil {
		return fmt.Errorf("complete knowledge object cleanup: %w", err)
	}
	if err := auditKnowledge(ctx, tx, cleanup.TenantID, "", cleanup.DocumentID, cleanup.VersionID, "",
		"source_cleanup_succeeded", `jsonb_build_object('attempts', $6::integer)`, cleanup.Attempts); err != nil {
		return err
	}
	return tx.Commit(ctx)
}

func (s *Store) FailObjectCleanup(ctx context.Context, cleanup ObjectCleanup, cause error) error {
	if cleanup.TenantID == "" || cleanup.DocumentID == "" || cleanup.VersionID == "" ||
		cleanup.LeaseToken == "" || cause == nil {
		return ErrInvalidInput
	}
	_, detail := sanitizeFailure("OBJECT_DELETE_FAILED", "knowledge source object deletion failed")
	tx, err := s.pool.Begin(ctx)
	if err != nil {
		return fmt.Errorf("begin failed knowledge object cleanup: %w", err)
	}
	defer func() { _ = tx.Rollback(ctx) }()
	var attempts, maxAttempts int
	if err := tx.QueryRow(ctx, `
SELECT cleanup_attempts, cleanup_max_attempts
FROM knowledge.document_source_objects
WHERE tenant_id = $1::uuid AND document_id = $2::uuid AND version_id = $3::uuid
  AND state = 'delete_pending' AND cleanup_lease_token = $4::uuid
  AND cleanup_lease_expires_at >= now()
FOR UPDATE`,
		cleanup.TenantID, cleanup.DocumentID, cleanup.VersionID, cleanup.LeaseToken,
	).Scan(&attempts, &maxAttempts); err != nil {
		if errors.Is(err, pgx.ErrNoRows) {
			return ErrLeaseLost
		}
		return fmt.Errorf("lock failed knowledge object cleanup: %w", err)
	}
	nextState := "delete_failed"
	delaySeconds := 0
	if attempts < maxAttempts {
		nextState = "delete_pending"
		delaySeconds = min(300, 1<<(min(attempts, 8)))
	}
	if _, err := tx.Exec(ctx, `
UPDATE knowledge.document_source_objects
SET state = $5,
    cleanup_available_at = CASE
        WHEN $5 = 'delete_pending' THEN now() + make_interval(secs => $6)
        ELSE NULL
    END,
    cleanup_lease_owner = NULL,
    cleanup_lease_token = NULL,
    cleanup_lease_expires_at = NULL,
    cleanup_failure_detail = $7,
    updated_at = now()
WHERE tenant_id = $1::uuid AND document_id = $2::uuid AND version_id = $3::uuid
  AND cleanup_lease_token = $4::uuid`,
		cleanup.TenantID, cleanup.DocumentID, cleanup.VersionID, cleanup.LeaseToken,
		nextState, delaySeconds, detail,
	); err != nil {
		return fmt.Errorf("record failed knowledge object cleanup: %w", err)
	}
	if err := auditKnowledge(ctx, tx, cleanup.TenantID, "", cleanup.DocumentID, cleanup.VersionID, "",
		"source_cleanup_"+strings.TrimPrefix(nextState, "delete_"),
		`jsonb_build_object('code', $6::text, 'attempts', $7::integer)`,
		"OBJECT_DELETE_FAILED", attempts); err != nil {
		return err
	}
	return tx.Commit(ctx)
}

func (s *Store) ClaimJob(ctx context.Context, owner string, lease time.Duration) (Job, bool, error) {
	owner = strings.TrimSpace(owner)
	if owner == "" || len(owner) > 128 || lease < 5*time.Second || lease > 30*time.Minute {
		return Job{}, false, ErrInvalidInput
	}
	leaseToken, err := randomUUID()
	if err != nil {
		return Job{}, false, err
	}
	leaseSeconds := int64((lease + time.Second - 1) / time.Second)
	tx, err := s.pool.Begin(ctx)
	if err != nil {
		return Job{}, false, fmt.Errorf("begin knowledge job claim: %w", err)
	}
	defer func() { _ = tx.Rollback(ctx) }()
	var job Job
	err = tx.QueryRow(ctx, `
WITH candidate AS (
    SELECT id
    FROM knowledge.ingestion_jobs
    WHERE attempts < max_attempts
      AND (
        (state IN ('queued', 'retryable') AND available_at <= now())
        OR (state = 'leased' AND lease_expires_at < now())
      )
    ORDER BY available_at, created_at, id
    FOR UPDATE SKIP LOCKED
    LIMIT 1
)
UPDATE knowledge.ingestion_jobs AS job
SET state = 'leased',
    attempts = attempts + 1,
    lease_owner = $1,
    lease_token = $2::uuid,
    lease_expires_at = now() + make_interval(secs => $3),
    failure_code = NULL,
    failure_detail = NULL,
    updated_at = now()
FROM candidate, knowledge.document_source_objects AS source
WHERE job.id = candidate.id AND source.version_id = job.version_id
RETURNING job.id::text, job.tenant_id::text, job.document_id::text, job.version_id::text,
          source.bucket, source.object_key, source.source_format, source.size_bytes,
          source.checksum, job.lease_token::text, job.attempts, job.max_attempts,
          job.parser_revision, job.embedding_revision, job.embedding_dimension`,
		owner, leaseToken, leaseSeconds).Scan(
		&job.ID, &job.TenantID, &job.DocumentID, &job.VersionID, &job.Bucket,
		&job.ObjectKey, &job.SourceFormat, &job.SizeBytes, &job.Checksum, &job.LeaseToken,
		&job.Attempts, &job.MaxAttempts, &job.ParserRevision, &job.EmbeddingRevision,
		&job.EmbeddingDimension,
	)
	if errors.Is(err, pgx.ErrNoRows) {
		if err := tx.Commit(ctx); err != nil {
			return Job{}, false, fmt.Errorf("commit empty knowledge job claim: %w", err)
		}
		return Job{}, false, nil
	}
	if err != nil {
		return Job{}, false, fmt.Errorf("claim knowledge ingestion job: %w", err)
	}
	if _, err := tx.Exec(ctx, `
UPDATE knowledge.document_versions
SET ingestion_state = 'processing', updated_at = now()
WHERE tenant_id = $1::uuid AND id = $2::uuid
  AND ingestion_state IN ('queued', 'processing')`, job.TenantID, job.VersionID); err != nil {
		return Job{}, false, fmt.Errorf("mark knowledge version processing: %w", err)
	}
	if err := tx.Commit(ctx); err != nil {
		return Job{}, false, fmt.Errorf("commit knowledge job claim: %w", err)
	}
	return job, true, nil
}

func (s *Store) RenewJobLease(ctx context.Context, job Job, lease time.Duration) error {
	if job.ID == "" || job.TenantID == "" || job.LeaseToken == "" ||
		lease < time.Minute || lease > 30*time.Minute {
		return ErrInvalidInput
	}
	leaseSeconds := int64((lease + time.Second - 1) / time.Second)
	result, err := s.pool.Exec(ctx, `
UPDATE knowledge.ingestion_jobs
SET lease_expires_at = now() + make_interval(secs => $4), updated_at = now()
WHERE id = $1::uuid AND tenant_id = $2::uuid AND lease_token = $3::uuid
  AND state = 'leased' AND lease_expires_at >= now()`,
		job.ID, job.TenantID, job.LeaseToken, leaseSeconds)
	if err != nil {
		return fmt.Errorf("renew knowledge ingestion lease: %w", err)
	}
	if result.RowsAffected() != 1 {
		return ErrLeaseLost
	}
	return nil
}

func (s *Store) CompleteJob(ctx context.Context, job Job, chunks []IndexedChunk) error {
	if job.ParserRevision != s.config.ParserRevision ||
		job.EmbeddingRevision != s.config.EmbeddingRevision ||
		job.EmbeddingDimension != s.config.EmbeddingDimension ||
		len(chunks) < 1 || len(chunks) > maxChunksPerVersion {
		return ErrInvalidInput
	}
	for index := range chunks {
		if chunks[index].Ordinal != index || !validSHA256(chunks[index].Checksum) ||
			len(chunks[index].Embedding) != s.config.EmbeddingDimension ||
			!validNormalizedVector(chunks[index].Embedding) {
			return ErrInvalidInput
		}
	}
	tx, err := s.pool.BeginTx(ctx, pgx.TxOptions{IsoLevel: pgx.Serializable})
	if err != nil {
		return fmt.Errorf("begin knowledge index completion: %w", err)
	}
	defer func() { _ = tx.Rollback(ctx) }()
	if _, err := tx.Exec(ctx, `SELECT pg_advisory_xact_lock(hashtext($1))`, job.TenantID); err != nil {
		return fmt.Errorf("lock knowledge index generation: %w", err)
	}
	var state string
	if err := tx.QueryRow(ctx, `
SELECT state
FROM knowledge.ingestion_jobs
WHERE id = $1::uuid AND tenant_id = $2::uuid AND lease_token = $3::uuid
  AND lease_owner IS NOT NULL AND lease_expires_at >= now()
FOR UPDATE`, job.ID, job.TenantID, job.LeaseToken).Scan(&state); err != nil {
		if errors.Is(err, pgx.ErrNoRows) {
			return ErrLeaseLost
		}
		return fmt.Errorf("lock knowledge ingestion completion: %w", err)
	}
	if state != "leased" {
		return ErrLeaseLost
	}
	generationID, err := s.ensureActiveGeneration(ctx, tx, job.TenantID)
	if err != nil {
		return err
	}
	if _, err := tx.Exec(ctx, `
DELETE FROM knowledge.chunks
WHERE tenant_id = $1::uuid AND version_id = $2::uuid`, job.TenantID, job.VersionID); err != nil {
		return fmt.Errorf("replace knowledge chunks: %w", err)
	}
	for _, chunk := range chunks {
		if _, err := tx.Exec(ctx, `
INSERT INTO knowledge.chunks
    (id, tenant_id, document_id, version_id, ordinal, content, checksum)
VALUES ($1::uuid, $2::uuid, $3::uuid, $4::uuid, $5, $6, $7)`,
			chunk.ID, job.TenantID, job.DocumentID, job.VersionID,
			chunk.Ordinal, chunk.Content, chunk.Checksum); err != nil {
			return fmt.Errorf("insert knowledge chunk %d: %w", chunk.Ordinal, err)
		}
		if _, err := tx.Exec(ctx, `
INSERT INTO knowledge.chunk_search_indexes
    (generation_id, tenant_id, chunk_id, model_revision, dimension,
     content_checksum, embedding, search_vector, normalized)
VALUES ($1::uuid, $2::uuid, $3::uuid, $4, $5, $6, $7::halfvec,
        to_tsvector('simple', $8), true)`,
			generationID, job.TenantID, chunk.ID, job.EmbeddingRevision,
			job.EmbeddingDimension, chunk.Checksum, halfVectorLiteral(chunk.Embedding),
			chunk.Lexemes); err != nil {
			return fmt.Errorf("index knowledge chunk %d: %w", chunk.Ordinal, err)
		}
	}
	if _, err := tx.Exec(ctx, `
UPDATE knowledge.ingestion_jobs
SET state = 'succeeded', chunk_count = $4, vector_count = $4,
    lease_owner = NULL, lease_token = NULL, lease_expires_at = NULL,
    failure_code = NULL, failure_detail = NULL, updated_at = now()
WHERE id = $1::uuid AND tenant_id = $2::uuid AND lease_token = $3::uuid`,
		job.ID, job.TenantID, job.LeaseToken, len(chunks)); err != nil {
		return fmt.Errorf("complete knowledge ingestion job: %w", err)
	}
	if _, err := tx.Exec(ctx, `
UPDATE knowledge.document_versions
SET ingestion_state = 'indexed', ingestion_failure_code = NULL,
    ingestion_failure_detail = NULL, updated_at = now()
WHERE tenant_id = $1::uuid AND id = $2::uuid`,
		job.TenantID, job.VersionID); err != nil {
		return fmt.Errorf("complete knowledge version ingestion state: %w", err)
	}
	if err := auditKnowledge(ctx, tx, job.TenantID, "", job.DocumentID, job.VersionID, job.ID,
		"ingestion_succeeded", `jsonb_build_object('chunk_count', $6::integer, 'generation_id', $7::text)`,
		len(chunks), generationID); err != nil {
		return err
	}
	if err := tx.Commit(ctx); err != nil {
		return fmt.Errorf("commit knowledge index completion: %w", err)
	}
	return nil
}

func (s *Store) FailJob(ctx context.Context, job Job, cause error) error {
	code, detail, retryable := classifyProcessingFailure(cause)
	tx, err := s.pool.Begin(ctx)
	if err != nil {
		return fmt.Errorf("begin knowledge job failure: %w", err)
	}
	defer func() { _ = tx.Rollback(ctx) }()
	var attempts, maxAttempts int
	if err := tx.QueryRow(ctx, `
SELECT attempts, max_attempts
FROM knowledge.ingestion_jobs
WHERE id = $1::uuid AND tenant_id = $2::uuid AND lease_token = $3::uuid
  AND state = 'leased' AND lease_expires_at >= now()
FOR UPDATE`, job.ID, job.TenantID, job.LeaseToken).Scan(&attempts, &maxAttempts); err != nil {
		if errors.Is(err, pgx.ErrNoRows) {
			return ErrLeaseLost
		}
		return fmt.Errorf("lock failed knowledge job: %w", err)
	}
	nextState := "failed"
	delaySeconds := 0
	if retryable && attempts < maxAttempts {
		nextState = "retryable"
		delaySeconds = min(300, 1<<(min(attempts, 8)))
	}
	if _, err := tx.Exec(ctx, `
UPDATE knowledge.ingestion_jobs
SET state = $4,
    available_at = now() + make_interval(secs => $5),
    lease_owner = NULL, lease_token = NULL, lease_expires_at = NULL,
    failure_code = $6, failure_detail = $7, updated_at = now()
WHERE id = $1::uuid AND tenant_id = $2::uuid AND lease_token = $3::uuid`,
		job.ID, job.TenantID, job.LeaseToken, nextState, delaySeconds, code, detail); err != nil {
		return fmt.Errorf("record failed knowledge job: %w", err)
	}
	if nextState == "failed" {
		if _, err := tx.Exec(ctx, `
UPDATE knowledge.document_versions
SET ingestion_state = 'failed', ingestion_failure_code = $3,
    ingestion_failure_detail = $4, updated_at = now()
WHERE tenant_id = $1::uuid AND id = $2::uuid`,
			job.TenantID, job.VersionID, code, detail); err != nil {
			return fmt.Errorf("mark knowledge version failed: %w", err)
		}
	}
	if err := auditKnowledge(ctx, tx, job.TenantID, "", job.DocumentID, job.VersionID, job.ID,
		"ingestion_"+nextState, `jsonb_build_object('code', $6::text, 'attempts', $7::integer)`,
		code, attempts); err != nil {
		return err
	}
	return tx.Commit(ctx)
}

func (s *Store) ensureActiveGeneration(ctx context.Context, tx pgx.Tx, tenantID string) (string, error) {
	var generationID, model string
	err := tx.QueryRow(ctx, `
SELECT id::text, model_revision
FROM knowledge.index_generations
WHERE tenant_id = $1::uuid AND state = 'active'
FOR UPDATE`, tenantID).Scan(&generationID, &model)
	if err == nil {
		if model != s.config.EmbeddingRevision {
			return "", errors.New("active knowledge index uses a different embedding revision")
		}
		return generationID, nil
	}
	if !errors.Is(err, pgx.ErrNoRows) {
		return "", fmt.Errorf("load active knowledge index generation: %w", err)
	}
	generationID, err = randomUUID()
	if err != nil {
		return "", err
	}
	if _, err := tx.Exec(ctx, `
INSERT INTO knowledge.index_generations
    (id, tenant_id, model_revision, dimension, storage_type, distance_metric,
     state, expected_chunk_count, indexed_chunk_count, activated_at)
VALUES ($1::uuid, $2::uuid, $3, $4, 'halfvec', 'cosine',
        'active', 0, 0, now())`,
		generationID, tenantID, s.config.EmbeddingRevision, s.config.EmbeddingDimension); err != nil {
		return "", fmt.Errorf("create active knowledge index generation: %w", err)
	}
	return generationID, nil
}

func refreshGenerationCounts(ctx context.Context, tx pgx.Tx, tenantID, generationID string) error {
	var expected, indexed int
	if err := tx.QueryRow(ctx, `
SELECT count(*)::integer,
       count(search.chunk_id) FILTER (WHERE search.content_checksum = chunk.checksum)::integer
FROM knowledge.documents AS document
JOIN knowledge.document_versions AS version
  ON version.tenant_id = document.tenant_id AND version.id = document.current_version_id
JOIN knowledge.chunks AS chunk
  ON chunk.tenant_id = version.tenant_id AND chunk.version_id = version.id
LEFT JOIN knowledge.chunk_search_indexes AS search
  ON search.tenant_id = chunk.tenant_id AND search.chunk_id = chunk.id
 AND search.generation_id = $2::uuid
WHERE document.tenant_id = $1::uuid AND document.status = 'active'
  AND version.status = 'published'`, tenantID, generationID).Scan(&expected, &indexed); err != nil {
		return fmt.Errorf("count active knowledge index projections: %w", err)
	}
	if expected != indexed {
		return errors.New("active knowledge index is incomplete for current published documents")
	}
	if _, err := tx.Exec(ctx, `
UPDATE knowledge.index_generations
SET expected_chunk_count = $3, indexed_chunk_count = $3, updated_at = now()
WHERE tenant_id = $1::uuid AND id = $2::uuid AND state = 'active'`,
		tenantID, generationID, expected); err != nil {
		return fmt.Errorf("refresh active knowledge index counts: %w", err)
	}
	return nil
}

func (s *Store) readIdempotentUpload(ctx context.Context, tx pgx.Tx, tenantID, keyDigest string) (UploadReservation, bool, error) {
	var result UploadReservation
	err := tx.QueryRow(ctx, `
SELECT idem.request_digest, idem.document_id::text, idem.version_id::text,
       version.version_number, version.ingestion_state, source.bucket, source.object_key
FROM knowledge.upload_idempotency AS idem
JOIN knowledge.document_versions AS version ON version.id = idem.version_id
JOIN knowledge.document_source_objects AS source ON source.version_id = idem.version_id
WHERE idem.tenant_id = $1::uuid AND idem.key_digest = $2`,
		tenantID, keyDigest).Scan(&result.RequestDigest, &result.DocumentID, &result.VersionID,
		&result.VersionNumber, &result.IngestionState, &result.Bucket, &result.ObjectKey)
	if errors.Is(err, pgx.ErrNoRows) {
		return UploadReservation{}, false, nil
	}
	if err != nil {
		return UploadReservation{}, false, fmt.Errorf("read idempotent knowledge upload: %w", err)
	}
	return result, true, nil
}

func auditKnowledge(ctx context.Context, tx pgx.Tx, tenantID, actorID, documentID, versionID, jobID, eventType, evidenceExpression string, evidenceArgs ...any) error {
	arguments := []any{tenantID, nullUUID(actorID), nullUUID(documentID), nullUUID(versionID), nullUUID(jobID)}
	arguments = append(arguments, evidenceArgs...)
	statement := `
INSERT INTO audit.knowledge_events
    (tenant_id, actor_member_id, document_id, version_id, job_id, event_type, evidence)
VALUES ($1::uuid, $2::uuid, $3::uuid, $4::uuid, $5::uuid, '` +
		strings.ReplaceAll(eventType, "'", "") + `', ` + evidenceExpression + `)`
	if _, err := tx.Exec(ctx, statement, arguments...); err != nil {
		return fmt.Errorf("audit knowledge event %s: %w", eventType, err)
	}
	return nil
}

func nullUUID(value string) any {
	if strings.TrimSpace(value) == "" {
		return nil
	}
	return value
}

func digest(value string) string {
	sum := sha256.Sum256([]byte(value))
	return "sha256:" + hex.EncodeToString(sum[:])
}

func randomUUID() (string, error) {
	var value [16]byte
	if _, err := rand.Read(value[:]); err != nil {
		return "", fmt.Errorf("generate knowledge UUID: %w", err)
	}
	value[6] = (value[6] & 0x0f) | 0x40
	value[8] = (value[8] & 0x3f) | 0x80
	encoded := hex.EncodeToString(value[:])
	return encoded[:8] + "-" + encoded[8:12] + "-" + encoded[12:16] + "-" + encoded[16:20] + "-" + encoded[20:], nil
}

func halfVectorLiteral(vector []float32) string {
	var builder strings.Builder
	builder.Grow(len(vector) * 8)
	builder.WriteByte('[')
	for index, value := range vector {
		if index > 0 {
			builder.WriteByte(',')
		}
		builder.WriteString(strconv.FormatFloat(float64(value), 'g', -1, 32))
	}
	builder.WriteByte(']')
	return builder.String()
}

func validNormalizedVector(vector []float32) bool {
	var norm float64
	for _, value := range vector {
		if math.IsNaN(float64(value)) || math.IsInf(float64(value), 0) {
			return false
		}
		norm += float64(value * value)
	}
	return norm > 0.99 && norm < 1.01
}

func classifyProcessingFailure(cause error) (string, string, bool) {
	var processing *ProcessingError
	if errors.As(cause, &processing) {
		code, detail := sanitizeFailure(processing.Code, processing.Detail)
		return code, detail, processing.Retryable
	}
	return "DEPENDENCY_FAILED", "a required ingestion dependency failed", true
}

func sanitizeFailure(code, detail string) (string, string) {
	code = strings.ToUpper(strings.TrimSpace(code))
	if len(code) < 1 || len(code) > 64 {
		code = "PROCESSING_FAILED"
	}
	for _, char := range code {
		if (char < 'A' || char > 'Z') && (char < '0' || char > '9') && char != '_' {
			code = "PROCESSING_FAILED"
			break
		}
	}
	detail = strings.TrimSpace(detail)
	if detail == "" {
		detail = "processing failed"
	}
	if len(detail) > 512 {
		detail = detail[:512]
	}
	return code, detail
}
