package delivery

import (
	"context"
	"errors"
	"log/slog"
	"time"
)

type FailureClass string

const (
	FailureRetryable FailureClass = "retryable"
	FailurePermanent FailureClass = "permanent"
	FailureUncertain FailureClass = "uncertain"
)

type SendError struct {
	Class FailureClass
	Err   error
}

func (e *SendError) Error() string { return e.Err.Error() }
func (e *SendError) Unwrap() error { return e.Err }

func Retryable(err error) error { return &SendError{Class: FailureRetryable, Err: err} }
func Permanent(err error) error { return &SendError{Class: FailurePermanent, Err: err} }
func Uncertain(err error) error { return &SendError{Class: FailureUncertain, Err: err} }

type RuntimeStore interface {
	Claim(context.Context, time.Duration, int) (*Record, error)
	MarkSent(context.Context, Record, string) error
	MarkRetry(context.Context, Record, string, int, time.Duration) error
	MarkPermanent(context.Context, Record, string) error
	MarkUncertain(context.Context, Record, string) error
}

type Sender interface {
	Send(context.Context, Record) (string, error)
}

type Worker struct {
	store       RuntimeStore
	sender      Sender
	poll        time.Duration
	lease       time.Duration
	maxAttempts int
}

func NewWorker(store RuntimeStore, sender Sender, poll, lease time.Duration, maxAttempts int) *Worker {
	return &Worker{store: store, sender: sender, poll: poll, lease: lease, maxAttempts: maxAttempts}
}

func (w *Worker) Run(ctx context.Context) error {
	ticker := time.NewTicker(w.poll)
	defer ticker.Stop()
	for {
		if err := w.runOnce(ctx); err != nil {
			return err
		}
		select {
		case <-ctx.Done():
			return nil
		case <-ticker.C:
		}
	}
}

func (w *Worker) runOnce(ctx context.Context) error {
	record, err := w.store.Claim(ctx, w.lease, w.maxAttempts)
	if err != nil || record == nil {
		return err
	}
	externalID, err := w.sender.Send(ctx, *record)
	if err == nil {
		if err := w.store.MarkSent(ctx, *record, externalID); err != nil {
			return err
		}
		slog.Info("Agent delivery completed", "run_id", record.RunID, "channel", record.Channel, "attempt", record.Attempts)
		return nil
	}
	if ctx.Err() != nil {
		return nil
	}
	class := FailureUncertain
	var sendErr *SendError
	if errors.As(err, &sendErr) {
		class = sendErr.Class
	}
	switch class {
	case FailureRetryable:
		delay := time.Second << min(record.Attempts-1, 5)
		if markErr := w.store.MarkRetry(ctx, *record, err.Error(), w.maxAttempts, delay); markErr != nil {
			return markErr
		}
	case FailurePermanent:
		if markErr := w.store.MarkPermanent(ctx, *record, err.Error()); markErr != nil {
			return markErr
		}
	default:
		if markErr := w.store.MarkUncertain(ctx, *record, err.Error()); markErr != nil {
			return markErr
		}
	}
	slog.Warn("Agent delivery failed", "run_id", record.RunID, "channel", record.Channel, "attempt", record.Attempts, "class", class, "error", err)
	return nil
}
