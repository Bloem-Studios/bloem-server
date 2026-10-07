package translation

import (
	"context"
	"errors"
	"slices"
	"testing"

	"github.com/Silo-Server/silo-server/internal/ai/jobrunner"
	"github.com/Silo-Server/silo-server/internal/catalog"
	"github.com/jackc/pgx/v5"
)

type noSelectedQuery struct{ t *testing.T }

func (q noSelectedQuery) Query(context.Context, string, ...any) (pgx.Rows, error) {
	q.t.Fatal("classification/discovery before host denial")
	return nil, nil
}
func (q noSelectedQuery) QueryRow(context.Context, string, ...any) pgx.Row {
	q.t.Fatal("classification before host denial")
	return nil
}
func TestNativeSelectedJobWinnerAndCancellation(t *testing.T) {
	repo := newFakeRepo()
	svc := testService(t, repo, seriesContent(), &fakeLocs{}, &upperChat{})
	job := &Job{ContentID: "series1", TargetKind: TargetItem, TargetLanguage: "fr", IncludeChildren: true, Status: jobrunner.StatusPending, IdempotencyKey: idempotencyKey(TargetItem, "series1", "fr", "test-model")}
	if err := repo.InsertJob(t.Context(), job); err != nil {
		t.Fatal(err)
	}
	calls := 0
	ctx := catalog.WithNativePhaseAuthorizer(t.Context(), noSelectedQuery{t}, func(_ context.Context, _ catalog.NativePhaseQuery, targets catalog.NativePhaseTargets) error {
		calls++
		if !slices.Contains(targets.ContentIDs, "ep1") {
			t.Fatal("existing winner's IncludeChildren lost")
		}
		return &catalog.NativePhaseRefusal{Code: "not_found"}
	})
	got, err := svc.Enqueue(ctx, JobRequest{ContentID: "series1", TargetKind: TargetItem, TargetLanguage: "fr", IncludeChildren: false})
	if got != nil || !catalog.IsNativePhaseRefusal(err) {
		t.Fatalf("winner=%v err=%v", got, err)
	}
	err = svc.Cancel(ctx, job.ID)
	var refusal *catalog.NativePhaseRefusal
	if !errors.As(err, &refusal) || refusal.Code != "not_found" {
		t.Fatalf("cancel err=%v", err)
	}
	stored, _ := repo.GetJob(t.Context(), job.ID)
	if calls != 2 || stored.Status != jobrunner.StatusPending {
		t.Fatalf("calls=%d status=%s", calls, stored.Status)
	}
}
