package knowledge

import (
	"context"
	"errors"
	"os"
	"strings"
	"testing"
	"time"

	"github.com/qsyy0921/openim/platform/services/platform-api/internal/knowledgeprojection"
)

func TestWorkerProcessesObjectCleanupBeforeIngestion(t *testing.T) {
	cleanup := ObjectCleanup{
		TenantID: "tenant", DocumentID: "document", VersionID: "version",
		Bucket: "knowledge", ObjectKey: "tenant/document/version/source",
		LeaseToken: "lease", Attempts: 1, MaxAttempts: 3,
	}
	repository := &workerRepositoryStub{cleanup: cleanup, cleanupFound: true}
	objects := &workerObjectStoreStub{}
	worker := newCleanupTestWorker(t, repository, objects)

	found, err := worker.ProcessOne(context.Background())
	if err != nil || !found {
		t.Fatalf("ProcessOne() = %v, %v", found, err)
	}
	if objects.deleteCalls != 1 || repository.completeCleanupCalls != 1 ||
		repository.failCleanupCalls != 0 || repository.claimJobCalls != 0 {
		t.Fatalf("cleanup path calls: delete=%d complete=%d fail=%d job=%d",
			objects.deleteCalls, repository.completeCleanupCalls,
			repository.failCleanupCalls, repository.claimJobCalls)
	}
}

func TestWorkerRecordsCleanupFailureWithoutFalseCompletion(t *testing.T) {
	deleteErr := errors.New("object store unavailable")
	cleanup := ObjectCleanup{
		TenantID: "tenant", DocumentID: "document", VersionID: "version",
		Bucket: "knowledge", ObjectKey: "tenant/document/version/source",
		LeaseToken: "lease", Attempts: 1, MaxAttempts: 3,
	}
	repository := &workerRepositoryStub{cleanup: cleanup, cleanupFound: true}
	objects := &workerObjectStoreStub{deleteErr: deleteErr}
	worker := newCleanupTestWorker(t, repository, objects)

	found, err := worker.ProcessOne(context.Background())
	if err != nil || !found {
		t.Fatalf("durably recorded cleanup retry = %v, %v", found, err)
	}
	if repository.completeCleanupCalls != 0 || repository.failCleanupCalls != 1 {
		t.Fatalf("cleanup failure calls: complete=%d fail=%d",
			repository.completeCleanupCalls, repository.failCleanupCalls)
	}
	if !errors.Is(repository.cleanupCause, deleteErr) {
		t.Fatalf("cleanup cause = %v, want delete error", repository.cleanupCause)
	}

	repository = &workerRepositoryStub{
		cleanup: cleanup, cleanupFound: true, failCleanupErr: ErrLeaseLost,
	}
	worker = newCleanupTestWorker(t, repository, objects)
	found, err = worker.ProcessOne(context.Background())
	if !found || !errors.Is(err, deleteErr) || !errors.Is(err, ErrLeaseLost) {
		t.Fatalf("unrecorded cleanup failure = %v, %v", found, err)
	}
}

func TestWorkerEmbedsTheVersionedTitleContentProjection(t *testing.T) {
	content := []byte("The approval retention period is seven years.")
	objects := &workerObjectStoreStub{downloadContent: content}
	embedder := &recordingWorkerEmbedder{}
	worker, err := NewWorker(
		&workerRepositoryStub{},
		objects,
		NewParser(),
		embedder,
		WorkerConfig{
			Owner: "worker-test", LeaseDuration: time.Minute,
			PollInterval: 100 * time.Millisecond, BatchSize: 8,
			TempDirectory: t.TempDir(),
		},
	)
	if err != nil {
		t.Fatal(err)
	}
	job := Job{
		ID: "job-1", TenantID: "tenant-1", DocumentID: "document-1",
		DocumentTitle: "Third-party approval policy",
		VersionID:     "aaaaaaaa-aaaa-4aaa-8aaa-aaaaaaaaaaaa",
		Bucket:        "knowledge", ObjectKey: "tenant/document/version/source",
		SourceFormat: FormatText, SizeBytes: int64(len(content)),
		Checksum:          checksumText(string(content)),
		EmbeddingRevision: "qwen3-embedding:4b", EmbeddingDimension: 2560,
		ProjectionRevision: knowledgeprojection.Revision,
	}
	indexed, err := worker.buildIndex(context.Background(), job)
	if err != nil {
		t.Fatal(err)
	}
	if len(indexed) != 1 || len(embedder.texts) != 1 {
		t.Fatalf("indexed=%d embedding inputs=%d", len(indexed), len(embedder.texts))
	}
	if !strings.HasPrefix(
		embedder.texts[0],
		"Third-party approval policy\n\n",
	) {
		t.Fatalf("embedding input omitted document identity: %q", embedder.texts[0])
	}
	if strings.TrimPrefix(
		embedder.texts[0],
		"Third-party approval policy\n\n",
	) != string(content) {
		t.Fatalf("embedding input changed Chunk content: %q", embedder.texts[0])
	}
}

