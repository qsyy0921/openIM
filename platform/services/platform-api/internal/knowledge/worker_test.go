package knowledge

import (
	"context"
	"errors"
	"testing"
	"time"
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
	deleteErr   error
	deleteCalls int
}

func (*workerObjectStoreStub) Put(context.Context, string, string, string, int64, string, string) error {
	return nil
}

func (*workerObjectStoreStub) Download(context.Context, string, string, string) error {
	return nil
}

func (s *workerObjectStoreStub) Delete(context.Context, string, string) error {
	s.deleteCalls++
	return s.deleteErr
}

type workerEmbeddingStub struct{}

func (workerEmbeddingStub) Embed(context.Context, []string) (EmbeddingBatch, error) {
	return EmbeddingBatch{}, nil
}
