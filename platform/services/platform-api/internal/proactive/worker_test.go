package proactive

import (
	"context"
	"errors"
	"testing"
	"time"

	"github.com/qsyy0921/openim/platform/services/platform-api/internal/agent"
)

type dispatcherStoreStub struct {
	suppressedReason string
	deferredReason   string
	enqueuedRunID    string
	retryFailure     string
	transitionErr    error
}

func (*dispatcherStoreStub) ClaimEvent(context.Context, time.Duration, int) (*Event, error) {
	return nil, nil
}
func (*dispatcherStoreStub) RecordRankMemory(context.Context, Event, []RankMemory) error { return nil }
func (*dispatcherStoreStub) SaveRank(context.Context, Event, RankResult) error           { return nil }
func (s *dispatcherStoreStub) Suppress(_ context.Context, _ Event, reason string) error {
	s.suppressedReason = reason
	return s.transitionErr
}
func (s *dispatcherStoreStub) Defer(_ context.Context, _ Event, _ time.Time, reason string) error {
	s.deferredReason = reason
	return s.transitionErr
}
func (s *dispatcherStoreStub) MarkEnqueued(_ context.Context, _ Event, runID string) error {
	s.enqueuedRunID = runID
	return s.transitionErr
}
func (s *dispatcherStoreStub) RetryEvent(_ context.Context, _ Event, failure string, _ int, _ time.Duration) error {
	s.retryFailure = failure
	return s.transitionErr
}
func (*dispatcherStoreStub) Reconcile(context.Context) error { return nil }

type rankerStub struct{ result RankResult }

func (s rankerStub) Rank(context.Context, RankRequest) (RankResult, error) { return s.result, nil }

type rankMemoryStub struct{}

func (rankMemoryStub) Search(context.Context, string, string, string, int) ([]RankMemory, error) {
	return nil, nil
}

type enqueuerStub struct{ runID string }

func (s enqueuerStub) EnqueueProactive(context.Context, agent.ProactiveRequest) (string, error) {
	return s.runID, nil
}

func testDispatchEvent() Event {
	return Event{
		ID: "event", TenantID: "tenant", MemberID: "member", AgentID: "agent",
		SourceChannel: "openim", TargetID: "user", Query: "agents", Title: "Paper",
		Summary: "Summary", URL: "https://arxiv.org/abs/1", Phase: "dispatching",
		LeaseToken: "lease", Score: 0.9,
		Preference: Preference{
			Enabled: true, Timezone: "Asia/Shanghai", QuietStart: 22 * time.Hour,
			QuietEnd: 8 * time.Hour, DailyBudget: 5, MinimumScore: 0.5,
		},
	}
}

func TestDispatcherSuppressesDisabledMember(t *testing.T) {
	store := &dispatcherStoreStub{}
	worker := NewDispatcher(store, rankerStub{}, rankMemoryStub{}, enqueuerStub{}, time.Second, time.Minute, 3)
	event := testDispatchEvent()
	event.Preference.Enabled = false

	if err := worker.process(context.Background(), event); err != nil {
		t.Fatalf("process disabled member: %v", err)
	}
	if store.suppressedReason != "member_disabled" {
		t.Fatalf("unexpected suppression reason %q", store.suppressedReason)
	}
}

func TestDispatcherPropagatesTransitionPersistenceFailure(t *testing.T) {
	want := errors.New("database unavailable")
	store := &dispatcherStoreStub{transitionErr: want}
	worker := NewDispatcher(store, rankerStub{}, rankMemoryStub{}, enqueuerStub{}, time.Second, time.Minute, 3)
	event := testDispatchEvent()
	event.Preference.Enabled = false

	if err := worker.process(context.Background(), event); !errors.Is(err, want) {
		t.Fatalf("process error = %v, want %v", err, want)
	}
}

func TestDispatcherDefersQuietHours(t *testing.T) {
	store := &dispatcherStoreStub{}
	worker := NewDispatcher(store, rankerStub{}, rankMemoryStub{}, enqueuerStub{}, time.Second, time.Minute, 3)
	worker.now = func() time.Time {
		location, err := time.LoadLocation("Asia/Shanghai")
		if err != nil {
			t.Fatal(err)
		}
		return time.Date(2026, 7, 18, 23, 0, 0, 0, location)
	}

	if err := worker.process(context.Background(), testDispatchEvent()); err != nil {
		t.Fatalf("process quiet hours: %v", err)
	}
	if store.deferredReason != "quiet_hours" {
		t.Fatalf("unexpected defer reason %q", store.deferredReason)
	}
}

func TestDispatcherPersistsEnqueuedRun(t *testing.T) {
	store := &dispatcherStoreStub{}
	worker := NewDispatcher(store, rankerStub{}, rankMemoryStub{}, enqueuerStub{runID: "run-1"}, time.Second, time.Minute, 3)
	worker.now = func() time.Time {
		location, err := time.LoadLocation("Asia/Shanghai")
		if err != nil {
			t.Fatal(err)
		}
		return time.Date(2026, 7, 18, 12, 0, 0, 0, location)
	}

	if err := worker.process(context.Background(), testDispatchEvent()); err != nil {
		t.Fatalf("process delivery: %v", err)
	}
	if store.enqueuedRunID != "run-1" {
		t.Fatalf("enqueued run = %q", store.enqueuedRunID)
	}
}
