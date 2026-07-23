package retrieval

import (
	"context"
	"crypto/rand"
	"encoding/hex"
	"errors"
	"fmt"
	"strings"
	"sync"

	"github.com/jackc/pgx/v5"
)

const (
	maxReindexChunks           = 100_000
	maxReindexEmbeddingWorkers = 8
)

type ReindexStats struct {
	GenerationID string `json:"generation_id"`
	State        string `json:"state"`
	Expected     int    `json:"expected_chunks"`
	Indexed      int    `json:"indexed_chunks"`
	Activated    bool   `json:"activated"`
}

type reindexItem struct {
	ChunkID  string
	Content  string
	Checksum string
}

// BuildIndexGeneration constructs an isolated generation and activates it only
// after every current published chunk has a checksum-matching projection.
func (s *Store) BuildIndexGeneration(
	ctx context.Context,
	tenantID string,
	batchSize int,
	embeddingWorkers int,
) (ReindexStats, error) {
	tenantID = strings.TrimSpace(tenantID)
	if tenantID == "" || batchSize < 1 || batchSize > 128 ||
		embeddingWorkers < 1 || embeddingWorkers > maxReindexEmbeddingWorkers {
		return ReindexStats{}, errors.New("knowledge reindex input is invalid")
	}
	stats, err := s.beginIndexGeneration(ctx, tenantID)
	if err != nil || stats.Activated {
		return stats, err
	}
	if stats.Expected > maxReindexChunks {
		return stats, fmt.Errorf("knowledge reindex exceeds the %d chunk safety bound", maxReindexChunks)
	}
	for stats.Indexed < stats.Expected {
		items, err := s.loadReindexBatch(
			ctx,
			tenantID,
			stats.GenerationID,
			batchSize*embeddingWorkers,
		)
		if err != nil {
			return stats, err
		}
		if len(items) == 0 {
			return stats, errors.New("knowledge reindex stopped before the generation was complete")
		}
		batches, err := s.embedReindexBatches(ctx, items, batchSize, embeddingWorkers)
		if err != nil {
			return stats, err
		}
		for _, batch := range batches {
			written, err := s.persistReindexBatch(
				ctx,
				tenantID,
				stats.GenerationID,
				batch.items,
				batch.embedding,
			)
			if err != nil {
				return stats, err
			}
			if written == 0 {
				return stats, errors.New("knowledge reindex made no progress because the published set changed")
			}
			stats.Indexed += written
			if stats.Indexed > maxReindexChunks {
				return stats, errors.New("knowledge reindex exceeded its chunk safety bound")
			}
		}
	}
	return s.activateIndexGeneration(ctx, tenantID, stats.GenerationID)
}

type reindexEmbeddedBatch struct {
	items     []reindexItem
	embedding EmbeddingBatch
	err       error
}

func (s *Store) embedReindexBatches(
	ctx context.Context,
	items []reindexItem,
	batchSize int,
	embeddingWorkers int,
) ([]reindexEmbeddedBatch, error) {
	if len(items) == 0 || batchSize < 1 || batchSize > 128 ||
		embeddingWorkers < 1 || embeddingWorkers > maxReindexEmbeddingWorkers {
		return nil, errors.New("knowledge reindex embedding batch input is invalid")
	}
	batchCount := (len(items) + batchSize - 1) / batchSize
	if batchCount > embeddingWorkers {
		return nil, errors.New("knowledge reindex embedding batch exceeds its worker bound")
	}
	results := make([]reindexEmbeddedBatch, batchCount)
	var wait sync.WaitGroup
	for batchIndex, start := 0, 0; start < len(items); batchIndex, start = batchIndex+1, start+batchSize {
		end := min(start+batchSize, len(items))
		results[batchIndex].items = items[start:end]
		wait.Add(1)
		go func(index int) {
			defer wait.Done()
			texts := make([]string, len(results[index].items))
			for itemIndex, item := range results[index].items {
				texts[itemIndex] = item.Content
			}
			batch, err := s.embedder.Embed(ctx, texts)
			if err == nil {
				err = s.validateEmbeddingBatch(batch, len(texts))
			}
			results[index].embedding = batch
			results[index].err = err
		}(batchIndex)
	}
	wait.Wait()
	for index, result := range results {
		if result.err != nil {
			return nil, fmt.Errorf("embed knowledge reindex batch %d: %w", index, result.err)
		}
	}
	return results, nil
}

