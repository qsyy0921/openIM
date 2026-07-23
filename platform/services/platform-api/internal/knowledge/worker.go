package knowledge

import (
	"context"
	"crypto/sha256"
	"encoding/hex"
	"errors"
	"fmt"
	"io"
	"math"
	"os"
	"path/filepath"
	"strings"
	"time"
)

type JobRepository interface {
	ClaimJob(context.Context, string, time.Duration) (Job, bool, error)
	RenewJobLease(context.Context, Job, time.Duration) error
	CompleteJob(context.Context, Job, []IndexedChunk) error
	FailJob(context.Context, Job, error) error
}

type CleanupRepository interface {
	ClaimObjectCleanup(context.Context, string, time.Duration) (ObjectCleanup, bool, error)
	CompleteObjectCleanup(context.Context, ObjectCleanup) error
	FailObjectCleanup(context.Context, ObjectCleanup, error) error
}

type WorkerRepository interface {
	JobRepository
	CleanupRepository
}

type WorkerConfig struct {
	Owner         string
	LeaseDuration time.Duration
	PollInterval  time.Duration
	BatchSize     int
	TempDirectory string
}

type Worker struct {
	store    WorkerRepository
	objects  ObjectStore
	parser   *Parser
	embedder EmbeddingProvider
	config   WorkerConfig
}

func NewWorker(store WorkerRepository, objects ObjectStore, parser *Parser, embedder EmbeddingProvider, config WorkerConfig) (*Worker, error) {
	config.Owner = strings.TrimSpace(config.Owner)
	config.TempDirectory = strings.TrimSpace(config.TempDirectory)
	if store == nil || objects == nil || parser == nil || embedder == nil ||
		config.Owner == "" || len(config.Owner) > 128 ||
		config.LeaseDuration < time.Minute || config.LeaseDuration > 30*time.Minute ||
		config.PollInterval < 100*time.Millisecond || config.PollInterval > time.Minute ||
		config.BatchSize < 1 || config.BatchSize > 128 || config.TempDirectory == "" {
		return nil, errors.New("knowledge ingestion worker configuration is invalid")
	}
	return &Worker{store: store, objects: objects, parser: parser, embedder: embedder, config: config}, nil
}

func (w *Worker) Run(ctx context.Context) error {
	if err := os.MkdirAll(w.config.TempDirectory, 0o700); err != nil {
		return fmt.Errorf("create knowledge ingestion temporary directory: %w", err)
	}
	for {
		cleanupFound, err := w.processCleanupOne(ctx)
		if err != nil {
			if ctx.Err() != nil {
				return nil
			}
			return err
		}
		job, found, err := w.store.ClaimJob(ctx, w.config.Owner, w.config.LeaseDuration)
		if err != nil {
			if ctx.Err() != nil {
				return nil
			}
			return err
		}
		if !found {
			if cleanupFound {
				continue
			}
			timer := time.NewTimer(w.config.PollInterval)
			select {
			case <-ctx.Done():
				timer.Stop()
				return nil
			case <-timer.C:
			}
			continue
		}
		if err := w.process(ctx, job); err != nil {
			if failErr := w.store.FailJob(ctx, job, err); failErr != nil {
				return errors.Join(err, failErr)
			}
		}
	}
}

func (w *Worker) ProcessOne(ctx context.Context) (bool, error) {
	cleanupFound, err := w.processCleanupOne(ctx)
	if err != nil || cleanupFound {
		return cleanupFound, err
	}
	job, found, err := w.store.ClaimJob(ctx, w.config.Owner, w.config.LeaseDuration)
	if err != nil || !found {
		return found, err
	}
	if err := w.process(ctx, job); err != nil {
		if failErr := w.store.FailJob(ctx, job, err); failErr != nil {
			return true, errors.Join(err, failErr)
		}
		return true, err
	}
	return true, nil
}

func (w *Worker) processCleanupOne(ctx context.Context) (bool, error) {
	cleanup, found, err := w.store.ClaimObjectCleanup(ctx, w.config.Owner, w.config.LeaseDuration)
	if err != nil || !found {
		return found, err
	}
	if err := w.objects.Delete(ctx, cleanup.Bucket, cleanup.ObjectKey); err != nil {
		if failErr := w.store.FailObjectCleanup(ctx, cleanup, err); failErr != nil {
			return true, errors.Join(err, failErr)
		}
		return true, nil
	}
	if err := w.store.CompleteObjectCleanup(ctx, cleanup); err != nil {
		return true, err
	}
	return true, nil
}

func (w *Worker) process(ctx context.Context, job Job) error {
	processCtx, cancel := context.WithCancel(ctx)
	renewed := make(chan error, 1)
	go func() {
		renewed <- w.renewLease(processCtx, job, cancel)
	}()
	indexed, err := w.buildIndex(processCtx, job)
	cancel()
	renewErr := <-renewed
	if renewErr != nil {
		return renewErr
	}
	if err != nil {
		return err
	}
	if err := w.store.RenewJobLease(ctx, job, w.config.LeaseDuration); err != nil {
		return fmt.Errorf("renew knowledge ingestion lease before commit: %w", err)
	}
	if err := w.store.CompleteJob(ctx, job, indexed); err != nil {
		return processingError("INDEX_COMMIT_FAILED", "knowledge projections could not be committed", true)
	}
	return nil
}

