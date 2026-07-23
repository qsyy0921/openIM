package retrieval

import (
	"context"
	"crypto/sha256"
	"errors"
	"fmt"
	"regexp"
	"strings"
	"unicode/utf8"

	"github.com/jackc/pgx/v5"
)

const (
	maxCandidateAnswerBytes   = 16 << 10
	minCitationSupportScore   = 0
	maxValidatedCitationCount = maxEvidenceItems
)

// ValidateCitations reauthorizes evidence against the current database snapshot
// before performing the model-backed support check outside the transaction.
func (s *Store) ValidateCitations(ctx context.Context, query Query, answer string, cited []Evidence) ([]Evidence, error) {
	answer = strings.TrimSpace(answer)
	if query.TenantID == "" || query.MemberID == "" || query.Purpose != "agent_answer" {
		return nil, errors.New("citation validation identity or purpose is invalid")
	}
	if answer == "" || len(answer) > maxCandidateAnswerBytes || !utf8.ValidString(answer) {
		return nil, errors.New("citation validation answer exceeds the bounded UTF-8 contract")
	}
	if len(cited) < 1 || len(cited) > maxValidatedCitationCount {
		return nil, errors.New("citation validation requires a bounded evidence set")
	}
	seen := make(map[string]struct{}, len(cited))
	for _, item := range cited {
		if item.CitationID == "" || item.DocumentID == "" || item.VersionID == "" ||
			item.ChunkID == "" || item.Checksum == "" {
			return nil, errors.New("citation validation evidence is incomplete")
		}
		if _, duplicate := seen[item.CitationID]; duplicate {
			return nil, errors.New("citation validation evidence contains duplicate IDs")
		}
		seen[item.CitationID] = struct{}{}
	}

	tx, err := s.pool.BeginTx(ctx, pgx.TxOptions{AccessMode: pgx.ReadOnly})
	if err != nil {
		return nil, fmt.Errorf("begin citation authorization snapshot: %w", err)
	}
	defer func() { _ = tx.Rollback(ctx) }()
	refreshed := make([]Evidence, len(cited))
	for index, item := range cited {
		refreshed[index].CitationID = item.CitationID
		err := tx.QueryRow(ctx, authorizedCitationSQL,
			query.TenantID, query.MemberID, item.ChunkID, item.DocumentID,
			item.VersionID, s.config.ModelRevision, s.config.Dimension,
		).Scan(
			&refreshed[index].DocumentID, &refreshed[index].VersionID,
			&refreshed[index].ChunkID, &refreshed[index].Title,
			&refreshed[index].SourceURI, &refreshed[index].Checksum,
			&refreshed[index].Content, &refreshed[index].IndexRevision,
		)
		if errors.Is(err, pgx.ErrNoRows) {
			return nil, errors.New("citation is no longer authorized or current")
		}
		if err != nil {
			return nil, fmt.Errorf("reauthorize citation: %w", err)
		}
		if refreshed[index].Checksum != item.Checksum ||
			refreshed[index].DocumentID != item.DocumentID ||
			refreshed[index].VersionID != item.VersionID {
			return nil, errors.New("citation metadata changed before persistence")
		}
		checksum := sha256.Sum256([]byte(refreshed[index].Content))
		if refreshed[index].Checksum != fmt.Sprintf("sha256:%x", checksum[:]) {
			return nil, errors.New("citation content checksum validation failed")
		}
	}
	if err := tx.Commit(ctx); err != nil {
		return nil, fmt.Errorf("commit citation authorization snapshot: %w", err)
	}

	supportQuery := strings.TrimSpace(citationTokenPattern.ReplaceAllString(answer, " "))
	if supportQuery == "" {
		return nil, errors.New("citation answer contains no verifiable claim")
	}
	input := make([]RerankCandidate, len(refreshed))
	for index, item := range refreshed {
		if !hasCitationLexicalAnchor(supportQuery, item.Content) {
			return nil, errors.New("candidate contains a citation without a deterministic lexical anchor")
		}
		input[index] = RerankCandidate{CandidateID: item.ChunkID, Content: item.Content}
	}
	response, err := s.reranker.Rerank(ctx, supportQuery, input)
	if err != nil {
		return nil, fmt.Errorf("validate citation support: %w", err)
	}
	if response.Model != s.config.RerankerModel ||
		response.Revision != s.config.RerankerRevision ||
		len(response.Scores) != len(refreshed) {
		return nil, errors.New("citation support response violates the locked reranker contract")
	}
	scores := make(map[string]float64, len(response.Scores))
	for _, score := range response.Scores {
		if _, duplicate := scores[score.CandidateID]; duplicate {
			return nil, errors.New("citation support response contains duplicate candidates")
		}
		scores[score.CandidateID] = score.Score
	}
	for _, item := range refreshed {
		score, ok := scores[item.ChunkID]
		if !ok || score < minCitationSupportScore {
			return nil, errors.New("candidate contains a citation that does not support the answer")
		}
	}
	return refreshed, nil
}

func hasCitationLexicalAnchor(answer, content string) bool {
	content = strings.ToLower(content)
	for _, term := range lexicalTerms(answer) {
		if _, ignored := citationAnchorStopwords[term]; ignored {
			continue
		}
		if strings.Contains(content, term) {
			return true
		}
	}
	return false
}

const authorizedCitationSQL = `
SELECT document.id::text, version.id::text, chunk.id::text,
       document.title, document.source_uri, chunk.checksum, chunk.content,
       generation.model_revision
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
 AND document_grant.member_id = member.id
 AND document_grant.permission = 'read'
JOIN knowledge.chunks AS chunk
  ON chunk.tenant_id = version.tenant_id
 AND chunk.document_id = version.document_id
 AND chunk.version_id = version.id
JOIN knowledge.index_generations AS generation
  ON generation.tenant_id = document.tenant_id
 AND generation.state = 'active'
 AND generation.model_revision = $6
 AND generation.dimension = $7
JOIN knowledge.chunk_search_indexes AS search
  ON search.tenant_id = chunk.tenant_id
 AND search.generation_id = generation.id
 AND search.chunk_id = chunk.id
 AND search.model_revision = generation.model_revision
 AND search.dimension = generation.dimension
 AND search.content_checksum = chunk.checksum
WHERE document.tenant_id = $1::uuid
  AND chunk.id = $3::uuid
  AND document.id = $4::uuid
  AND version.id = $5::uuid
  AND document.status = 'active'
  AND version.status = 'published'
  AND version.ingestion_state IN ('legacy_indexed', 'indexed')
  AND document.classification IN ('public', 'internal')`

var citationTokenPattern = regexp.MustCompile(`\[C[1-8]\]`)

var citationAnchorStopwords = map[string]struct{}{
	"and": {}, "are": {}, "as": {}, "at": {}, "be": {}, "but": {}, "by": {},
	"for": {}, "from": {}, "has": {}, "have": {}, "in": {}, "into": {}, "is": {},
	"its": {}, "not": {}, "of": {}, "on": {}, "or": {}, "that": {}, "the": {},
	"their": {}, "this": {}, "to": {}, "was": {}, "were": {}, "will": {}, "with": {},
}
