package retrieval

import (
	"math"
	"testing"
)

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
