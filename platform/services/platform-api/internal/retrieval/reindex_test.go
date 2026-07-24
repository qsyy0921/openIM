package retrieval

import (
	"context"
	"sync"
	"testing"
	"time"
)

type measuringEmbedder struct {
	mu          sync.Mutex
	active      int
	maxActive   int
	invalidText bool
}

func (e *measuringEmbedder) Embed(_ context.Context, texts []string) (EmbeddingBatch, error) {
	e.mu.Lock()
	e.active++
	for _, text := range texts {
		if text != "Policy\n\ncontent" {
			e.invalidText = true
		}
	}
	if e.active > e.maxActive {
		e.maxActive = e.active
	}
	e.mu.Unlock()
	time.Sleep(25 * time.Millisecond)
	vectors := make([][]float32, len(texts))
	for index := range vectors {
		vectors[index] = make([]float32, 2560)
		vectors[index][0] = float32(index + 1)
	}
	e.mu.Lock()
	e.active--
	e.mu.Unlock()
	return EmbeddingBatch{
		Model: "qwen3-embedding:4b", Dimension: 2560, Vectors: vectors,
	}, nil
}

func TestEmbedReindexBatchesUsesExplicitBoundedConcurrency(t *testing.T) {
	embedder := &measuringEmbedder{}
	store := &Store{
		embedder: embedder,
		config: Config{
			ModelRevision:      "qwen3-embedding:4b",
			ProjectionRevision: "document-title-content-v1",
			Dimension:          2560,
		},
	}
	items := make([]reindexItem, 32)
	for index := range items {
		items[index] = reindexItem{
			ChunkID: string(rune('a' + index)),
			Title:   "Policy",
			Content: "content",
		}
	}
	batches, err := store.embedReindexBatches(context.Background(), items, 4, 8)
	if err != nil {
		t.Fatal(err)
	}
	if len(batches) != 8 {
		t.Fatalf("embedReindexBatches() returned %d batches", len(batches))
	}
	embedder.mu.Lock()
	maxActive := embedder.maxActive
	embedder.mu.Unlock()
	if maxActive != 8 {
		t.Fatalf("maximum embedding concurrency = %d, want 8", maxActive)
	}
	if embedder.invalidText {
		t.Fatal("reindex embedder did not receive the title/content projection")
	}
	for index, batch := range batches {
		if len(batch.items) != 4 || len(batch.embedding.Vectors) != 4 {
			t.Fatalf("batch %d violated its fixed size", index)
		}
	}
}

func TestEmbedReindexBatchesRejectsUnboundedConcurrency(t *testing.T) {
	store := &Store{}
	items := []reindexItem{{ChunkID: "chunk", Title: "Policy", Content: "content"}}
	if _, err := store.embedReindexBatches(
		context.Background(),
		items,
		1,
		maxReindexEmbeddingWorkers+1,
	); err == nil {
		t.Fatal("unbounded embedding concurrency was accepted")
	}
}
