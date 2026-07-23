package delivery

import (
	"context"
	"errors"
	"testing"
	"time"
)

type runtimeStoreStub struct {
	record    *Record
	marked    string
	external  string
	retryMax  int
	retryWait time.Duration
}

func (s *runtimeStoreStub) Claim(context.Context, time.Duration, int) (*Record, error) {
	return s.record, nil
}
func (s *runtimeStoreStub) MarkSent(_ context.Context, _ Record, external string) error {
	s.marked, s.external = "sent", external
	return nil
}
func (s *runtimeStoreStub) MarkRetry(_ context.Context, _ Record, _ string, max int, wait time.Duration) error {
	s.marked, s.retryMax, s.retryWait = "retry", max, wait
	return nil
}
func (s *runtimeStoreStub) MarkPermanent(context.Context, Record, string) error {
	s.marked = "permanent"
	return nil
}
func (s *runtimeStoreStub) MarkUncertain(context.Context, Record, string) error {
	s.marked = "uncertain"
	return nil
}

type senderStub struct {
	external string
	err      error
}

func (s senderStub) Send(context.Context, Record) (string, error) { return s.external, s.err }

func TestWorkerPersistsDeliveryOutcomes(t *testing.T) {
	tests := []struct {
		name, want string
		err        error
	}{
		{name: "sent", want: "sent"},
		{name: "retryable", want: "retry", err: Retryable(errors.New("busy"))},
		{name: "permanent", want: "permanent", err: Permanent(errors.New("rejected"))},
		{name: "uncertain", want: "uncertain", err: errors.New("connection reset")},
	}
	for _, test := range tests {
		t.Run(test.name, func(t *testing.T) {
			store := &runtimeStoreStub{record: &Record{ID: "delivery-1", RunID: "run-1", Attempts: 1}}
			worker := NewWorker(store, senderStub{external: "message-1", err: test.err}, time.Second, time.Second, 4)
			if err := worker.runOnce(context.Background()); err != nil {
				t.Fatal(err)
			}
			if store.marked != test.want {
				t.Fatalf("marked = %q, want %q", store.marked, test.want)
			}
			if test.want == "sent" && store.external != "message-1" {
				t.Fatalf("external ID = %q", store.external)
			}
			if test.want == "retry" && (store.retryMax != 4 || store.retryWait != time.Second) {
				t.Fatalf("retry max=%d wait=%s", store.retryMax, store.retryWait)
			}
		})
	}
}

func TestUnknownSendErrorFailsClosedWithoutRetry(t *testing.T) {
	store := &runtimeStoreStub{record: &Record{ID: "delivery-1", RunID: "run-1", Attempts: 1}}
	worker := NewWorker(store, senderStub{err: Uncertain(errors.New("response lost"))}, time.Second, time.Second, 4)
	if err := worker.runOnce(context.Background()); err != nil {
		t.Fatal(err)
	}
	if store.marked != "uncertain" {
		t.Fatalf("marked = %q", store.marked)
	}
}
