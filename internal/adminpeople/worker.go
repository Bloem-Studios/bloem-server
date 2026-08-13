package adminpeople

import (
	"context"
	"time"

	"github.com/google/uuid"
)

type WorkerStore interface {
	ListRunnableBulkJobs(context.Context, int) ([]DurableJob, error)
	ProcessBulkBatch(context.Context, uuid.UUID, string, int) (BulkResult, error)
	FailBulkJob(context.Context, uuid.UUID, string, error) error
	CleanupTerminalBulkJobs(context.Context, time.Time, int) (int64, error)
	CleanupExpiredSelections(context.Context, int) (int64, error)
}

type WorkerOptions struct {
	RecoveryInterval time.Duration
	CleanupInterval  time.Duration
	BatchSize        int
	ScanLimit        int
	CleanupLimit     int
	JobRetention     time.Duration
}

type Worker struct {
	store   WorkerStore
	wake    chan struct{}
	options WorkerOptions
}

func NewWorker(store WorkerStore, options WorkerOptions) *Worker {
	if options.RecoveryInterval <= 0 {
		options.RecoveryInterval = 30 * time.Second
	}
	if options.CleanupInterval <= 0 {
		options.CleanupInterval = time.Hour
	}
	if options.BatchSize <= 0 {
		options.BatchSize = defaultBulkBatchSize
	}
	if options.ScanLimit <= 0 {
		options.ScanLimit = 100
	}
	if options.CleanupLimit <= 0 {
		options.CleanupLimit = 100
	}
	if options.JobRetention <= 0 {
		options.JobRetention = 24 * time.Hour
	}
	return &Worker{store: store, wake: make(chan struct{}, 1), options: options}
}

func (w *Worker) Wake() {
	if w == nil {
		return
	}
	select {
	case w.wake <- struct{}{}:
	default:
	}
}

func (w *Worker) Run(ctx context.Context) {
	if w == nil || w.store == nil {
		return
	}
	recovery := time.NewTicker(w.options.RecoveryInterval)
	cleanup := time.NewTicker(w.options.CleanupInterval)
	defer recovery.Stop()
	defer cleanup.Stop()
	w.process(ctx)
	for {
		select {
		case <-ctx.Done():
			return
		case <-w.wake:
			w.process(ctx)
		case <-recovery.C:
			w.process(ctx)
		case <-cleanup.C:
			_, _ = w.store.CleanupTerminalBulkJobs(ctx, time.Now().UTC().Add(-w.options.JobRetention), w.options.CleanupLimit)
			_, _ = w.store.CleanupExpiredSelections(ctx, w.options.CleanupLimit)
		}
	}
}

func (w *Worker) process(ctx context.Context) {
	jobs, err := w.store.ListRunnableBulkJobs(ctx, w.options.ScanLimit)
	if err != nil {
		return
	}
	more := false
	for _, job := range jobs {
		if ctx.Err() != nil {
			return
		}
		result, err := w.store.ProcessBulkBatch(ctx, job.OrganizationID, job.JobID, w.options.BatchSize)
		if err != nil && ctx.Err() == nil {
			_ = w.store.FailBulkJob(ctx, job.OrganizationID, job.JobID, err)
		}
		if err == nil && (result.Status == "queued" || result.Status == "running") {
			more = true
		}
	}
	if more {
		w.Wake()
	}
}
