package knowledge

import (
	"context"
	"errors"
	"time"
)

const (
	MaxSourceBytes = 32 << 20
	MaxChunkRunes  = 1200
	MaxChunkBytes  = 8000
	ChunkOverlap   = 120
)

var (
	ErrForbidden           = errors.New("knowledge administration is forbidden")
	ErrInvalidInput        = errors.New("knowledge input is invalid")
	ErrIdempotencyConflict = errors.New("knowledge idempotency key conflicts with an earlier request")
	ErrNotFound            = errors.New("knowledge resource was not found")
	ErrConflict            = errors.New("knowledge state conflicts with this operation")
	ErrLeaseLost           = errors.New("knowledge ingestion lease is not held")
)

type SourceFormat string

const (
	FormatMarkdown SourceFormat = "markdown"
	FormatText     SourceFormat = "text"
	FormatPDF      SourceFormat = "pdf"
	FormatDOCX     SourceFormat = "docx"
)

type UploadFile struct {
	Path             string
	OriginalFilename string
	DeclaredType     string
	DetectedType     string
	Format           SourceFormat
	Size             int64
	Checksum         string
}

type UploadCommand struct {
	TenantID       string
	ActorMemberID  string
	DocumentID     string
	Title          string
	Classification string
	IdempotencyKey string
	File           UploadFile
}

type UploadResult struct {
	DocumentID     string `json:"document_id"`
	VersionID      string `json:"version_id"`
	VersionNumber  int    `json:"version_number"`
	IngestionState string `json:"ingestion_state"`
	Idempotent     bool   `json:"idempotent"`
}

type UploadReservation struct {
	UploadResult
	TenantID      string
	Bucket        string
	ObjectKey     string
	RequestDigest string
}

type DocumentSummary struct {
	ID                  string    `json:"id"`
	Title               string    `json:"title"`
	Classification      string    `json:"classification"`
	Status              string    `json:"status"`
	CurrentVersionID    string    `json:"current_version_id,omitempty"`
	CurrentVersion      int       `json:"current_version,omitempty"`
	CurrentIngestion    string    `json:"current_ingestion_state,omitempty"`
	LatestVersionID     string    `json:"latest_version_id,omitempty"`
	LatestVersion       int       `json:"latest_version,omitempty"`
	LatestIngestion     string    `json:"latest_ingestion_state,omitempty"`
	LatestFailureCode   string    `json:"latest_failure_code,omitempty"`
	LatestFailureDetail string    `json:"latest_failure_detail,omitempty"`
	GrantCount          int       `json:"grant_count"`
	CreatedAt           time.Time `json:"created_at"`
}

type VersionSummary struct {
	ID               string     `json:"id"`
	VersionNumber    int        `json:"version_number"`
	Checksum         string     `json:"checksum"`
	Status           string     `json:"status"`
	IngestionState   string     `json:"ingestion_state"`
	FailureCode      string     `json:"failure_code,omitempty"`
	FailureDetail    string     `json:"failure_detail,omitempty"`
	SourceFormat     string     `json:"source_format,omitempty"`
	OriginalFilename string     `json:"original_filename,omitempty"`
	SizeBytes        int64      `json:"size_bytes,omitempty"`
	JobState         string     `json:"job_state,omitempty"`
	Attempts         int        `json:"attempts,omitempty"`
	CreatedAt        time.Time  `json:"created_at"`
	PublishedAt      *time.Time `json:"published_at,omitempty"`
}

type MemberSummary struct {
	ID          string `json:"id"`
	DisplayName string `json:"display_name"`
	Status      string `json:"status"`
	Granted     bool   `json:"granted"`
}

type AdminSnapshot struct {
	Roles     []string          `json:"roles"`
	Documents []DocumentSummary `json:"documents"`
	Members   []MemberSummary   `json:"members"`
}

type Job struct {
	ID                 string
	TenantID           string
	DocumentID         string
	VersionID          string
	Bucket             string
	ObjectKey          string
	SourceFormat       SourceFormat
	SizeBytes          int64
	Checksum           string
	LeaseToken         string
	Attempts           int
	MaxAttempts        int
	ParserRevision     string
	EmbeddingRevision  string
	EmbeddingDimension int
}

type ObjectCleanup struct {
	TenantID    string
	DocumentID  string
	VersionID   string
	Bucket      string
	ObjectKey   string
	LeaseToken  string
	Attempts    int
	MaxAttempts int
}

type Section struct {
	Heading string
	Text    string
}

type Chunk struct {
	ID       string
	Ordinal  int
	Content  string
	Checksum string
	Lexemes  string
}

type IndexedChunk struct {
	Chunk
	Embedding []float32
}

type EmbeddingBatch struct {
	Model     string
	Dimension int
	Vectors   [][]float32
}

type EmbeddingProvider interface {
	Embed(ctx context.Context, texts []string) (EmbeddingBatch, error)
}
