package retrieval

import (
	"context"
	"errors"
	"fmt"
	"math"
	"sort"
	"strings"
	"sync"
	"time"
	"unicode"
	"unicode/utf8"

	"github.com/jackc/pgx/v5/pgxpool"
	"github.com/prometheus/client_golang/prometheus"
)

var (
	retrievalQueries = prometheus.NewCounterVec(prometheus.CounterOpts{
		Namespace: "openim_platform", Subsystem: "rag", Name: "queries_total",
		Help: "Enterprise RAG queries by terminal outcome.",
	}, []string{"outcome"})
	retrievalDuration = prometheus.NewHistogram(prometheus.HistogramOpts{
		Namespace: "openim_platform", Subsystem: "rag", Name: "query_duration_seconds",
		Help: "Enterprise hybrid retrieval latency.", Buckets: prometheus.DefBuckets,
	})
	retrievalEvidence = prometheus.NewHistogram(prometheus.HistogramOpts{
		Namespace: "openim_platform", Subsystem: "rag", Name: "evidence_count",
		Help: "Evidence chunks returned by successful enterprise RAG queries.", Buckets: []float64{0, 1, 2, 4, 6, 8},
	})
)

func init() {
	prometheus.MustRegister(retrievalQueries, retrievalDuration, retrievalEvidence)
}

type Query struct {
	TenantID string
	MemberID string
	Purpose  string
	Text     string
	Limit    int
}

type Evidence struct {
	CitationID string `json:"citation_id"`
	DocumentID string `json:"document_id"`
	VersionID  string `json:"version_id"`
	ChunkID    string `json:"chunk_id"`
	Title      string `json:"title"`
	SourceURI  string `json:"source_uri"`
	Checksum   string `json:"checksum"`
	Content    string `json:"content"`
}

type EmbeddingBatch struct {
	Model     string
	Dimension int
	Vectors   [][]float32
}

type EmbeddingProvider interface {
	Embed(context.Context, []string) (EmbeddingBatch, error)
}

type Config struct {
	ModelRevision      string
	Dimension          int
	DenseMinSimilarity float64
	MaxCandidates      int
}

func (c Config) validate() error {
	if strings.TrimSpace(c.ModelRevision) == "" || c.Dimension < 8 || c.Dimension > 8192 {
		return errors.New("retrieval embedding model contract is invalid")
	}
	if c.DenseMinSimilarity < -1 || c.DenseMinSimilarity > 1 {
		return errors.New("retrieval dense minimum similarity must be in [-1, 1]")
	}
	if c.MaxCandidates < 1 || c.MaxCandidates > 10_000 {
		return errors.New("retrieval maximum candidate count must be between 1 and 10000")
	}
	return nil
}

type Store struct {
	pool     *pgxpool.Pool
	embedder EmbeddingProvider
	config   Config
	cacheMu  sync.RWMutex
	cache    map[string]cachedChunk
}

func NewStore(pool *pgxpool.Pool, embedder EmbeddingProvider, config Config) (*Store, error) {
	if pool == nil || embedder == nil {
		return nil, errors.New("hybrid retrieval dependencies are required")
	}
	if err := config.validate(); err != nil {
		return nil, err
	}
	return &Store{pool: pool, embedder: embedder, config: config, cache: make(map[string]cachedChunk)}, nil
}

type cachedChunk struct {
	content   string
	embedding []float32
}

type candidate struct {
	evidence     Evidence
	embedding    []float32
	lexicalScore float64
	denseScore   float64
	lexicalRank  int
	denseRank    int
	fusionScore  float64
}

