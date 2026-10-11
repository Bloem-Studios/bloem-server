package tasks

import (
	"context"
	"errors"
	"fmt"

	"github.com/Silo-Server/silo-server/internal/bookrequests"
	"github.com/Silo-Server/silo-server/internal/taskmanager"
)

type bookRequestsReconciler interface {
	Reconcile(context.Context, int) (int, error)
	ReconcileEbooks(context.Context, int) (int, error)
}
type BookRequestsTask struct{ service bookRequestsReconciler }

func NewBookRequestsTask(s *bookrequests.Service) *BookRequestsTask {
	return &BookRequestsTask{service: s}
}
func (*BookRequestsTask) Key() string  { return "bloem_reconcile_book_requests" }
func (*BookRequestsTask) Name() string { return "Reconcile Book Requests" }
func (*BookRequestsTask) Description() string {
	return "Reconcile audiobook library matches and ebook fulfillment with request notifications"
}
func (*BookRequestsTask) Category() taskmanager.TaskCategory { return taskmanager.TaskCategoryLibrary }
func (*BookRequestsTask) IsHidden() bool                     { return true }
func (*BookRequestsTask) DefaultTriggers() []taskmanager.TriggerConfig {
	return []taskmanager.TriggerConfig{{Type: taskmanager.TriggerTypeInterval, IntervalMs: 5 * 60 * 1000}}
}
func (t *BookRequestsTask) Execute(ctx context.Context, p taskmanager.ProgressReporter) error {
	p.Report(0, "Checking book requests")
	n, audioErr := t.service.Reconcile(ctx, 100)
	ebooks, ebookErr := t.service.ReconcileEbooks(ctx, 100)
	if err := errors.Join(audioErr, ebookErr); err != nil {
		return err
	}
	p.Report(100, fmt.Sprintf("Completed %d audiobook and %d ebook requests", n, ebooks))
	return nil
}
