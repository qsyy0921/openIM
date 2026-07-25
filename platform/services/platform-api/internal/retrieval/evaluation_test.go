package retrieval

import (
	"context"
	"errors"
	"fmt"
	"math"
	"sort"
	"strconv"
	"strings"
	"sync"
	"testing"
	"time"
)

type evaluationMeasuringEmbedder struct {
	mu         sync.Mutex
	active     int
	maxActive  int
	batchSizes []int
	failOn     string
}

func (e *evaluationMeasuringEmbedder) Embed(_ context.Context, texts []string) (EmbeddingBatch, error) {
	e.mu.Lock()
	e.active++
	if e.active > e.maxActive {
		e.maxActive = e.active
	}
	e.batchSizes = append(e.batchSizes, len(texts))
	e.mu.Unlock()
	defer func() {
		e.mu.Lock()
		e.active--
		e.mu.Unlock()
	}()
	time.Sleep(20 * time.Millisecond)
	vectors := make([][]float32, len(texts))
	for index, text := range texts {
		if text == e.failOn {
			return EmbeddingBatch{}, errors.New("fixed embedding failure")
		}
		id, err := strconv.Atoi(strings.TrimPrefix(text, "question-"))
		if err != nil {
			return EmbeddingBatch{}, fmt.Errorf("parse test question: %w", err)
		}
		vectors[index] = make([]float32, 2560)
		vectors[index][id+1] = 1
	}
	return EmbeddingBatch{
		Model: "qwen3-embedding:4b", Dimension: 2560, Vectors: vectors,
	}, nil
}

func TestEmbedEvaluationQuestionsUsesMeasuredBoundsAndPreservesOrder(t *testing.T) {
	embedder := &evaluationMeasuringEmbedder{}
	store := &Store{
		embedder: embedder,
		config: Config{
			ModelRevision: "qwen3-embedding:4b",
			Dimension:     2560,
		},
	}
	cases := make([]QACase, 13)
	for index := range cases {
		cases[index].Question = fmt.Sprintf("question-%d", index)
	}
	vectors, err := store.embedEvaluationQuestions(context.Background(), cases)
	if err != nil {
		t.Fatal(err)
	}
	if len(vectors) != len(cases) {
		t.Fatalf("embedding count = %d, want %d", len(vectors), len(cases))
	}
	for index, vector := range vectors {
		if vector[index+1] != 1 {
			t.Fatalf("embedding %d was not preserved in QA order", index)
		}
	}
	embedder.mu.Lock()
	maxActive := embedder.maxActive
	batchSizes := append([]int(nil), embedder.batchSizes...)
	embedder.mu.Unlock()
	if maxActive != evaluationEmbeddingWorkers {
		t.Fatalf("maximum embedding concurrency = %d, want %d", maxActive, evaluationEmbeddingWorkers)
	}
	sort.Ints(batchSizes)
	if fmt.Sprint(batchSizes) != "[1 4 4 4]" {
		t.Fatalf("embedding batch sizes = %v, want [1 4 4 4]", batchSizes)
	}
}

func TestEmbedEvaluationQuestionsFailsClosed(t *testing.T) {
	embedder := &evaluationMeasuringEmbedder{failOn: "question-5"}
	store := &Store{
		embedder: embedder,
		config: Config{
			ModelRevision: "qwen3-embedding:4b",
			Dimension:     2560,
		},
	}
	cases := make([]QACase, 9)
	for index := range cases {
		cases[index].Question = fmt.Sprintf("question-%d", index)
	}
	if _, err := store.embedEvaluationQuestions(context.Background(), cases); err == nil ||
		!strings.Contains(err.Error(), "fixed embedding failure") {
		t.Fatalf("embedding failure was not preserved: %v", err)
	}
}

func TestRankedMetricsUseStableBinaryRelevance(t *testing.T) {
	results := []Evidence{
		{ChunkID: "noise-1"},
		{ChunkID: "expected-a"},
		{ChunkID: "noise-2"},
		{ChunkID: "expected-b"},
	}
	expected := map[string]struct{}{"expected-a": {}, "expected-b": {}}

	first, relevant5, relevant10 := rankedRelevance(results, expected)
	if first != 2 || relevant5 != 2 || relevant10 != 2 {
		t.Fatalf("unexpected relevance result: first=%d at5=%d at10=%d", first, relevant5, relevant10)
	}
	want := (1/math.Log2(3) + 1/math.Log2(5)) / (1 + 1/math.Log2(3))
	if got := normalizedDiscountedGain(results, expected); math.Abs(got-want) > 1e-12 {
		t.Fatalf("unexpected nDCG: got %.12f want %.12f", got, want)
	}
}

func TestRankedMetricsDoNotCountDuplicateGoldRowsTwice(t *testing.T) {
	results := []Evidence{{ChunkID: "expected"}}
	expected := map[string]struct{}{"expected": {}}
	first, relevant5, relevant10 := rankedRelevance(results, expected)
	if first != 1 || relevant5 != 1 || relevant10 != 1 {
		t.Fatalf("unexpected duplicate-safe relevance: %d %d %d", first, relevant5, relevant10)
	}
	if got := normalizedDiscountedGain(results, expected); got != 1 {
		t.Fatalf("expected perfect nDCG, got %f", got)
	}
}
