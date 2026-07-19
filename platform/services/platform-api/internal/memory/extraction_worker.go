package memory

import (
	"context"
	"fmt"
	"time"
)

type ExtractionModel interface {
	Extract(context.Context, ExtractionRequest) (ExtractionResult, error)
}

type ExtractionJobStore interface {
	EnqueueDelivered(context.Context) (bool, error)
	Claim(context.Context, time.Duration, int) (*ExtractionJob, error)
	SaveResult(context.Context, ExtractionJob, ExtractionResult) error
	SaveGroupProposals(context.Context, ExtractionJob) error
	Complete(context.Context, ExtractionJob) error
	Retry(context.Context, ExtractionJob, string, int, time.Duration) error
}

type MemoryEventAppender interface {
	AppendUpsert(context.Context, Scope, string, string, string, FactPayload) (string, error)
}

type ExtractionWorker struct {
	jobs        ExtractionJobStore
	model       ExtractionModel
	events      MemoryEventAppender
	poll, lease time.Duration
	maxAttempts int
}

func NewExtractionWorker(jobs ExtractionJobStore, model ExtractionModel, events MemoryEventAppender, poll, lease time.Duration, maxAttempts int) *ExtractionWorker {
	return &ExtractionWorker{jobs: jobs, model: model, events: events, poll: poll, lease: lease, maxAttempts: maxAttempts}
}

func (w *ExtractionWorker) Run(ctx context.Context) error {
	ticker := time.NewTicker(w.poll)
	defer ticker.Stop()
	for {
		enqueued, err := w.jobs.EnqueueDelivered(ctx)
		if err != nil {
			return err
		}
		job, err := w.jobs.Claim(ctx, w.lease, w.maxAttempts)
		if err != nil {
			return err
		}
		if job != nil {
			w.process(ctx, *job)
			continue
		}
		if enqueued {
			continue
		}
		select {
		case <-ctx.Done():
			return nil
		case <-ticker.C:
		}
	}
}

func (w *ExtractionWorker) process(ctx context.Context, job ExtractionJob) {
	if job.Phase == "extracting" {
		result, err := w.model.Extract(ctx, ExtractionRequest{
			RunID: job.RunID, UserMessage: job.UserMessage, AssistantResponse: job.AssistantResponse,
		})
		if err == nil {
			err = w.jobs.SaveResult(ctx, job, result)
		}
		if err != nil {
			_ = w.jobs.Retry(ctx, job, err.Error(), w.maxAttempts, retryDelay(job.ModelAttempts))
		}
		return
	}
	if job.Phase != "projecting" {
		_ = w.jobs.Retry(ctx, job, "unsupported memory extraction phase", w.maxAttempts, retryDelay(job.ProjectionAttempts))
		return
	}
	if job.SessionType == 2 {
		if err := w.jobs.SaveGroupProposals(ctx, job); err != nil {
			_ = w.jobs.Retry(ctx, job, err.Error(), w.maxAttempts, retryDelay(job.ProjectionAttempts))
			return
		}
		if err := w.jobs.Complete(ctx, job); err != nil {
			_ = w.jobs.Retry(ctx, job, err.Error(), w.maxAttempts, retryDelay(job.ProjectionAttempts))
		}
		return
	}
	scope := Scope{Type: "personal", TenantID: job.TenantID, MemberID: job.MemberID}
	for _, fact := range job.ExtractedFacts {
		payload, err := NewFactPayload(fact.Category, fact.Content)
		if err == nil {
			idempotency := fmt.Sprintf("memory-extraction:%s:%s:%s", job.ID, fact.FactKey(), payload.Checksum)
			_, err = w.events.AppendUpsert(ctx, scope, fact.FactKey(), job.RunID, idempotency, payload)
		}
		if err != nil {
			_ = w.jobs.Retry(ctx, job, err.Error(), w.maxAttempts, retryDelay(job.ProjectionAttempts))
			return
		}
	}
	if err := w.jobs.Complete(ctx, job); err != nil {
		_ = w.jobs.Retry(ctx, job, err.Error(), w.maxAttempts, retryDelay(job.ProjectionAttempts))
	}
}

func retryDelay(attempt int) time.Duration {
	if attempt < 1 {
		attempt = 1
	}
	if attempt > 6 {
		attempt = 6
	}
	return time.Second << (attempt - 1)
}
