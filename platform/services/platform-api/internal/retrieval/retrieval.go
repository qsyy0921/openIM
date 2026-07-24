package retrieval

import (
	"context"
	"errors"
	"fmt"
	"math"
	"sort"
	"strconv"
	"strings"
	"time"
	"unicode"
	"unicode/utf8"

	"github.com/jackc/pgx/v5"
	"github.com/jackc/pgx/v5/pgxpool"
	"github.com/prometheus/client_golang/prometheus"
	"github.com/qsyy0921/openim/platform/services/platform-api/internal/knowledgeprojection"
)

const (
	maxQueryBytes        = 2000
	maxBranchCandidates  = 32
	maxFusionCandidates  = 64
	maxRerankCandidates  = 32
	maxEvidenceItems     = 8
	maxEvidenceBytes     = 24 << 10
	maxChunksPerDocument = 3
	maxRerankerTextBytes = 8000
	rrfK                 = 60
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
	CitationID         string  `json:"citation_id"`
	DocumentID         string  `json:"document_id"`
	VersionID          string  `json:"version_id"`
	ChunkID            string  `json:"chunk_id"`
	Title              string  `json:"title"`
	SourceURI          string  `json:"source_uri"`
	Checksum           string  `json:"checksum"`
	Content            string  `json:"content"`
	LexicalRank        int     `json:"lexical_rank,omitempty"`
	DenseRank          int     `json:"dense_rank,omitempty"`
	FusionScore        float64 `json:"fusion_score,omitempty"`
	RerankerScore      float64 `json:"reranker_score,omitempty"`
	IndexRevision      string  `json:"index_revision,omitempty"`
	ProjectionRevision string  `json:"projection_revision,omitempty"`
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
	ProjectionRevision string
	Dimension          int
	DenseMinSimilarity float64
	MaxCandidates      int
	RerankerModel      string
	RerankerRevision   string
	HNSWEFSearch       int
}

func (c Config) validate() error {
	if strings.TrimSpace(c.ModelRevision) == "" || c.Dimension != 2560 {
		return errors.New("retrieval embedding model contract is invalid")
	}
	if c.ProjectionRevision != knowledgeprojection.Revision {
		return errors.New("retrieval projection revision contract is invalid")
	}
	if c.DenseMinSimilarity < -1 || c.DenseMinSimilarity > 1 {
		return errors.New("retrieval dense minimum similarity must be in [-1, 1]")
	}
	if c.MaxCandidates != maxBranchCandidates {
		return fmt.Errorf("retrieval maximum candidates must equal %d", maxBranchCandidates)
	}
	if c.RerankerModel != LockedRerankerModel || c.RerankerRevision != LockedRerankerRevision {
		return errors.New("retrieval reranker contract is invalid")
	}
	if c.HNSWEFSearch < maxBranchCandidates || c.HNSWEFSearch > 1000 {
		return errors.New("retrieval HNSW ef_search must be between 32 and 1000")
	}
	return nil
}

type Store struct {
	pool     *pgxpool.Pool
	embedder EmbeddingProvider
	reranker Reranker
	config   Config
}

func NewStore(pool *pgxpool.Pool, embedder EmbeddingProvider, reranker Reranker, config Config) (*Store, error) {
	if pool == nil || embedder == nil || reranker == nil {
		return nil, errors.New("hybrid retrieval dependencies are required")
	}
	if err := config.validate(); err != nil {
		return nil, err
	}
	return &Store{pool: pool, embedder: embedder, reranker: reranker, config: config}, nil
}