func (s *Store) beginIndexGeneration(ctx context.Context, tenantID string) (ReindexStats, error) {
	tx, err := s.pool.BeginTx(ctx, pgx.TxOptions{IsoLevel: pgx.Serializable})
	if err != nil {
		return ReindexStats{}, fmt.Errorf("begin knowledge reindex generation: %w", err)
	}
	defer func() { _ = tx.Rollback(ctx) }()
	if _, err := tx.Exec(ctx, `SELECT pg_advisory_xact_lock(hashtext($1))`, tenantID); err != nil {
		return ReindexStats{}, fmt.Errorf("lock knowledge reindex generation: %w", err)
	}
	var currentID, currentModel string
	var currentDimension, currentExpected, currentIndexed int
	err = tx.QueryRow(ctx, `
SELECT id::text, model_revision, dimension, expected_chunk_count, indexed_chunk_count
FROM knowledge.index_generations
WHERE tenant_id = $1::uuid AND state = 'active'
FOR UPDATE`, tenantID).Scan(
		&currentID, &currentModel, &currentDimension, &currentExpected, &currentIndexed,
	)
	if err != nil && !errors.Is(err, pgx.ErrNoRows) {
		return ReindexStats{}, fmt.Errorf("load active knowledge index generation: %w", err)
	}
	if err == nil && currentModel == s.config.ModelRevision &&
		currentDimension == s.config.Dimension {
		expected, indexed, countErr := countGeneration(ctx, tx, tenantID, currentID)
		if countErr != nil {
			return ReindexStats{}, countErr
		}
		if expected != indexed || currentExpected != currentIndexed {
			return ReindexStats{}, errors.New("active knowledge index generation is incomplete")
		}
		if _, err := tx.Exec(ctx, `
UPDATE knowledge.index_generations
SET expected_chunk_count = $3, indexed_chunk_count = $3, updated_at = now()
WHERE tenant_id = $1::uuid AND id = $2::uuid AND state = 'active'`,
			tenantID, currentID, expected); err != nil {
			return ReindexStats{}, fmt.Errorf("refresh active knowledge index generation: %w", err)
		}
		if err := tx.Commit(ctx); err != nil {
			return ReindexStats{}, fmt.Errorf("commit existing knowledge index generation: %w", err)
		}
		return ReindexStats{
			GenerationID: currentID, State: "active", Expected: expected,
			Indexed: indexed, Activated: true,
		}, nil
	}
	var generationID string
	err = tx.QueryRow(ctx, `
SELECT id::text
FROM knowledge.index_generations
WHERE tenant_id = $1::uuid AND model_revision = $2 AND dimension = $3
  AND state = 'building'
ORDER BY created_at, id
LIMIT 1
FOR UPDATE`, tenantID, s.config.ModelRevision, s.config.Dimension).Scan(&generationID)
	if errors.Is(err, pgx.ErrNoRows) {
		generationID, err = randomReindexUUID()
		if err != nil {
			return ReindexStats{}, err
		}
		if _, err := tx.Exec(ctx, `
INSERT INTO knowledge.index_generations
    (id, tenant_id, model_revision, dimension, storage_type, distance_metric, state)
VALUES ($1::uuid, $2::uuid, $3, $4, 'halfvec', 'cosine', 'building')`,
			generationID, tenantID, s.config.ModelRevision, s.config.Dimension); err != nil {
			return ReindexStats{}, fmt.Errorf("create knowledge index generation: %w", err)
		}
	} else if err != nil {
		return ReindexStats{}, fmt.Errorf("load building knowledge index generation: %w", err)
	}
	expected, indexed, err := countGeneration(ctx, tx, tenantID, generationID)
	if err != nil {
		return ReindexStats{}, err
	}
	if _, err := tx.Exec(ctx, `
UPDATE knowledge.index_generations
SET expected_chunk_count = $3, indexed_chunk_count = $4, updated_at = now()
WHERE tenant_id = $1::uuid AND id = $2::uuid AND state = 'building'`,
		tenantID, generationID, expected, indexed); err != nil {
		return ReindexStats{}, fmt.Errorf("refresh building knowledge index generation: %w", err)
	}
	if err := tx.Commit(ctx); err != nil {
		return ReindexStats{}, fmt.Errorf("commit building knowledge index generation: %w", err)
	}
	return ReindexStats{
		GenerationID: generationID, State: "building",
		Expected: expected, Indexed: indexed,
	}, nil
}

