package adminjob

// Fixtures Bloem's tests share with Silo tests that upstream has since
// removed. They are copied unchanged so the Silo files stay deleted.

import (
	"context"
	"testing"

	"github.com/Silo-Server/silo-server/internal/models"
)

func lifecycleJob(t *testing.T, r *Repository, kind string) *models.AdminJob {
	t.Helper()
	job, err := r.Create(t.Context(), CreateJobInput{JobType: kind, CreatedByUserID: bloemLifecycleActor(t, r), RequestPayload: LibraryRefreshRequest{LibraryID: 1}})
	if err != nil {
		t.Fatal(err)
	}
	t.Cleanup(func() { _, _ = r.pool.Exec(context.Background(), "DELETE FROM admin_jobs WHERE id=$1", job.ID) })
	return job
}