func newCleanupTestWorker(t *testing.T, repository WorkerRepository, objects ObjectStore) *Worker {
	t.Helper()
	worker, err := NewWorker(repository, objects, NewParser(), workerEmbeddingStub{}, WorkerConfig{
		Owner: "worker-test", LeaseDuration: time.Minute, PollInterval: 100 * time.Millisecond,
		BatchSize: 1, TempDirectory: t.TempDir(),
	})
	if err != nil {
		t.Fatal(err)
	}
	return worker
}

type workerRepositoryStub struct {
	cleanup              ObjectCleanup
	cleanupFound         bool
	claimCleanupErr      error
	completeCleanupErr   error
	failCleanupErr       error
	cleanupCause         error
	completeCleanupCalls int
	failCleanupCalls     int
	claimJobCalls        int
}

func (s *workerRepositoryStub) ClaimObjectCleanup(context.Context, string, time.Duration) (ObjectCleanup, bool, error) {
	return s.cleanup, s.cleanupFound, s.claimCleanupErr
}

func (s *workerRepositoryStub) CompleteObjectCleanup(context.Context, ObjectCleanup) error {
	s.completeCleanupCalls++
	return s.completeCleanupErr
}

func (s *workerRepositoryStub) FailObjectCleanup(_ context.Context, _ ObjectCleanup, cause error) error {
	s.failCleanupCalls++
	s.cleanupCause = cause
	return s.failCleanupErr
}

func (s *workerRepositoryStub) ClaimJob(context.Context, string, time.Duration) (Job, bool, error) {
	s.claimJobCalls++
	return Job{}, false, nil
}

func (*workerRepositoryStub) RenewJobLease(context.Context, Job, time.Duration) error {
	return nil
}

func (*workerRepositoryStub) CompleteJob(context.Context, Job, []IndexedChunk) error {
	return nil
}

func (*workerRepositoryStub) FailJob(context.Context, Job, error) error {
	return nil
}

type workerObjectStoreStub struct {
	deleteErr       error
	deleteCalls     int
	downloadContent []byte
}

func (*workerObjectStoreStub) Put(context.Context, string, string, string, int64, string, string) error {
	return nil
}

func (s *workerObjectStoreStub) Download(_ context.Context, _, _, destination string) error {
	if s.downloadContent == nil {
		return nil
	}
	return os.WriteFile(destination, s.downloadContent, 0o600)
}

func (s *workerObjectStoreStub) Delete(context.Context, string, string) error {
	s.deleteCalls++
	return s.deleteErr
}

type workerEmbeddingStub struct{}

func (workerEmbeddingStub) Embed(context.Context, []string) (EmbeddingBatch, error) {
	return EmbeddingBatch{}, nil
}

type recordingWorkerEmbedder struct {
	texts []string
}

func (e *recordingWorkerEmbedder) Embed(_ context.Context, texts []string) (EmbeddingBatch, error) {
	e.texts = append(e.texts, texts...)
	vectors := make([][]float32, len(texts))
	for index := range vectors {
		vectors[index] = make([]float32, 2560)
		vectors[index][0] = 1
	}
	return EmbeddingBatch{
		Model: "qwen3-embedding:4b", Dimension: 2560, Vectors: vectors,
	}, nil
}
