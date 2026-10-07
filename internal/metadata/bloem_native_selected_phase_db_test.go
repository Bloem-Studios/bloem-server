//go:build integration

package metadata

import (
	"context"
	"errors"
	"slices"
	"testing"
	"time"

	"github.com/Silo-Server/silo-server/internal/catalog"
	"github.com/Silo-Server/silo-server/internal/catalog/filesplit"
	"github.com/Silo-Server/silo-server/internal/literaryworks"
	"github.com/Silo-Server/silo-server/internal/models"
	"github.com/Silo-Server/silo-server/internal/scanner"
	"github.com/google/uuid"
	"github.com/jackc/pgx/v5"
	"github.com/jackc/pgx/v5/pgxpool"
)

// These are producer/SQL tests with a deterministic host callback. They prove
// real deletion/link/move ordering, not API middleware, credentials or PINs.
func selectedPhaseContext(t *testing.T, pool *pgxpool.Pool, hidden string) context.Context {
	t.Helper()
	return catalog.WithNativePhaseAuthorizer(t.Context(), pool, func(_ context.Context, _ catalog.NativePhaseQuery, targets catalog.NativePhaseTargets) error {
		if hidden != "" && slices.Contains(targets.ContentIDs, hidden) {
			return &catalog.NativePhaseRefusal{Code: "not_found"}
		}
		return nil
	})
}
func selectedPhaseCode(t *testing.T, err error, code string) {
	t.Helper()
	var refusal *catalog.NativePhaseRefusal
	if !errors.As(err, &refusal) || refusal.Code != code {
		t.Fatalf("refusal=%v, want %s", err, code)
	}
}