func (s *Store) loadReindexBatch(ctx context.Context, tenantID, generationID string, limit int) ([]reindexItem, error) {
	rows, err := s.pool.Query(ctx, `
SELECT chunk.id::text, chunk.content, chunk.checksum
FROM knowledge.documents AS document
JOIN knowledge.document_versions AS version
  ON version.tenant_id = document.tenant_id
 AND version.document_id = document.id
 AND version.id = document.current_version_id
JOIN knowledge.chunks AS chunk
  ON chunk.tenant_id = version.tenant_id
 AND chunk.document_id = version.document_id
 AND chunk.version_id = version.id
LEFT JOIN knowledge.chunk_search_indexes AS search
  ON search.tenant_id = chunk.tenant_id
 AND search.chunk_id = chunk.id
 AND search.generation_id = $2::uuid
 AND search.model_revision = $3
 AND search.dimension = $4
 AND search.content_checksum = chunk.checksum
WHERE document.tenant_id = $1::uuid
  AND document.status = 'active'
  AND version.status = 'published'
  AND version.ingestion_state IN ('legacy_indexed', 'indexed')
  AND search.chunk_id IS NULL
ORDER BY chunk.id
LIMIT $5`, tenantID, generationID, s.config.ModelRevision, s.config.Dimension, limit)
	if err != nil {
		return nil, fmt.Errorf("load knowledge reindex batch: %w", err)
	}
	defer rows.Close()
	items := make([]reindexItem, 0, limit)
	for rows.Next() {
		var item reindexItem
		if err := rows.Scan(&item.ChunkID, &item.Content, &item.Checksum); err != nil {
			return nil, fmt.Errorf("scan knowledge reindex chunk: %w", err)
		}
		items = append(items, item)
	}
	if err := rows.Err(); err != nil {
		return nil, fmt.Errorf("iterate knowledge reindex chunks: %w", err)
	}
	return items, nil
}

func (s *Store) persistReindexBatch(ctx context.Context, tenantID, generationID string, items []reindexItem, batch EmbeddingBatch) (int, error) {
	tx, err := s.pool.Begin(ctx)
	if err != nil {
		return 0, fmt.Errorf("begin knowledge reindex batch: %w", err)
	}
	defer func() { _ = tx.Rollback(ctx) }()
	written := 0
	for index, item := range items {
		vector, err := normalized(batch.Vectors[index])
		if err != nil {
			return 0, fmt.Errorf("normalize knowledge reindex chunk: %w", err)
		}
		lexemes := strings.Join(lexicalTerms(item.Content), " ")
		if lexemes == "" {
			return 0, errors.New("knowledge reindex chunk has no lexical projection")
		}
		result, err := tx.Exec(ctx, `
INSERT INTO knowledge.chunk_search_indexes
    (generation_id, tenant_id, chunk_id, model_revision, dimension,
     content_checksum, embedding, search_vector, normalized)
SELECT $2::uuid, chunk.tenant_id, chunk.id, $3, $4, chunk.checksum,
       $5::halfvec, to_tsvector('simple', $6), true
FROM knowledge.documents AS document
JOIN knowledge.document_versions AS version
  ON version.tenant_id = document.tenant_id
 AND version.document_id = document.id
 AND version.id = document.current_version_id
JOIN knowledge.chunks AS chunk
  ON chunk.tenant_id = version.tenant_id
 AND chunk.document_id = version.document_id
 AND chunk.version_id = version.id
WHERE document.tenant_id = $1::uuid
  AND document.status = 'active'
  AND version.status = 'published'
  AND version.ingestion_state IN ('legacy_indexed', 'indexed')
  AND chunk.id = $7::uuid
  AND chunk.checksum = $8
ON CONFLICT (generation_id, chunk_id) DO UPDATE SET
    model_revision = EXCLUDED.model_revision,
    dimension = EXCLUDED.dimension,
    content_checksum = EXCLUDED.content_checksum,
    embedding = EXCLUDED.embedding,
    search_vector = EXCLUDED.search_vector,
    normalized = true,
    indexed_at = now()`,
			tenantID, generationID, s.config.ModelRevision, s.config.Dimension,
			halfVectorLiteral(vector), lexemes, item.ChunkID, item.Checksum)
		if err != nil {
			return 0, fmt.Errorf("persist knowledge reindex chunk: %w", err)
		}
		written += int(result.RowsAffected())
	}
	if err := tx.Commit(ctx); err != nil {
		return 0, fmt.Errorf("commit knowledge reindex batch: %w", err)
	}
	return written, nil
}