func (s *Store) Search(ctx context.Context, query Query) (result []Evidence, err error) {
	started := time.Now()
	defer func() {
		retrievalDuration.Observe(time.Since(started).Seconds())
		if err != nil {
			retrievalQueries.WithLabelValues("error").Inc()
			return
		}
		retrievalQueries.WithLabelValues("success").Inc()
		retrievalEvidence.Observe(float64(len(result)))
	}()
	if query.TenantID == "" || query.MemberID == "" || query.Purpose != "agent_answer" {
		return nil, errors.New("retrieval identity or purpose is invalid")
	}
	if query.Limit < 1 || query.Limit > 8 {
		return nil, errors.New("retrieval limit must be between 1 and 8")
	}
	terms := lexicalTerms(query.Text)
	if len(terms) == 0 {
		return nil, errors.New("retrieval query has no searchable terms")
	}
	batch, err := s.embedder.Embed(ctx, []string{query.Text})
	if err != nil {
		return nil, fmt.Errorf("embed retrieval query: %w", err)
	}
	if err := s.validateEmbeddingBatch(batch, 1); err != nil {
		return nil, err
	}
	queryVector, err := normalized(batch.Vectors[0])
	if err != nil {
		return nil, fmt.Errorf("normalize retrieval query embedding: %w", err)
	}
	result, err = s.searchWithVector(ctx, query, terms, queryVector)
	return result, err
}

func (s *Store) searchWithVector(ctx context.Context, query Query, terms []string, queryVector []float32) ([]Evidence, error) {
	const statement = `
SELECT d.id::text, v.id::text, c.id::text, d.title, d.source_uri, c.checksum, e.chunk_id IS NULL,
	       COALESCE(e.content_checksum, ''), COALESCE(e.dimension, 0), COALESCE(e.normalized, false)
FROM knowledge.documents AS d
JOIN knowledge.document_versions AS v
  ON v.document_id = d.id AND v.id = d.current_version_id
JOIN knowledge.chunks AS c
  ON c.document_id = d.id AND c.version_id = v.id AND c.tenant_id = d.tenant_id
JOIN authz.document_grants AS g
  ON g.tenant_id = d.tenant_id AND g.document_id = d.id
 AND g.member_id = $2::uuid AND g.permission = 'read'
LEFT JOIN knowledge.chunk_embeddings AS e
  ON e.chunk_id = c.id AND e.model_revision = $3
WHERE d.tenant_id = $1::uuid
  AND d.status = 'active'
  AND v.status = 'published'
  AND d.classification IN ('public', 'internal')
ORDER BY c.id
LIMIT $4`
	rows, err := s.pool.Query(ctx, statement, query.TenantID, query.MemberID, s.config.ModelRevision, s.config.MaxCandidates+1)
	if err != nil {
		return nil, fmt.Errorf("load authorized hybrid retrieval candidates: %w", err)
	}
	defer rows.Close()
	items := make([]candidate, 0)
	for rows.Next() {
		var item candidate
		var missing, storedNormalized bool
		var contentChecksum string
		var dimension int
		if err := rows.Scan(&item.evidence.DocumentID, &item.evidence.VersionID, &item.evidence.ChunkID,
			&item.evidence.Title, &item.evidence.SourceURI, &item.evidence.Checksum,
			&missing, &contentChecksum, &dimension, &storedNormalized); err != nil {
			return nil, fmt.Errorf("scan hybrid retrieval candidate: %w", err)
		}
		if missing || contentChecksum != item.evidence.Checksum || dimension != s.config.Dimension || !storedNormalized {
			return nil, fmt.Errorf("knowledge chunk %s has no valid %s embedding", item.evidence.ChunkID, s.config.ModelRevision)
		}
		items = append(items, item)
	}
	if err := rows.Err(); err != nil {
		return nil, fmt.Errorf("iterate authorized hybrid retrieval candidates: %w", err)
	}
	if len(items) > s.config.MaxCandidates {
		return nil, fmt.Errorf("authorized knowledge exceeds bounded hybrid scan of %d chunks", s.config.MaxCandidates)
	}
	if err := s.hydrateCandidates(ctx, items); err != nil {
		return nil, err
	}
	return rankCandidates(items, terms, queryVector, s.config.DenseMinSimilarity, query.Limit), nil
}