type candidate struct {
	evidence    Evidence
	lexicalRank int
	denseRank   int
	bestRank    int
	fusionScore float64
	rerankScore float64
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
	query.Text = strings.TrimSpace(query.Text)
	if query.TenantID == "" || query.MemberID == "" || query.Purpose != "agent_answer" {
		return nil, errors.New("retrieval identity or purpose is invalid")
	}
	if query.Limit < 1 || query.Limit > maxEvidenceItems {
		return nil, fmt.Errorf("retrieval limit must be between 1 and %d", maxEvidenceItems)
	}
	if len(query.Text) < 1 || len(query.Text) > maxQueryBytes || !utf8.ValidString(query.Text) {
		return nil, errors.New("retrieval query exceeds the bounded UTF-8 contract")
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
	return s.searchWithVector(ctx, query, terms, queryVector)
}

func (s *Store) searchWithVector(ctx context.Context, query Query, terms []string, queryVector []float32) ([]Evidence, error) {
	candidates, err := s.rerankedCandidatesWithVector(ctx, query, terms, queryVector)
	if err != nil {
		return nil, err
	}
	return selectEvidence(candidates, query.Limit), nil
}

func (s *Store) rerankedCandidatesWithVector(ctx context.Context, query Query, terms []string, queryVector []float32) ([]candidate, error) {
	candidates, err := s.authorizedCandidates(ctx, query, terms, queryVector)
	if err != nil {
		return nil, err
	}
	if len(candidates) == 0 {
		return []candidate{}, nil
	}
	rerankInput := make([]RerankCandidate, len(candidates))
	for index := range candidates {
		content, err := knowledgeprojection.RerankerText(
			candidates[index].evidence.Title,
			candidates[index].evidence.Content,
			maxRerankerTextBytes,
		)
		if err != nil {
			return nil, fmt.Errorf("build knowledge reranker projection: %w", err)
		}
		rerankInput[index] = RerankCandidate{
			CandidateID: candidates[index].evidence.ChunkID,
			Content:     content,
		}
	}
	reranked, err := s.reranker.Rerank(ctx, query.Text, rerankInput)
	if err != nil {
		return nil, fmt.Errorf("rerank authorized knowledge evidence: %w", err)
	}
	if err := applyRerankerScores(candidates, reranked, s.config); err != nil {
		return nil, err
	}
	return candidates, nil
}

func (s *Store) authorizedCandidates(ctx context.Context, query Query, terms []string, queryVector []float32) ([]candidate, error) {
	tx, err := s.pool.BeginTx(ctx, pgx.TxOptions{AccessMode: pgx.ReadOnly})
	if err != nil {
		return nil, fmt.Errorf("begin authorized retrieval snapshot: %w", err)
	}
	defer func() { _ = tx.Rollback(ctx) }()
	if _, err := tx.Exec(ctx, "SET LOCAL hnsw.ef_search = "+strconv.Itoa(s.config.HNSWEFSearch)); err != nil {
		return nil, fmt.Errorf("configure HNSW bounded scan: %w", err)
	}
	if _, err := tx.Exec(ctx, "SET LOCAL hnsw.iterative_scan = strict_order"); err != nil {
		return nil, fmt.Errorf("configure HNSW iterative scan: %w", err)
	}
	byID := make(map[string]*candidate, maxFusionCandidates)
	lexicalQuery := lexicalTSQuery(terms)
	rows, err := tx.Query(ctx, authorizedLexicalSQL, query.TenantID, query.MemberID,
		s.config.ModelRevision, s.config.Dimension, s.config.ProjectionRevision,
		lexicalQuery, s.config.MaxCandidates)
	if err != nil {
		return nil, fmt.Errorf("query authorized lexical candidates: %w", err)
	}
	rank := 0
	for rows.Next() {
		rank++
		item, err := scanCandidate(rows)
		if err != nil {
			rows.Close()
			return nil, err
		}
		item.lexicalRank = rank
		item.bestRank = rank
		item.fusionScore = 1 / float64(rrfK+rank)
		byID[item.evidence.ChunkID] = &item
	}
	if err := rows.Err(); err != nil {
		rows.Close()
		return nil, fmt.Errorf("iterate authorized lexical candidates: %w", err)
	}
	rows.Close()
	rows, err = tx.Query(ctx, authorizedDenseSQL, query.TenantID, query.MemberID,
		s.config.ModelRevision, s.config.Dimension, s.config.ProjectionRevision,
		halfVectorLiteral(queryVector),
		s.config.DenseMinSimilarity, s.config.MaxCandidates)
	if err != nil {
		return nil, fmt.Errorf("query authorized dense candidates: %w", err)
	}
	rank = 0
	for rows.Next() {
		rank++
		item, err := scanCandidate(rows)
		if err != nil {
			rows.Close()
			return nil, err
		}
		if existing := byID[item.evidence.ChunkID]; existing != nil {
			existing.denseRank = rank
			existing.bestRank = min(existing.bestRank, rank)
			existing.fusionScore += 1 / float64(rrfK+rank)
		} else {
			item.denseRank = rank
			item.bestRank = rank
			item.fusionScore = 1 / float64(rrfK+rank)
			byID[item.evidence.ChunkID] = &item
		}
	}
	if err := rows.Err(); err != nil {
		rows.Close()
		return nil, fmt.Errorf("iterate authorized dense candidates: %w", err)
	}
	rows.Close()
	if err := tx.Commit(ctx); err != nil {
		return nil, fmt.Errorf("commit authorized retrieval snapshot: %w", err)
	}
	if len(byID) > maxFusionCandidates {
		return nil, errors.New("hybrid retrieval exceeded the bounded fusion set")
	}
	result := make([]candidate, 0, len(byID))
	for _, item := range byID {
		result = append(result, *item)
	}
	sort.Slice(result, func(i, j int) bool {
		if result[i].fusionScore != result[j].fusionScore {
			return result[i].fusionScore > result[j].fusionScore
		}
		if result[i].bestRank != result[j].bestRank {
			return result[i].bestRank < result[j].bestRank
		}
		return result[i].evidence.ChunkID < result[j].evidence.ChunkID
	})
	if len(result) > maxRerankCandidates {
		result = result[:maxRerankCandidates]
	}
	return result, nil
}

const authorizedLexicalSQL = `
SELECT document.id::text, version.id::text, chunk.id::text,
       document.title, document.source_uri, chunk.checksum, chunk.content,
       generation.model_revision, generation.projection_revision
FROM knowledge.documents AS document
JOIN identity.members AS member
  ON member.tenant_id = document.tenant_id
 AND member.id = $2::uuid
 AND member.status = 'active'
JOIN knowledge.document_versions AS version
  ON version.tenant_id = document.tenant_id
 AND version.document_id = document.id
 AND version.id = document.current_version_id
JOIN authz.document_grants AS document_grant
  ON document_grant.tenant_id = document.tenant_id
 AND document_grant.document_id = document.id
 AND document_grant.member_id = $2::uuid
 AND document_grant.permission = 'read'
JOIN knowledge.chunks AS chunk
  ON chunk.tenant_id = version.tenant_id
 AND chunk.document_id = version.document_id
 AND chunk.version_id = version.id
JOIN knowledge.index_generations AS generation
  ON generation.tenant_id = document.tenant_id
 AND generation.state = 'active'
 AND generation.model_revision = $3
 AND generation.dimension = $4
 AND generation.projection_revision = $5
JOIN knowledge.chunk_search_indexes AS search
  ON search.tenant_id = chunk.tenant_id
 AND search.generation_id = generation.id
 AND search.chunk_id = chunk.id
 AND search.model_revision = generation.model_revision
 AND search.projection_revision = generation.projection_revision
 AND search.dimension = generation.dimension
 AND search.content_checksum = chunk.checksum
WHERE document.tenant_id = $1::uuid
  AND document.status = 'active'
  AND version.status = 'published'
  AND version.ingestion_state IN ('legacy_indexed', 'indexed')
  AND document.classification IN ('public', 'internal')
  AND search.search_vector @@ to_tsquery('simple', $6)
ORDER BY ts_rank_cd(search.search_vector, to_tsquery('simple', $6)) DESC, chunk.id
LIMIT $7`

const authorizedDenseSQL = `
SELECT document.id::text, version.id::text, chunk.id::text,
       document.title, document.source_uri, chunk.checksum, chunk.content,
       generation.model_revision, generation.projection_revision
FROM knowledge.documents AS document
JOIN identity.members AS member
  ON member.tenant_id = document.tenant_id
 AND member.id = $2::uuid
 AND member.status = 'active'
JOIN knowledge.document_versions AS version
  ON version.tenant_id = document.tenant_id
 AND version.document_id = document.id
 AND version.id = document.current_version_id
JOIN authz.document_grants AS document_grant
  ON document_grant.tenant_id = document.tenant_id
 AND document_grant.document_id = document.id
 AND document_grant.member_id = $2::uuid
 AND document_grant.permission = 'read'
JOIN knowledge.chunks AS chunk
  ON chunk.tenant_id = version.tenant_id
 AND chunk.document_id = version.document_id
 AND chunk.version_id = version.id
JOIN knowledge.index_generations AS generation
  ON generation.tenant_id = document.tenant_id
 AND generation.state = 'active'
 AND generation.model_revision = $3
 AND generation.dimension = $4
 AND generation.projection_revision = $5
JOIN knowledge.chunk_search_indexes AS search
  ON search.tenant_id = chunk.tenant_id
 AND search.generation_id = generation.id
 AND search.chunk_id = chunk.id
 AND search.model_revision = generation.model_revision
 AND search.projection_revision = generation.projection_revision
 AND search.dimension = generation.dimension
 AND search.content_checksum = chunk.checksum
WHERE document.tenant_id = $1::uuid
  AND document.status = 'active'
  AND version.status = 'published'
  AND version.ingestion_state IN ('legacy_indexed', 'indexed')
  AND document.classification IN ('public', 'internal')
  AND 1 - (search.embedding <=> $6::halfvec) >= $7
ORDER BY search.embedding <=> $6::halfvec, chunk.id
LIMIT $8`

type candidateScanner interface {
	Scan(...any) error
}

func scanCandidate(row candidateScanner) (candidate, error) {
	var item candidate
	if err := row.Scan(
		&item.evidence.DocumentID, &item.evidence.VersionID, &item.evidence.ChunkID,
		&item.evidence.Title, &item.evidence.SourceURI, &item.evidence.Checksum,
		&item.evidence.Content, &item.evidence.IndexRevision,
		&item.evidence.ProjectionRevision,
	); err != nil {
		return candidate{}, fmt.Errorf("scan authorized retrieval candidate: %w", err)
	}
	return item, nil
}

func applyRerankerScores(items []candidate, response RerankResponse, config Config) error {
	if response.Model != config.RerankerModel || response.Revision != config.RerankerRevision ||
		len(response.Scores) != len(items) {
		return errors.New("reranker response violates the retrieval contract")
	}
	byID := make(map[string]float64, len(response.Scores))
	for _, score := range response.Scores {
		if score.CandidateID == "" || math.IsNaN(score.Score) || math.IsInf(score.Score, 0) {
			return errors.New("reranker response contains an invalid score")
		}
		if _, exists := byID[score.CandidateID]; exists {
			return errors.New("reranker response contains duplicate candidates")
		}
		byID[score.CandidateID] = score.Score
	}
	for index := range items {
		score, ok := byID[items[index].evidence.ChunkID]
		if !ok {
			return errors.New("reranker response omitted an authorized candidate")
		}
		items[index].rerankScore = score
	}
	sort.Slice(items, func(i, j int) bool {
		if items[i].rerankScore != items[j].rerankScore {
			return items[i].rerankScore > items[j].rerankScore
		}
		if items[i].fusionScore != items[j].fusionScore {
			return items[i].fusionScore > items[j].fusionScore
		}
		return items[i].evidence.ChunkID < items[j].evidence.ChunkID
	})
	return nil
}

func selectEvidence(items []candidate, limit int) []Evidence {
	result := make([]Evidence, 0, limit)
	perDocument := make(map[string]int)
	bytesUsed := 0
	for _, item := range items {
		if len(result) == limit {
			break
		}
		if perDocument[item.evidence.DocumentID] >= maxChunksPerDocument ||
			bytesUsed+len(item.evidence.Content) > maxEvidenceBytes {
			continue
		}
		item.evidence.CitationID = fmt.Sprintf("C%d", len(result)+1)
		item.evidence.LexicalRank = item.lexicalRank
		item.evidence.DenseRank = item.denseRank
		item.evidence.FusionScore = item.fusionScore
		item.evidence.RerankerScore = item.rerankScore
		result = append(result, item.evidence)
		perDocument[item.evidence.DocumentID]++
		bytesUsed += len(item.evidence.Content)
	}
	return result
}

func selectEvaluationEvidence(items []candidate, limit int) []Evidence {
	if limit > len(items) {
		limit = len(items)
	}
	result := make([]Evidence, limit)
	for index := 0; index < limit; index++ {
		result[index] = items[index].evidence
		result[index].CitationID = fmt.Sprintf("C%d", index+1)
		result[index].LexicalRank = items[index].lexicalRank
		result[index].DenseRank = items[index].denseRank
		result[index].FusionScore = items[index].fusionScore
		result[index].RerankerScore = items[index].rerankScore
	}
	return result
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

func lexicalTerms(text string) []string {
	fields := strings.FieldsFunc(strings.ToLower(text), func(r rune) bool {
		return unicode.IsSpace(r) || unicode.IsPunct(r) || unicode.IsSymbol(r)
	})
	seen := make(map[string]struct{})
	terms := make([]string, 0, 64)
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

func lexicalTSQuery(terms []string) string {
	quoted := make([]string, 0, len(terms))
	for _, term := range terms {
		term = strings.ReplaceAll(term, "'", "''")
		quoted = append(quoted, "'"+term+"'")
	}
	return strings.Join(quoted, " | ")
}