func (s *Store) activateIndexGeneration(ctx context.Context, tenantID, generationID string) (ReindexStats, error) {
	tx, err := s.pool.BeginTx(ctx, pgx.TxOptions{IsoLevel: pgx.Serializable})
	if err != nil {
		return ReindexStats{}, fmt.Errorf("begin knowledge index activation: %w", err)
	}
	defer func() { _ = tx.Rollback(ctx) }()
	if _, err := tx.Exec(ctx, `SELECT pg_advisory_xact_lock(hashtext($1))`, tenantID); err != nil {
		return ReindexStats{}, fmt.Errorf("lock knowledge index activation: %w", err)
	}
	expected, indexed, err := countGeneration(ctx, tx, tenantID, generationID)
	if err != nil {
		return ReindexStats{}, err
	}
	if expected != indexed {
		return ReindexStats{
			GenerationID: generationID, State: "building",
			Expected: expected, Indexed: indexed,
		}, errors.New("knowledge index generation changed before activation")
	}
	if _, err := tx.Exec(ctx, `
UPDATE knowledge.index_generations
SET state = 'retired', updated_at = now()
WHERE tenant_id = $1::uuid AND state = 'active' AND id <> $2::uuid`,
		tenantID, generationID); err != nil {
		return ReindexStats{}, fmt.Errorf("retire previous knowledge index generation: %w", err)
	}
	result, err := tx.Exec(ctx, `
UPDATE knowledge.index_generations
SET state = 'active', expected_chunk_count = $3, indexed_chunk_count = $3,
    activated_at = COALESCE(activated_at, now()), updated_at = now()
WHERE tenant_id = $1::uuid AND id = $2::uuid
  AND model_revision = $4 AND dimension = $5
  AND state IN ('building', 'active')`,
		tenantID, generationID, expected, s.config.ModelRevision, s.config.Dimension)
	if err != nil {
		return ReindexStats{}, fmt.Errorf("activate knowledge index generation: %w", err)
	}
	if result.RowsAffected() != 1 {
		return ReindexStats{}, errors.New("knowledge index generation is no longer activatable")
	}
	if _, err := tx.Exec(ctx, `
INSERT INTO audit.knowledge_events (tenant_id, event_type, evidence)
VALUES ($1::uuid, 'index_generation_activated',
        jsonb_build_object('generation_id', $2::text, 'model_revision', $3::text,
                           'chunk_count', $4::integer))`,
		tenantID, generationID, s.config.ModelRevision, expected); err != nil {
		return ReindexStats{}, fmt.Errorf("audit knowledge index generation activation: %w", err)
	}
	if err := tx.Commit(ctx); err != nil {
		return ReindexStats{}, fmt.Errorf("commit knowledge index activation: %w", err)
	}
	return ReindexStats{
		GenerationID: generationID, State: "active",
		Expected: expected, Indexed: indexed, Activated: true,
	}, nil
}

func countGeneration(ctx context.Context, tx pgx.Tx, tenantID, generationID string) (int, int, error) {
	var expected, indexed int
	if err := tx.QueryRow(ctx, `
SELECT count(*)::integer,
       count(search.chunk_id) FILTER (
           WHERE search.content_checksum = chunk.checksum
       )::integer
FROM knowledge.documents AS document
JOIN knowledge.document_versions AS version
  ON version.tenant_id = document.tenant_id
 AND version.document_id = document.id
 AND version.id = document.current_version_id
JOIN knowledge.chunks AS chunk
  ON chunk.tenant_id = version.tenant_id
 AND chunk.document_id = version.document_id
 AND chunk.version_id = version.id
LEFT JOIN knowledge.chunk_search_indexes AS search
  ON search.tenant_id = chunk.tenant_id
 AND search.chunk_id = chunk.id
 AND search.generation_id = $2::uuid
WHERE document.tenant_id = $1::uuid
  AND document.status = 'active'
  AND version.status = 'published'
  AND version.ingestion_state IN ('legacy_indexed', 'indexed')`,
		tenantID, generationID).Scan(&expected, &indexed); err != nil {
		return 0, 0, fmt.Errorf("count knowledge index generation: %w", err)
	}
	return expected, indexed, nil
}

func randomReindexUUID() (string, error) {
	var value [16]byte
	if _, err := rand.Read(value[:]); err != nil {
		return "", err
	}
	value[6] = (value[6] & 0x0f) | 0x40
	value[8] = (value[8] & 0x3f) | 0x80
	encoded := hex.EncodeToString(value[:])
	return encoded[:8] + "-" + encoded[8:12] + "-" + encoded[12:16] + "-" +
		encoded[16:20] + "-" + encoded[20:], nil
}