func (s *Store) hydrateCandidates(ctx context.Context, items []candidate) error {
	missing := make([]string, 0)
	for index := range items {
		key := s.cacheKey(items[index].evidence.ChunkID, items[index].evidence.Checksum)
		s.cacheMu.RLock()
		cached, ok := s.cache[key]
		s.cacheMu.RUnlock()
		if ok {
			items[index].evidence.Content = cached.content
			items[index].embedding = cached.embedding
			continue
		}
		missing = append(missing, items[index].evidence.ChunkID)
	}
	if len(missing) > 0 {
		rows, err := s.pool.Query(ctx, `
SELECT c.id::text, c.checksum, c.content, e.embedding
FROM knowledge.chunks AS c
JOIN knowledge.chunk_embeddings AS e
  ON e.chunk_id = c.id AND e.model_revision = $1
WHERE c.id = ANY($2::uuid[])`, s.config.ModelRevision, missing)
		if err != nil {
			return fmt.Errorf("load uncached knowledge embeddings: %w", err)
		}
		loaded := 0
		for rows.Next() {
			var chunkID, checksum, content string
			var embedding []float32
			if err := rows.Scan(&chunkID, &checksum, &content, &embedding); err != nil {
				rows.Close()
				return fmt.Errorf("scan uncached knowledge embedding: %w", err)
			}
			if len(embedding) != s.config.Dimension {
				rows.Close()
				return fmt.Errorf("knowledge chunk %s embedding dimension is invalid", chunkID)
			}
			s.cacheMu.Lock()
			s.cache[s.cacheKey(chunkID, checksum)] = cachedChunk{content: content, embedding: embedding}
			s.cacheMu.Unlock()
			loaded++
		}
		err = rows.Err()
		rows.Close()
		if err != nil {
			return fmt.Errorf("iterate uncached knowledge embeddings: %w", err)
		}
		if loaded != len(missing) {
			return errors.New("knowledge embedding cache hydration was incomplete")
		}
	}
	for index := range items {
		key := s.cacheKey(items[index].evidence.ChunkID, items[index].evidence.Checksum)
		s.cacheMu.RLock()
		cached, ok := s.cache[key]
		s.cacheMu.RUnlock()
		if !ok {
			return fmt.Errorf("knowledge chunk %s was not hydrated", items[index].evidence.ChunkID)
		}
		items[index].evidence.Content = cached.content
		items[index].embedding = cached.embedding
	}
	return nil
}

func (s *Store) cacheKey(chunkID, checksum string) string {
	return s.config.ModelRevision + "\x00" + chunkID + "\x00" + checksum
}

func (s *Store) validateEmbeddingBatch(batch EmbeddingBatch, count int) error {
	if batch.Model != s.config.ModelRevision || batch.Dimension != s.config.Dimension || len(batch.Vectors) != count {
		return errors.New("embedding provider response does not match the retrieval model contract")
	}
	for _, vector := range batch.Vectors {
		if len(vector) != s.config.Dimension {
			return errors.New("embedding provider returned an invalid vector dimension")
		}
	}
	return nil
}

