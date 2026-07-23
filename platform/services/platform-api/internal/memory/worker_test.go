package memory

import (
	"context"
	"errors"
	"testing"
	"time"
)

type projectorStub struct {
	results []bool
	err     error
	calls   int
}

func (s *projectorStub) ProjectNext(context.Context) (bool, error) {
	s.calls++
	if s.err != nil {
		return false, s.err
	}
	if len(s.results) == 0 {
		return false, nil
	}
	result := s.results[0]
	s.results = s.results[1:]
	return result, nil
}

func TestWorkerDrainsReadyProjectionBeforePolling(t *testing.T) {
	projector := &projectorStub{results: []bool{true, true, false}}
	ctx, cancel := context.WithCancel(context.Background())
	worker := NewWorker(projector, time.Hour)
	done := make(chan error, 1)
	go func() { done <- worker.Run(ctx) }()
	deadline := time.After(time.Second)
	for projector.calls < 3 {
		select {
		case <-deadline:
			t.Fatal("worker did not drain ready projections")
		default:
			time.Sleep(time.Millisecond)
		}
	}
	cancel()
	if err := <-done; err != nil {
		t.Fatal(err)
	}
}

func TestWorkerFailsClosedOnProjectionError(t *testing.T) {
	want := errors.New("projection failed")
	err := NewWorker(&projectorStub{err: want}, time.Millisecond).Run(context.Background())
	if !errors.Is(err, want) {
		t.Fatalf("Run() error=%v", err)
	}
}
