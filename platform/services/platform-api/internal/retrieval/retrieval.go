package retrieval

import (
	"context"
	"errors"
	"fmt"
	"strings"
	"unicode"
	"unicode/utf8"

	"github.com/jackc/pgx/v5/pgxpool"
)

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

type Store struct{ pool *pgxpool.Pool }

func NewStore(pool *pgxpool.Pool) *Store { return &Store{pool: pool} }

func (s *Store) Search(ctx context.Context, query Query) ([]Evidence, error) {
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
	const statement = `
SELECT d.id::text, v.id::text, c.id::text, d.title, d.source_uri, c.checksum, c.content,
       matches.score
FROM knowledge.documents AS d
JOIN knowledge.document_versions AS v
  ON v.document_id = d.id AND v.id = d.current_version_id
JOIN knowledge.chunks AS c
  ON c.document_id = d.id AND c.version_id = v.id AND c.tenant_id = d.tenant_id
JOIN authz.document_grants AS g
  ON g.tenant_id = d.tenant_id AND g.document_id = d.id
 AND g.member_id = $2::uuid AND g.permission = 'read'
JOIN LATERAL (
    SELECT count(*)::integer AS score
    FROM unnest($3::text[]) AS term
    WHERE strpos(lower(c.content), lower(term)) > 0
       OR strpos(lower(d.title), lower(term)) > 0
) AS matches ON matches.score > 0
WHERE d.tenant_id = $1::uuid
  AND d.status = 'active'
  AND v.status = 'published'
  AND d.classification IN ('public', 'internal')
ORDER BY matches.score DESC, d.id, c.ordinal
LIMIT $4`
	rows, err := s.pool.Query(ctx, statement, query.TenantID, query.MemberID, terms, query.Limit)
	if err != nil {
		return nil, fmt.Errorf("search authorized knowledge: %w", err)
	}
	defer rows.Close()
	var result []Evidence
	for rows.Next() {
		var item Evidence
		var score int
		if err := rows.Scan(&item.DocumentID, &item.VersionID, &item.ChunkID, &item.Title, &item.SourceURI, &item.Checksum, &item.Content, &score); err != nil {
			return nil, fmt.Errorf("scan authorized knowledge: %w", err)
		}
		item.CitationID = fmt.Sprintf("C%d", len(result)+1)
		result = append(result, item)
	}
	if err := rows.Err(); err != nil {
		return nil, fmt.Errorf("iterate authorized knowledge: %w", err)
	}
	return result, nil
}

func lexicalTerms(text string) []string {
	fields := strings.FieldsFunc(strings.ToLower(text), func(r rune) bool {
		return unicode.IsSpace(r) || unicode.IsPunct(r) || unicode.IsSymbol(r)
	})
	seen := make(map[string]struct{})
	terms := make([]string, 0, len(fields))
	for _, field := range fields {
		field = strings.TrimSpace(field)
		if utf8.RuneCountInString(field) < 2 {
			continue
		}
		if _, exists := seen[field]; exists {
			continue
		}
		seen[field] = struct{}{}
		terms = append(terms, field)
		if len(terms) == 16 {
			break
		}
	}
	return terms
}