func rankCandidates(items []candidate, terms []string, queryVector []float32, denseMinimum float64, limit int) []Evidence {
	documentFrequency := make(map[string]int, len(terms))
	for _, item := range items {
		haystack := strings.ToLower(item.evidence.Title + "\n" + item.evidence.Content)
		for _, term := range terms {
			if strings.Contains(haystack, term) {
				documentFrequency[term]++
			}
		}
	}
	for index := range items {
		haystack := strings.ToLower(items[index].evidence.Title + "\n" + items[index].evidence.Content)
		for _, term := range terms {
			if strings.Contains(haystack, term) {
				items[index].lexicalScore += math.Log((float64(len(items))+1)/(float64(documentFrequency[term])+1)) + 1
			}
		}
		items[index].denseScore = dot(queryVector, items[index].embedding)
	}
	lexicalOrder := make([]int, 0, len(items))
	denseOrder := make([]int, 0, len(items))
	for index := range items {
		if items[index].lexicalScore > 0 {
			lexicalOrder = append(lexicalOrder, index)
		}
		if items[index].denseScore >= denseMinimum {
			denseOrder = append(denseOrder, index)
		}
	}
	sort.Slice(lexicalOrder, func(i, j int) bool {
		left, right := items[lexicalOrder[i]], items[lexicalOrder[j]]
		if left.lexicalScore != right.lexicalScore {
			return left.lexicalScore > right.lexicalScore
		}
		return left.evidence.ChunkID < right.evidence.ChunkID
	})
	sort.Slice(denseOrder, func(i, j int) bool {
		left, right := items[denseOrder[i]], items[denseOrder[j]]
		if left.denseScore != right.denseScore {
			return left.denseScore > right.denseScore
		}
		return left.evidence.ChunkID < right.evidence.ChunkID
	})
	for rank, index := range lexicalOrder {
		items[index].lexicalRank = rank + 1
	}
	for rank, index := range denseOrder {
		items[index].denseRank = rank + 1
	}
	ranked := make([]candidate, 0, len(items))
	for _, item := range items {
		if item.lexicalRank == 0 && item.denseRank == 0 {
			continue
		}
		if item.lexicalRank > 0 {
			item.fusionScore += 2 / float64(60+item.lexicalRank)
		}
		if item.denseRank > 0 {
			item.fusionScore += 1 / float64(60+item.denseRank)
		}
		ranked = append(ranked, item)
	}
	sort.Slice(ranked, func(i, j int) bool {
		if ranked[i].fusionScore != ranked[j].fusionScore {
			return ranked[i].fusionScore > ranked[j].fusionScore
		}
		if ranked[i].lexicalScore != ranked[j].lexicalScore {
			return ranked[i].lexicalScore > ranked[j].lexicalScore
		}
		if ranked[i].denseScore != ranked[j].denseScore {
			return ranked[i].denseScore > ranked[j].denseScore
		}
		return ranked[i].evidence.ChunkID < ranked[j].evidence.ChunkID
	})
	if len(ranked) > limit {
		ranked = ranked[:limit]
	}
	result := make([]Evidence, len(ranked))
	for index := range ranked {
		result[index] = ranked[index].evidence
		result[index].CitationID = fmt.Sprintf("C%d", index+1)
	}
	return result
}

func normalized(vector []float32) ([]float32, error) {
	var squared float64
	for _, value := range vector {
		if math.IsNaN(float64(value)) || math.IsInf(float64(value), 0) {
			return nil, errors.New("embedding vector contains a non-finite value")
		}
		squared += float64(value * value)
	}
	if squared == 0 {
		return nil, errors.New("embedding vector has zero norm")
	}
	norm := float32(math.Sqrt(squared))
	result := make([]float32, len(vector))
	for index, value := range vector {
		result[index] = value / norm
	}
	return result, nil
}

func dot(left, right []float32) float64 {
	if len(left) != len(right) {
		return -1
	}
	var value float64
	for index := range left {
		value += float64(left[index] * right[index])
	}
	return value
}

func lexicalTerms(text string) []string {
	fields := strings.FieldsFunc(strings.ToLower(text), func(r rune) bool {
		return unicode.IsSpace(r) || unicode.IsPunct(r) || unicode.IsSymbol(r)
	})
	seen := make(map[string]struct{})
	terms := make([]string, 0, 32)
	add := func(term string) {
		term = strings.TrimSpace(term)
		if utf8.RuneCountInString(term) < 2 {
			return
		}
		if _, exists := seen[term]; exists || len(terms) == 64 {
			return
		}
		seen[term] = struct{}{}
		terms = append(terms, term)
	}
	for _, field := range fields {
		add(field)
		var chinese []rune
		for _, char := range field {
			if unicode.Is(unicode.Han, char) {
				chinese = append(chinese, char)
			}
		}
		for index := 0; index+1 < len(chinese); index++ {
			add(string(chinese[index : index+2]))
		}
	}
	return terms
}
