package tasks

import (
	"context"
	"fmt"
	"github.com/Silo-Server/silo-server/internal/bookrequests"
	"github.com/Silo-Server/silo-server/internal/taskmanager"
)

type BookRequestsTask struct{ service *bookrequests.Service }

func NewBookRequestsTask(s *bookrequests.Service) *BookRequestsTask {
	return &BookRequestsTask{service: s}
}
func (*BookRequestsTask) Key() string  { return "bloem_reconcile_book_requests" }
func (*BookRequestsTask) Name() string { return "Reconcile Audiobook Requests" }
func (*BookRequestsTask) Description() string {
	return "Match audiobook requests against organization-accessible audiobook libraries and deliver request notifications"
}
func (*BookRequestsTask) Category() taskmanager.TaskCategory { return taskmanager.TaskCategoryLibrary }
func (*BookRequestsTask) IsHidden() bool                     { return true }
func (*BookRequestsTask) DefaultTriggers() []taskmanager.TriggerConfig {
	return []taskmanager.TriggerConfig{{Type: taskmanager.TriggerTypeInterval, IntervalMs: 5 * 60 * 1000}}
}
func (t *BookRequestsTask) Execute(ctx context.Context, p taskmanager.ProgressReporter) error {
	p.Report(0, "Checking audiobook requests")
	n, err := t.service.Reconcile(ctx, 100)
	if err != nil {
		return err
	}
	p.Report(100, fmt.Sprintf("Completed %d audiobook requests", n))
	return nil
}
