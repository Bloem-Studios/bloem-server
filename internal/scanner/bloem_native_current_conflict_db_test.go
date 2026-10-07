//go:build integration

package scanner

import (
	"context"
	"github.com/Silo-Server/silo-server/internal/models"
	"github.com/jackc/pgx/v5"
	"testing"
	"time"
)

func currentLocalTupleFixture(t *testing.T) (*nativeIngestFixture, int, string, string, string, string) {
	t.Helper()
	x := nativeIngestSetup(t) // Actual unrelated marked/current B library.
	var folder int
	if err := x.pool.QueryRow(t.Context(), "INSERT INTO media_folders(type,name,owner_id) VALUES('movies','Local tuple',bloem_platform_resource_owner_id()) RETURNING id").Scan(&folder); err != nil {
		t.Fatal(err)
	}
	a, b, episode, extra := "tuple-A", "tuple-B", "tuple-E", "tuple-X"
	nativeIngestSQL(t, x.pool, "INSERT INTO media_items(content_id,type,status,title) VALUES($1,'series','matched','A'),($2,'series','matched','B')", a, b)
	nativeIngestSQL(t, x.pool, "INSERT INTO episodes(content_id,series_id,season_number,episode_number,title) VALUES($1,$2,1,1,'Episode')", episode, a)
	nativeIngestSQL(t, x.pool, "INSERT INTO media_extras(content_id,parent_id,kind,title) VALUES($1,$2,'trailer','Extra')", extra, a)
	return x, folder, a, b, episode, extra
}
func TestNativeOnboardingEffectiveTupleDB(t *testing.T) {
	x, folder, a, b, episode, extra := currentLocalTupleFixture(t)
	for _, tc := range []struct {
		name                                string
		oldContent, oldEpisode, oldExtra    string
		newContent, newEpisode, newExtra    string
		wantContent, wantEpisode, wantExtra string
	}{
		{"nullRetainsContent", a, "", "", "", "", "", a, "", ""},
		{"nullRetainsEpisode", "", episode, "", "", "", "", "", episode, ""},
		{"nullRetainsBoth", a, episode, "", "", "", "", a, episode, ""},
		{"fullySuppliedSame", a, episode, "", a, episode, "", a, episode, ""},
		{"whollyNull", "", "", "", "", "", "", "", "", ""},
		{"incomingExtraClearsBoth", a, episode, "", a, episode, extra, "", "", extra},
		{"incomingExtraRetainsExtraOnly", "", "", extra, "", "", extra, "", "", extra},
		{"nullExtraOverwritesStored", "", "", extra, "", "", "", "", "", ""},
		{"contentMovement", a, "", "", b, "", "", b, "", ""},
		{"episodeMovement", a, "", "", "", episode, "", a, episode, ""},
	} {
		t.Run(tc.name, func(t *testing.T) {
			path := "/local/" + tc.name
			original, err := x.s.fileRepo.Upsert(t.Context(), models.MediaFile{MediaFolderID: folder, FilePath: path, ContentID: tc.oldContent, EpisodeID: tc.oldEpisode, ExtraID: tc.oldExtra})
			if err != nil {
				t.Fatal("stored tuple", err)
			}
			incoming := models.MediaFile{MediaFolderID: folder, FilePath: path, ContentID: tc.newContent, EpisodeID: tc.newEpisode, ExtraID: tc.newExtra}
			saved, err := x.s.fileRepo.Upsert(t.Context(), incoming)
			if err != nil {
				t.Fatal("actual insert/conflict update", err)
			}
			if saved.ID != original.ID || saved.ContentID != tc.wantContent || saved.EpisodeID != tc.wantEpisode || saved.ExtraID != tc.wantExtra {
				t.Fatalf("effective tuple got %d/%s/%s/%s expected %d/%s/%s/%s", saved.ID, saved.ContentID, saved.EpisodeID, saved.ExtraID, original.ID, tc.wantContent, tc.wantEpisode, tc.wantExtra)
			}
		})
	}
	for _, isolation := range []pgx.TxIsoLevel{pgx.RepeatableRead, pgx.Serializable} {
		t.Run(string(isolation)+"_sameLocalConflict", func(t *testing.T) {
			path := "/local/isolation-" + string(isolation)
			original, err := x.s.fileRepo.Upsert(t.Context(), models.MediaFile{MediaFolderID: folder, FilePath: path, ContentID: a, EpisodeID: episode})
			if err != nil {
				t.Fatal(err)
			}
			tx, err := x.pool.BeginTx(t.Context(), pgx.TxOptions{IsoLevel: isolation})
			if err != nil {
				t.Fatal(err)
			}
			defer tx.Rollback(context.Background())
			saved, err := x.s.fileRepo.UpsertTx(t.Context(), tx, models.MediaFile{MediaFolderID: folder, FilePath: path})
			if err != nil {
				t.Fatal("same local conflict acquired native/RC lock", err)
			}
			if saved.ID != original.ID || saved.ContentID != a || saved.EpisodeID != episode {
				t.Fatal("same local conflict changed links")
			}
			if err = tx.Commit(t.Context()); err != nil {
				t.Fatal(err)
			}
		})
	}
}
func TestNativeOnboardingConflictRowRaceDB(t *testing.T) {
	x, folder, a, b, _, _ := currentLocalTupleFixture(t)
	for _, scenario := range []string{"relinkNullRetainsCurrentB", "relinkSuppliedAMovement", "deleteBeforeWait", "pathMoveBeforeWait", "invisibleReplacementLocalB"} {
		t.Run(scenario, func(t *testing.T) {
			path := "/local/race-" + scenario
			original, err := x.s.fileRepo.Upsert(t.Context(), models.MediaFile{MediaFolderID: folder, FilePath: path, ContentID: a})
			if err != nil {
				t.Fatal(err)
			}
			ctx, cancel := context.WithTimeout(t.Context(), 10*time.Second)
			defer cancel()
			blocker, err := x.pool.Begin(ctx)
			if err != nil {
				t.Fatal(err)
			}
			defer blocker.Rollback(context.Background())
			var pid int
			if err = blocker.QueryRow(ctx, "SELECT pg_backend_pid()").Scan(&pid); err != nil {
				t.Fatal(err)
			}
			if _, err = blocker.Exec(ctx, "SELECT id FROM media_files WHERE id=$1 FOR NO KEY UPDATE", original.ID); err != nil {
				t.Fatal(err)
			}
			result := make(chan *models.MediaFile, 1)
			done := make(chan error, 1)
			joined := make(chan struct{})
			incoming := models.MediaFile{MediaFolderID: folder, FilePath: path}
			if scenario == "relinkSuppliedAMovement" {
				incoming.ContentID = a
			}
			go func() {
				defer close(joined)
				saved, err := x.s.fileRepo.Upsert(ctx, incoming)
				result <- saved
				done <- err
			}()
			t.Cleanup(func() {
				cancel()
				select {
				case <-joined:
				case <-time.After(15 * time.Second):
					t.Error("row-wait writer failed to join")
				}
			})
			nativeIngestWaitForBlocker(t, ctx, x.pool, pid)
			var replacementID int
			switch scenario {
			case "relinkNullRetainsCurrentB", "relinkSuppliedAMovement":
				_, err = blocker.Exec(ctx, "UPDATE media_files SET content_id=$2 WHERE id=$1", original.ID, b)
			case "deleteBeforeWait":
				_, err = blocker.Exec(ctx, "DELETE FROM media_files WHERE id=$1", original.ID)
			case "pathMoveBeforeWait":
				_, err = blocker.Exec(ctx, "UPDATE media_files SET file_path=$2 WHERE id=$1", original.ID, path+"-moved")
			case "invisibleReplacementLocalB":
				if _, err = blocker.Exec(ctx, "DELETE FROM media_files WHERE id=$1", original.ID); err == nil {
					replacement, insertErr := x.s.fileRepo.UpsertTx(ctx, blocker, models.MediaFile{MediaFolderID: folder, FilePath: path, ContentID: b})
					err = insertErr
					if err == nil {
						replacementID = replacement.ID
					}
				}
			}
			if err != nil {
				t.Fatal("mutation before row-wait release", err)
			}
			if err = blocker.Commit(ctx); err != nil {
				t.Fatal(err)
			}
			if err = <-done; err != nil {
				t.Fatal("actual conflict waiter", err)
			}
			saved := <-result
			want := ""
			if scenario == "relinkNullRetainsCurrentB" || scenario == "invisibleReplacementLocalB" {
				want = b
			}
			if scenario == "relinkSuppliedAMovement" {
				want = a
			}
			if saved.ContentID != want {
				t.Fatalf("stale inheritance after %s: got=%s want=%s", scenario, saved.ContentID, want)
			}
			if scenario == "invisibleReplacementLocalB" && (replacementID == original.ID || saved.ID != replacementID) {
				t.Fatal("actual invisible conflict UPDATE did not retain replacement identity")
			}
			if scenario == "deleteBeforeWait" || scenario == "pathMoveBeforeWait" {
				if saved.ID == original.ID {
					t.Fatal("disappeared/path-moved candidate was reused")
				}
			}
		})
	}
	t.Run("reverseOrderRetainsConflictRowThroughCommit", func(t *testing.T) {
		path := "/local/reverse-row-retention"
		original, err := x.s.fileRepo.Upsert(t.Context(), models.MediaFile{MediaFolderID: folder, FilePath: path, ContentID: a})
		if err != nil {
			t.Fatal(err)
		}
		ctx, cancel := context.WithTimeout(t.Context(), 10*time.Second)
		defer cancel()
		first, err := x.pool.Begin(ctx)
		if err != nil {
			t.Fatal(err)
		}
		defer first.Rollback(context.Background())
		saved, err := x.s.fileRepo.UpsertTx(ctx, first, models.MediaFile{MediaFolderID: folder, FilePath: path})
		if err != nil || saved.ContentID != a {
			t.Fatal(err)
		}
		var pid int
		if err = first.QueryRow(ctx, "SELECT pg_backend_pid()").Scan(&pid); err != nil {
			t.Fatal(err)
		}
		done := make(chan error, 1)
		joined := make(chan struct{})
		go func() {
			defer close(joined)
			_, err := x.pool.Exec(ctx, "UPDATE media_files SET content_id=$2 WHERE id=$1", original.ID, b)
			done <- err
		}()
		t.Cleanup(func() {
			cancel()
			select {
			case <-joined:
			case <-time.After(15 * time.Second):
				t.Error("reverse writer failed to join")
			}
		})
		nativeIngestWaitForBlocker(t, ctx, x.pool, pid)
		if err = first.Commit(ctx); err != nil {
			t.Fatal(err)
		}
		if err = <-done; err != nil {
			t.Fatal("post-commit relink", err)
		}
		var content string
		if err = x.pool.QueryRow(ctx, "SELECT content_id FROM media_files WHERE id=$1", original.ID).Scan(&content); err != nil || content != b {
			t.Fatal("reverse relink result", err)
		}
	})
}