func (w *Worker) renewLease(ctx context.Context, job Job, cancel context.CancelFunc) error {
	interval := w.config.LeaseDuration / 3
	ticker := time.NewTicker(interval)
	defer ticker.Stop()
	for {
		select {
		case <-ctx.Done():
			return nil
		case <-ticker.C:
			if err := w.store.RenewJobLease(ctx, job, w.config.LeaseDuration); err != nil {
				cancel()
				return fmt.Errorf("renew knowledge ingestion lease: %w", err)
			}
		}
	}
}

func (w *Worker) buildIndex(ctx context.Context, job Job) ([]IndexedChunk, error) {
	directory, err := os.MkdirTemp(w.config.TempDirectory, "knowledge-"+job.ID+"-")
	if err != nil {
		return nil, processingError("TEMPORARY_STORAGE_FAILED", "temporary ingestion directory cannot be created", true)
	}
	defer os.RemoveAll(directory)
	sourcePath := filepath.Join(directory, "source")
	if err := w.objects.Download(ctx, job.Bucket, job.ObjectKey, sourcePath); err != nil {
		return nil, processingError("OBJECT_GET_FAILED", "source object download failed", true)
	}
	if err := verifySourceFile(sourcePath, job.SizeBytes, job.Checksum); err != nil {
		return nil, err
	}
	sections, err := w.parser.Parse(sourcePath, job.SourceFormat)
	if err != nil {
		return nil, err
	}
	chunks, err := ChunkSections(job.VersionID, sections)
	if err != nil {
		return nil, err
	}
	indexed := make([]IndexedChunk, len(chunks))
	for start := 0; start < len(chunks); start += w.config.BatchSize {
		end := min(start+w.config.BatchSize, len(chunks))
		texts := make([]string, end-start)
		for index := start; index < end; index++ {
			texts[index-start] = chunks[index].Content
		}
		batch, err := w.embedder.Embed(ctx, texts)
		if err != nil {
			return nil, processingError("EMBEDDING_DEPENDENCY_FAILED", "embedding request failed", true)
		}
		if batch.Model != job.EmbeddingRevision || batch.Dimension != job.EmbeddingDimension ||
			len(batch.Vectors) != len(texts) {
			return nil, processingError("EMBEDDING_CONTRACT_VIOLATION", "embedding response violates the locked model contract", false)
		}
		for offset, vector := range batch.Vectors {
			normalized, err := normalizeEmbedding(vector, job.EmbeddingDimension)
			if err != nil {
				return nil, err
			}
			indexed[start+offset] = IndexedChunk{Chunk: chunks[start+offset], Embedding: normalized}
		}
	}
	return indexed, nil
}

func verifySourceFile(path string, expectedSize int64, expectedChecksum string) error {
	file, err := os.Open(path)
	if err != nil {
		return processingError("SOURCE_READ_FAILED", "downloaded source cannot be opened", true)
	}
	defer file.Close()
	hash := sha256.New()
	size, err := io.Copy(hash, io.LimitReader(file, MaxSourceBytes+1))
	if err != nil {
		return processingError("SOURCE_READ_FAILED", "downloaded source cannot be read", true)
	}
	if size != expectedSize || size > MaxSourceBytes {
		return processingError("SOURCE_SIZE_MISMATCH", "downloaded source size does not match its immutable version", false)
	}
	actual := "sha256:" + hex.EncodeToString(hash.Sum(nil))
	if actual != expectedChecksum {
		return processingError("SOURCE_CHECKSUM_MISMATCH", "downloaded source checksum does not match its immutable version", false)
	}
	return nil
}

func normalizeEmbedding(vector []float32, dimension int) ([]float32, error) {
	if len(vector) != dimension {
		return nil, processingError("EMBEDDING_DIMENSION_INVALID", "embedding vector dimension is invalid", false)
	}
	var sum float64
	for _, value := range vector {
		if math.IsNaN(float64(value)) || math.IsInf(float64(value), 0) {
			return nil, processingError("EMBEDDING_VALUE_INVALID", "embedding vector contains a non-finite value", false)
		}
		sum += float64(value * value)
	}
	if sum == 0 {
		return nil, processingError("EMBEDDING_VALUE_INVALID", "embedding vector has zero norm", false)
	}
	norm := float32(math.Sqrt(sum))
	result := make([]float32, len(vector))
	for index, value := range vector {
		result[index] = value / norm
	}
	return result, nil
}

var _ JobRepository = (*Store)(nil)
var _ CleanupRepository = (*Store)(nil)
var _ WorkerRepository = (*Store)(nil)