// Rollback must release a fixture checkout even after Fatal or cancellation.
func selectedPhaseRollback(t *testing.T, tx pgx.Tx) {
	t.Helper()
	ctx, cancel := context.WithTimeout(context.Background(), 2*time.Second)
	defer cancel()
	if err := tx.Rollback(ctx); err != nil && !errors.Is(err, pgx.ErrTxClosed) {
		t.Error("selected phase fixture rollback failed")
	}
}
func selectedPhaseDrain(t *testing.T, cancel context.CancelFunc, finished <-chan struct{}) {
	t.Helper()
	cancel()
	timer := time.NewTimer(15 * time.Second)
	defer timer.Stop()
	select {
	case <-finished:
	case <-timer.C:
		t.Error("selected phase writer did not finish")
	}
}
func TestNativeSelectedProvisionalOwnerBeforeDeleteDB(t *testing.T) {
	pool, trace := writerCorrectionDatabase(t)
	native, _ := metadataNativeItem(t, pool)
	for _, tc := range []struct {
		name           string
		native, hidden bool
		code           string
	}{
		{"native", true, false, "native_local_operation_unsupported"},
		{"hidden native", true, true, "not_found"},
		{"hidden local", false, true, "not_found"},
		{"authorized local", false, false, ""},
	} {
		t.Run(tc.name, func(t *testing.T) {
			id := native
			if !tc.native {
				id = writerCorrectionLocal(t, pool)
			}
			metadataExec(t, pool, `UPDATE media_items SET status='pending' WHERE content_id=$1`, id)
			var kind string
			if err := pool.QueryRow(t.Context(), `SELECT type FROM media_items WHERE content_id=$1`, id).Scan(&kind); err != nil {
				t.Fatal(err)
			}
			metadataExec(t, pool, `INSERT INTO media_item_provider_ids(content_id,item_type,provider,provider_id) VALUES($1,$2,'isbn',$3) ON CONFLICT(content_id,provider) DO UPDATE SET provider_id=EXCLUDED.provider_id`, id, kind, uuid.NewString())
			hidden := ""
			if tc.hidden {
				hidden = id
			}
			trace.start()
			err := clearProvisionalProviderIDsLocked(selectedPhaseContext(t, pool, hidden), pool, id)
			events := trace.stop()
			var count int
			if err := pool.QueryRow(t.Context(), `SELECT count(*) FROM media_item_provider_ids WHERE content_id=$1`, id).Scan(&count); err != nil {
				t.Fatal(err)
			}
			if tc.code != "" {
				selectedPhaseCode(t, err, tc.code)
				writerCorrectionNoDML(t, events)
				if count != 1 {
					t.Fatal("provider owner modified on refusal")
				}
			} else if err != nil || count != 0 {
				t.Fatalf("local delete err=%v count=%d", err, count)
			}
		})
	}
}
func TestNativeSelectedFilesMoveAndStoredWinnerDB(t *testing.T) {
	pool, trace := writerCorrectionDatabase(t)
	source, target := writerCorrectionLocal(t, pool), writerCorrectionLocal(t, pool)
	fileID := writerCorrectionFile(t, pool, source)
	var path string
	var folder int
	if err := pool.QueryRow(t.Context(), `SELECT file_path,media_folder_id FROM media_files WHERE id=$1`, fileID).Scan(&path, &folder); err != nil {
		t.Fatal(err)
	}
	// The request's incoming tuple names target; the stored owner is hidden source.
	ctx := selectedPhaseContext(t, pool, source)
	trace.start()
	_, err := scanner.NewFileRepository(pool).Upsert(ctx, models.MediaFile{ContentID: target, FilePath: path, MediaFolderID: folder})
	events := trace.stop()
	selectedPhaseCode(t, err, "not_found")
	writerCorrectionNoDML(t, events)
	for _, hidden := range []string{target, ""} {
		ctx = selectedPhaseContext(t, pool, hidden)
		tx, err := pool.Begin(ctx)
		if err != nil {
			t.Fatal(err)
		}
		defer selectedPhaseRollback(t, tx)
		trace.start()
		_, moveErr := filesplit.Move(ctx, tx, filesplit.Options{FromContentID: source, ToContentID: target, ItemType: "movie", Files: []filesplit.File{{ID: fileID, ContentID: source, FilePath: path, MediaFolderID: folder}}})
		events = trace.stop()
		if hidden != "" {
			selectedPhaseCode(t, moveErr, "not_found")
			writerCorrectionNoDML(t, events)
			_ = tx.Rollback(ctx)
		} else {
			if moveErr != nil {
				_ = tx.Rollback(ctx)
				t.Fatal(moveErr)
			}
			if err := tx.Commit(ctx); err != nil {
				t.Fatal(err)
			}
		}
		var owner string
		if err := pool.QueryRow(ctx, `SELECT content_id FROM media_files WHERE id=$1`, fileID).Scan(&owner); err != nil {
			t.Fatal(err)
		}
		want := source
		if hidden == "" {
			want = target
		}
		if owner != want {
			t.Fatalf("file owner=%s want=%s", owner, want)
		}
	}
}
func TestNativeSelectedTitleLinkPreservesSavedTitleDB(t *testing.T) {
	pool, _ := writerCorrectionDatabase(t)
	source, target := "selected-source-"+uuid.NewString(), "selected-target-"+uuid.NewString()
	title := "Selected phase book " + uuid.NewString()
	metadataExec(t, pool, `INSERT INTO media_items(content_id,type,status,title) VALUES($1,'ebook','matched',$2)`, source, title)
	metadataExec(t, pool, `INSERT INTO media_items(content_id,type,status,title) VALUES($1,'audiobook','matched',$2)`, target, title)
	person := time.Now().UnixNano()
	metadataExec(t, pool, `INSERT INTO people(id,name) VALUES($1,$2)`, person, uuid.NewString())
	for _, id := range []string{source, target} {
		metadataExec(t, pool, `INSERT INTO item_people(id,content_id,person_id,kind) VALUES($1,$2,$3,7)`, time.Now().UnixNano(), id, person)
	}
	metadataExec(t, pool, `UPDATE media_items SET title=$2 WHERE content_id=$1`, source, "Old unlinked title")
	detail := catalog.NewDetailService(catalog.NewItemRepository(pool), nil, nil, nil, nil)
	detail.SetLiteraryWorkLinker(literaryworks.NewService(literaryworks.NewRepository(pool)))
	err := detail.UpdateMediaItemMetadata(selectedPhaseContext(t, pool, target), source, &catalog.MetadataUpdate{Title: &title})
	selectedPhaseCode(t, err, "not_found")
	var saved string
	var links int
	if err := pool.QueryRow(t.Context(), `SELECT title FROM media_items WHERE content_id=$1`, source).Scan(&saved); err != nil {
		t.Fatal(err)
	}
	if err := pool.QueryRow(t.Context(), `SELECT count(*) FROM literary_work_items WHERE content_id=ANY($1)`, []string{source, target}).Scan(&links); err != nil {
		t.Fatal(err)
	}
	if saved != title || links != 0 {
		t.Fatalf("saved title/link phases = %q/%d", saved, links)
	}
	if err := detail.UpdateMediaItemMetadata(selectedPhaseContext(t, pool, ""), source, &catalog.MetadataUpdate{Title: &title}); err != nil {
		t.Fatal(err)
	}
	if err := pool.QueryRow(t.Context(), `SELECT count(*) FROM literary_work_items WHERE content_id=ANY($1)`, []string{source, target}).Scan(&links); err != nil || links != 2 {
		t.Fatalf("local title link count=%d err=%v", links, err)
	}
}
