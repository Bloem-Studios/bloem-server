package adminpeople

import (
	"context"
	"errors"
	"sync"
	"testing"
	"time"

	"github.com/google/uuid"
)

type workerStoreStub struct {
	mu             sync.Mutex
	jobs           []DurableJob
	processed      []string
	cleanups       int
	jobCleanups    int
	processStarted chan struct{}
	releaseProcess chan struct{}
	processErr     error
	cancelProcess  func()
	failures       int
	failureStored  chan struct{}
}

func (s *workerStoreStub) ListRunnableBulkJobs(context.Context, int) ([]DurableJob, error) {
	s.mu.Lock()
	defer s.mu.Unlock()
	return append([]DurableJob(nil), s.jobs...), nil
}
func (s *workerStoreStub) ProcessBulkBatch(ctx context.Context, _ uuid.UUID, jobID string, _ int) (BulkResult, error) {
	s.mu.Lock()
	s.processed = append(s.processed, jobID)
	started := s.processStarted
	release := s.releaseProcess
	processErr := s.processErr
	cancelProcess := s.cancelProcess
	s.mu.Unlock()
	if started != nil {
		select {
		case started <- struct{}{}:
		default:
			{
			}
		}
	}
	if release != nil {
		select {
		case <-release:
		case <-ctx.Done():
			return BulkResult{}, ctx.Err()
		}
	}
	if cancelProcess != nil {
		cancelProcess()
	}
	if processErr != nil {
		return BulkResult{}, processErr
	}
	return BulkResult{JobID: jobID, Status: "completed"}, nil
}
func (s *workerStoreStub) CleanupExpiredSelections(context.Context, int) (int64, error) {
	s.mu.Lock()
	s.cleanups++
	s.mu.Unlock()
	return 0, nil
}
func (s *workerStoreStub) CleanupTerminalBulkJobs(context.Context, time.Time, int) (int64, error) {
	s.mu.Lock()
	s.jobCleanups++
	s.mu.Unlock()
	return 0, nil
}
func (s *workerStoreStub) FailBulkJob(context.Context, uuid.UUID, string, error) error {
	s.mu.Lock()
	s.failures++
	stored := s.failureStored
	s.mu.Unlock()
	if stored != nil {
		select {
		case stored <- struct{}{}:
		default:
		}
	}
	return nil
}

func TestWorkerWakeProcessesQueuedJobWithoutPerRequestGoroutine(t *testing.T) {
	store := &workerStoreStub{jobs: []DurableJob{{JobID: "job-1", OrganizationID: uuid.New()}}}
	worker := NewWorker(store, WorkerOptions{RecoveryInterval: time.Hour, CleanupInterval: time.Hour})
	ctx, cancel := context.WithCancel(context.Background())
	done := make(chan struct{})
	go func() { worker.Run(ctx); close(done) }()
	worker.Wake()
	eventually(t, func() bool { store.mu.Lock(); defer store.mu.Unlock(); return len(store.processed) >= 1 })
	cancel()
	select {
	case <-done:
	case <-time.After(time.Second):
		t.Fatal("worker did not stop")
	}
}

func TestWorkerPeriodicRecoveryProcessesJobsAndCleansExpiredSelections(t *testing.T) {
	store := &workerStoreStub{jobs: []DurableJob{{JobID: "recovered", OrganizationID: uuid.New()}}}
	worker := NewWorker(store, WorkerOptions{RecoveryInterval: 10 * time.Millisecond, CleanupInterval: 10 * time.Millisecond})
	ctx, cancel := context.WithCancel(context.Background())
	done := make(chan struct{})
	go func() { worker.Run(ctx); close(done) }()
	eventually(t, func() bool {
		store.mu.Lock()
		defer store.mu.Unlock()
		return len(store.processed) > 0 && store.cleanups > 0 && store.jobCleanups > 0
	})
	cancel()
	<-done
}

func TestWorkerShutdownCancelsActiveBatch(t *testing.T) {
	started := make(chan struct{}, 1)
	store := &workerStoreStub{jobs: []DurableJob{{JobID: "active", OrganizationID: uuid.New()}}, processStarted: started, releaseProcess: make(chan struct{})}
	worker := NewWorker(store, WorkerOptions{RecoveryInterval: time.Hour, CleanupInterval: time.Hour})
	ctx, cancel := context.WithCancel(context.Background())
	done := make(chan struct{})
	go func() { worker.Run(ctx); close(done) }()
	worker.Wake()
	select {
	case <-started:
	case <-time.After(time.Second):
		t.Fatal("batch did not start")
	}
	cancel()
	select {
	case <-done:
	case <-time.After(time.Second):
		t.Fatal("worker did not cancel active batch")
	}
}

func TestWorkerDurablySchedulesNonContextFailureDuringConcurrentShutdown(t *testing.T) {
	ctx, cancel := context.WithCancel(context.Background())
	stored := make(chan struct{}, 1)
	store := &workerStoreStub{
		jobs:          []DurableJob{{JobID: "retry", OrganizationID: uuid.New()}},
		processErr:    errors.New("database unavailable"),
		cancelProcess: cancel,
		failureStored: stored,
	}
	worker := NewWorker(store, WorkerOptions{RecoveryInterval: time.Hour, CleanupInterval: time.Hour})
	done := make(chan struct{})
	go func() { worker.Run(ctx); close(done) }()
	select {
	case <-stored:
	case <-time.After(time.Second):
		t.Fatal("non-context failure was not durably scheduled")
	}
	<-done
	store.mu.Lock()
	defer store.mu.Unlock()
	if store.failures != 1 || len(store.processed) != 1 {
		t.Fatalf("failures=%d processed=%v", store.failures, store.processed)
	}
}

func eventually(t *testing.T, condition func() bool) {
	t.Helper()
	deadline := time.Now().Add(time.Second)
	for time.Now().Before(deadline) {
		if condition() {
			return
		}
		time.Sleep(time.Millisecond)
	}
	t.Fatal("condition not reached")
}
