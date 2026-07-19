package retrieval

import (
	"context"
	"fmt"
)

type IndexStats struct {
	Indexed int `json:"indexed"`
}

type indexItem struct {
	ChunkID  string
	Content  string
	Checksum string
}

func (s *Store) IndexMissing(ctx context.Context, batchSize int) (IndexStats, error) {
	if batchSize < 1 || batchSize > 128 {
		return IndexStats{}, fmt.Errorf("embedding index batch size must be between 1 and 128")
	}
	stats := IndexStats{}
	for {
		const statement = `
SELECT c.id::text, c.content, c.checksum
FROM knowledge.documents AS d
JOIN knowledge.document_versions AS v
  ON v.document_id = d.id AND v.id = d.current_version_id
JOIN knowledge.chunks AS c
  ON c.document_id = d.id AND c.version_id = v.id AND c.tenant_id = d.tenant_id
LEFT JOIN knowledge.chunk_embeddings AS e
  ON e.chunk_id = c.id AND e.model_revision = $1
WHERE d.status = 'active' AND v.status = 'published'
  AND (e.chunk_id IS NULL OR e.content_checksum <> c.checksum OR e.dimension <> $2 OR NOT e.normalized)
ORDER BY c.id
LIMIT $3`
		rows, err := s.pool.Query(ctx, statement, s.config.ModelRevision, s.config.Dimension, batchSize)
		if err != nil {
			return stats, fmt.Errorf("load chunks requiring embeddings: %w", err)
		}
		items := make([]indexItem, 0, batchSize)
		for rows.Next() {
			var item indexItem
			if err := rows.Scan(&item.ChunkID, &item.Content, &item.Checksum); err != nil {
				rows.Close()
				return stats, fmt.Errorf("scan chunk requiring embedding: %w", err)
			}
			items = append(items, item)
		}
		err = rows.Err()
		rows.Close()
		if err != nil {
			return stats, fmt.Errorf("iterate chunks requiring embeddings: %w", err)
		}
		if len(items) == 0 {
			return stats, nil
		}
		texts := make([]string, len(items))
		for index := range items {
			texts[index] = items[index].Content
		}
		batch, err := s.embedder.Embed(ctx, texts)
		if err != nil {
			return stats, fmt.Errorf("embed knowledge chunks: %w", err)
		}
		if err := s.validateEmbeddingBatch(batch, len(items)); err != nil {
			return stats, err
		}
		tx, err := s.pool.Begin(ctx)
		if err != nil {
			return stats, fmt.Errorf("begin knowledge embedding batch: %w", err)
		}
		for index, item := range items {
			vector, err := normalized(batch.Vectors[index])
			if err != nil {
				_ = tx.Rollback(ctx)
				return stats, fmt.Errorf("normalize knowledge chunk %s: %w", item.ChunkID, err)
			}
			if _, err := tx.Exec(ctx, `
INSERT INTO knowledge.chunk_embeddings
    (chunk_id, model_revision, dimension, content_checksum, embedding, normalized, indexed_at)
VALUES ($1::uuid, $2, $3, $4, $5::real[], true, now())
ON CONFLICT (chunk_id, model_revision) DO UPDATE SET
    dimension = EXCLUDED.dimension,
    content_checksum = EXCLUDED.content_checksum,
    embedding = EXCLUDED.embedding,
    normalized = true,
    indexed_at = now()`, item.ChunkID, s.config.ModelRevision, s.config.Dimension, item.Checksum, vector); err != nil {
				_ = tx.Rollback(ctx)
				return stats, fmt.Errorf("persist knowledge chunk embedding: %w", err)
			}
		}
		if err := tx.Commit(ctx); err != nil {
			return stats, fmt.Errorf("commit knowledge embedding batch: %w", err)
		}
		stats.Indexed += len(items)
	}
}
