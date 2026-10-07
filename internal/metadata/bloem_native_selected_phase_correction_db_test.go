//go:build integration

package metadata

import (
	"context"
	"fmt"
	"slices"
	"strings"
	"sync"
	"sync/atomic"
	"testing"
	"time"

	"github.com/Silo-Server/silo-server/internal/catalog"
	"github.com/Silo-Server/silo-server/internal/catalog/filesplit"
	"github.com/Silo-Server/silo-server/internal/contentid"
	"github.com/Silo-Server/silo-server/internal/metadata/translation"
	"github.com/Silo-Server/silo-server/internal/models"
	"github.com/Silo-Server/silo-server/internal/scanner"
	"github.com/google/uuid"
	"github.com/jackc/pgx/v5"
	"github.com/jackc/pgx/v5/pgxpool"
)

// Real producer/SQL regressions with a deterministic host admission callback.
// These do not claim HTTP/PIN middleware or complete scanner integration proof.
func TestNativeSelectedSeriesSplitDestinationsDB(t *testing.T) {
	pool, _ := writerCorrectionDatabase(t)
	for _, tc := range []struct {
		name                                   string
		shared, nilSource, absent, deny, whole bool
	}{
		{name: "partial source episode hidden destination", shared: true, deny: true},
		{name: "nil source episode hidden destination", nilSource: true, deny: true},
		{name: "absent deterministic destination", absent: true},
		{name: "occupied allowed destination"},
		{name: "last active source file allowed", whole: true},
	} {
		t.Run(tc.name, func(t *testing.T) {
			source := "selected-series-" + uuid.NewString()
			target, ok := contentid.ForSeries(contentid.ProviderIDs{Imdb: "tt" + fmt.Sprint(time.Now().UnixNano())})
			if !ok {
				t.Fatal("target id")
			}
			fromEpisode := source + "-episode"
			toEpisode, ok := contentid.ForEpisode(target, 1, 1)
			if !ok {
				t.Fatal("episode id")
			}
			for _, id := range []string{source, target} {
				metadataExec(t, pool, `INSERT INTO media_items(content_id,type,status,title) VALUES($1,'series','matched','Selected split')`, id)
			}
			var folder int
			if err := pool.QueryRow(t.Context(), `INSERT INTO media_folders(type,name,owner_id) VALUES('series','Selected split',bloem_platform_resource_owner_id()) RETURNING id`).Scan(&folder); err != nil {
				t.Fatal(err)
			}
			for _, id := range []string{source, target} {
				metadataExec(t, pool, `INSERT INTO media_item_libraries(content_id,media_folder_id) VALUES($1,$2)`, id, folder)
			}
			metadataExec(t, pool, `INSERT INTO episodes(content_id,series_id,season_number,episode_number,title) VALUES($1,$2,1,1,'Source')`, fromEpisode, source)
			metadataExec(t, pool, `INSERT INTO episode_libraries(episode_id,media_folder_id,first_seen_at) VALUES($1,$2,NOW())`, fromEpisode, folder)
			if !tc.absent {
				metadataExec(t, pool, `INSERT INTO episodes(content_id,series_id,season_number,episode_number,title) VALUES($1,$2,1,1,'Target')`, toEpisode, target)
			}
			oldEpisode := fromEpisode
			if tc.nilSource {
				oldEpisode = ""
			}
			repo := scanner.NewFileRepository(pool)
			moved, err := repo.Upsert(t.Context(), models.MediaFile{FilePath: "/selected/" + uuid.NewString() + ".mkv", ContentID: source, EpisodeID: oldEpisode, MediaFolderID: folder, SeasonNumber: 1, EpisodeNumber: 1})
			if err != nil {
				t.Fatal(err)
			}
			// A second file preserves source-root membership; only the shared case
			// keeps the same source episode and therefore filters its state pair out.
			remainingEpisode := ""
			if tc.shared {
				remainingEpisode = fromEpisode
			}
			remaining, err := repo.Upsert(t.Context(), models.MediaFile{FilePath: "/selected/" + uuid.NewString() + ".mkv", ContentID: source, EpisodeID: remainingEpisode, MediaFolderID: folder, SeasonNumber: 1, EpisodeNumber: 1})
			if err != nil {
				t.Fatal(err)
			}
			if tc.whole {
				// The real admin route requires a subset of stored files.
				// An already-missing remainder permits the last active file to move.
				metadataExec(t, pool, "UPDATE media_files SET missing_since=NOW() WHERE id=$1", remaining.ID)
			}
			user, profile, _ := metadataCurrentProfile(t, pool)
			writerCorrectionProgress(t, pool, user, profile, fromEpisode, 31, writerCorrectionStamp, 0)
			sawTarget, sawProspective := false, false
			ctx := catalog.WithNativePhaseAuthorizer(t.Context(), pool, func(checkCtx context.Context, q catalog.NativePhaseQuery, set catalog.NativePhaseTargets) error {
				if tc.whole {
					actual := catalog.NativePhaseItemAccess{Query: q}
					for _, id := range []string{source, target} {
						if err := actual.EnsureAccessible(checkCtx, id, catalog.AccessFilter{AllowedLibraryIDs: []int{folder}}); err != nil {
							return err
						}
					}
				}
				if slices.Contains(set.ContentIDs, toEpisode) {
					sawTarget = true
					if tc.deny {
						return &catalog.NativePhaseRefusal{Code: "not_found"}
					}
				}
				for _, p := range set.Prospective {
					if p.ContentID == toEpisode {
						sawProspective = slices.Contains(p.SourceIDs, fromEpisode) && slices.Contains(p.ParentIDs, target)
					}
				}
				return nil
			})
			tx, err := pool.Begin(ctx)
			if err != nil {
				t.Fatal(err)
			}
			defer selectedPhaseRollback(t, tx)
			result, moveErr := filesplit.Move(ctx, tx, filesplit.Options{FromContentID: source, ToContentID: target, ItemType: "series", Files: []filesplit.File{{ID: moved.ID, ContentID: source, MediaFolderID: folder, FilePath: moved.FilePath, SeasonNumber: 1, EpisodeNumber: 1, EpisodeID: oldEpisode}}})
			if tc.deny {
				selectedPhaseCode(t, moveErr, "not_found")
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
			if tc.whole {
				var memberships int
				if err := pool.QueryRow(ctx, "SELECT count(*) FROM media_item_libraries WHERE content_id=$1", source).Scan(&memberships); err != nil || memberships != 0 {
					t.Fatal("emptied source library membership was not removed", err)
				}
			}
			var owner, episode string
			if err := pool.QueryRow(ctx, `SELECT content_id,COALESCE(episode_id,'') FROM media_files WHERE id=$1`, moved.ID).Scan(&owner, &episode); err != nil {
				t.Fatal(err)
			}
			var members int
			if err := pool.QueryRow(ctx, `SELECT count(*) FROM episode_libraries WHERE episode_id=$1`, toEpisode).Scan(&members); err != nil {
				t.Fatal(err)
			}
			if tc.deny {
				if !sawTarget || owner != source || episode != oldEpisode || members != 0 {
					t.Fatalf("hidden relink changed: targetSeen=%v owner=%s episode=%s members=%d", sawTarget, owner, episode, members)
				}
			} else if tc.absent {
				if !sawProspective || owner != target || episode != "" || len(result.EpisodePairs) != 1 || result.EpisodePairs[0].To != toEpisode {
					t.Fatalf("absent destination state semantics lost: %+v", result)
				}
				var count int
				if err := pool.QueryRow(ctx, `SELECT count(*) FROM episodes WHERE content_id=$1`, toEpisode).Scan(&count); err != nil || count != 0 {
					t.Fatal("absent episode fabricated", err)
				}
				if err := pool.QueryRow(ctx, `SELECT count(*) FROM user_watch_progress WHERE media_item_id=$1`, toEpisode).Scan(&count); err != nil || count != 1 {
					t.Fatal("soft progress pair not moved", err)
				}
			} else if !sawTarget || owner != target || episode != toEpisode || members != 1 {
				t.Fatalf("allowed relink failed: owner=%s episode=%s members=%d", owner, episode, members)
			}
		})
	}
}

type selectedWriterRequestKey struct{}

func selectedWaitForBlockedWriter(t *testing.T, pool *pgxpool.Pool, blocker uint32) {
	t.Helper()
	ctx, cancel := context.WithTimeout(t.Context(), 5*time.Second)
	defer cancel()
	for {
		var waiting bool
		err := pool.QueryRow(ctx, `SELECT EXISTS(SELECT 1 FROM pg_stat_activity WHERE datname=current_database() AND $1::int=ANY(pg_blocking_pids(pid)))`, int(blocker)).Scan(&waiting)
		if err != nil {
			t.Fatal("writer did not reach actual row-lock wait", err)
		}
		if waiting {
			return
		}
	}
}

func TestNativeSelectedStoredWinnerAfterWaitDB(t *testing.T) {
	pool, _ := writerCorrectionDatabase(t)
	for _, operation := range []string{"upsert", "identity"} {
		for _, replacement := range []bool{false, true} {
			for _, deny := range []bool{true, false} {
				t.Run(fmt.Sprintf("%s/replacement=%v/deny=%v", operation, replacement, deny), func(t *testing.T) {
					source, other := writerCorrectionLocal(t, pool), writerCorrectionLocal(t, pool)
					file := writerCorrectionFile(t, pool, source)
					var path string
					var folder int
					if err := pool.QueryRow(t.Context(), `SELECT file_path,media_folder_id FROM media_files WHERE id=$1`, file).Scan(&path, &folder); err != nil {
						t.Fatal(err)
					}
					blocker, err := pool.Begin(t.Context())
					if err != nil {
						t.Fatal(err)
					}
					defer selectedPhaseRollback(t, blocker)
					if replacement {
						if _, err := blocker.Exec(t.Context(), `DELETE FROM media_files WHERE id=$1`, file); err != nil {
							t.Fatal(err)
						}
						if err := blocker.QueryRow(t.Context(), `INSERT INTO media_files(file_path,media_folder_id,content_id,base_title) VALUES($1,$2,$3,'Concurrent winner') RETURNING id`, path, folder, other).Scan(&file); err != nil {
							t.Fatal(err)
						}
					} else {
						if _, err := blocker.Exec(t.Context(), `UPDATE media_files SET content_id=$2,base_title='Concurrent winner' WHERE id=$1`, file, other); err != nil {
							t.Fatal(err)
						}
					}
					hidden := ""
					if deny {
						hidden = other
					}
					ctx, cancel := context.WithTimeout(selectedPhaseContext(t, pool, hidden), 10*time.Second)
					defer cancel()
					done := make(chan error, 1)
					finished := make(chan struct{})
					defer selectedPhaseDrain(t, cancel, finished)
					go func() {
						defer close(finished)
						repo := scanner.NewFileRepository(pool)
						input := models.MediaFile{ContentID: source, FilePath: path, MediaFolderID: folder, BaseTitle: "Request update"}
						if operation == "identity" {
							_, err := repo.UpdateIdentity(ctx, input)
							done <- err
						} else {
							_, err := repo.Upsert(ctx, input)
							done <- err
						}
					}()
					selectedWaitForBlockedWriter(t, pool, blocker.Conn().PgConn().PID())
					if err := blocker.Commit(t.Context()); err != nil {
						t.Fatal(err)
					}
					var writeErr error
					select {
					case writeErr = <-done:
					case <-ctx.Done():
						t.Fatal("writer did not finish", ctx.Err())
					}
					if deny {
						selectedPhaseCode(t, writeErr, "not_found")
					} else if writeErr != nil {
						t.Fatal(writeErr)
					}
					var title, owner string
					if err := pool.QueryRow(t.Context(), `SELECT base_title,content_id FROM media_files WHERE id=$1`, file).Scan(&title, &owner); err != nil {
						t.Fatal(err)
					}
					if deny && (owner != other || title != "Concurrent winner") {
						t.Fatal("hidden post-wait owner was changed")
					}
					if !deny && title != "Request update" {
						t.Fatal("allowed winner was not updated")
					}
				})
			}
		}
	}
}

func TestNativeSelectedAbsentPathConflictDB(t *testing.T) {
	pool, trace := writerCorrectionDatabase(t)
	for _, suppliedTx := range []bool{false, true} {
		for _, deny := range []bool{true, false} {
			t.Run(fmt.Sprintf("tx=%v/deny=%v", suppliedTx, deny), func(t *testing.T) {
				source, other := writerCorrectionLocal(t, pool), writerCorrectionLocal(t, pool)
				existing := writerCorrectionFile(t, pool, source)
				var folder int
				if err := pool.QueryRow(t.Context(), `SELECT media_folder_id FROM media_files WHERE id=$1`, existing).Scan(&folder); err != nil {
					t.Fatal(err)
				}
				path := "/selected-absent/" + uuid.NewString() + ".mkv"
				hidden := ""
				if deny {
					hidden = other
				}
				ctx, cancel := context.WithTimeout(context.WithValue(selectedPhaseContext(t, pool, hidden), selectedWriterRequestKey{}, true), 10*time.Second)
				defer cancel()
				atInsert, resume := make(chan struct{}), make(chan struct{})
				var once sync.Once
				trace.start()
				trace.mu.Lock()
				trace.startHook = func(qctx context.Context, event writerCorrectionEvent) {
					if qctx.Value(selectedWriterRequestKey{}) == true && strings.HasPrefix(event.SQL, "INSERT INTO media_files") && strings.Contains(event.SQL, "ON CONFLICT (file_path) DO NOTHING") {
						once.Do(func() {
							close(atInsert)
							select {
							case <-resume:
							case <-ctx.Done():
							}
						})
					}
				}
				trace.mu.Unlock()
				defer trace.stop()
				done := make(chan error, 1)
				finished := make(chan struct{})
				defer selectedPhaseDrain(t, cancel, finished)
				go func() {
					defer close(finished)
					repo := scanner.NewFileRepository(pool)
					input := models.MediaFile{ContentID: source, FilePath: path, MediaFolderID: folder, BaseTitle: "Request update"}
					var err error
					if suppliedTx {
						var tx pgx.Tx
						tx, err = pool.Begin(ctx)
						if err == nil {
							defer selectedPhaseRollback(t, tx)
							_, err = repo.UpsertTx(ctx, tx, input)
							if err == nil {
								err = tx.Commit(ctx)
							}
						}
					} else {
						_, err = repo.Upsert(ctx, input)
					}
					done <- err
				}()
				select {
				case <-atInsert:
				case <-ctx.Done():
					t.Fatal("absent insert boundary not reached", ctx.Err())
				}
				winner, err := scanner.NewFileRepository(pool).Upsert(t.Context(), models.MediaFile{ContentID: other, FilePath: path, MediaFolderID: folder, BaseTitle: "Concurrent winner"})
				if err != nil {
					close(resume)
					t.Fatal(err)
				}
				close(resume)
				var writeErr error
				select {
				case writeErr = <-done:
				case <-ctx.Done():
					t.Fatal("conflict writer did not finish", ctx.Err())
				}
				trace.stop()
				if deny {
					selectedPhaseCode(t, writeErr, "not_found")
				} else if writeErr != nil {
					t.Fatal(writeErr)
				}
				var owner, title string
				if err := pool.QueryRow(t.Context(), `SELECT content_id,base_title FROM media_files WHERE id=$1`, winner.ID).Scan(&owner, &title); err != nil {
					t.Fatal(err)
				}
				if deny && (owner != other || title != "Concurrent winner") {
					t.Fatal("unseen hidden conflict winner mutated")
				}
				if !deny && (owner != source || title != "Request update") {
					t.Fatal("allowed conflict winner not updated")
				}
			})
		}
	}
}

type selectedAcceptedJobRepository struct {
	*translation.PgRepository
	accepted atomic.Bool
	done     chan struct{}
	once     sync.Once
}

func (r *selectedAcceptedJobRepository) InsertJob(ctx context.Context, job *translation.Job) error {
	err := r.PgRepository.InsertJob(ctx, job)
	if err == nil {
		r.accepted.Store(true)
	}
	return err
}
func (r *selectedAcceptedJobRepository) CompleteJob(ctx context.Context, id int64, message string, done, total int) error {
	err := r.PgRepository.CompleteJob(ctx, id, message, done, total)
	if err == nil {
		r.once.Do(func() { close(r.done) })
	}
	return err
}
func TestNativeSelectedTranslationAcceptanceDispatchDB(t *testing.T) {
	pool, _ := writerCorrectionDatabase(t)
	id := writerCorrectionLocal(t, pool)
	pg := translation.NewPgRepository(pool)
	repo := &selectedAcceptedJobRepository{PgRepository: pg, done: make(chan struct{})}
	appCtx, cancel := context.WithCancel(t.Context())
	defer cancel()
	sem := make(chan struct{}, 1)
	sem <- struct{}{}
	locs := &translation.CatalogLocalizationStore{Items: catalog.NewMediaItemLocalizationRepository(pool), Seasons: catalog.NewSeasonLocalizationRepository(pool), Episodes: catalog.NewEpisodeLocalizationRepository(pool)}
	svc := translation.NewService(appCtx, translation.Config{Enabled: true, Configured: true, ChatModel: "selected-test"}, repo, pg, locs, nil, sem, nil)
	var checks atomic.Int32
	ctx := catalog.WithNativePhaseAuthorizer(t.Context(), pool, func(context.Context, catalog.NativePhaseQuery, catalog.NativePhaseTargets) error {
		checks.Add(1)
		if repo.accepted.Load() {
			return &catalog.NativePhaseRefusal{Code: "forbidden"}
		}
		return nil
	})
	req := translation.JobRequest{ContentID: id, TargetKind: translation.TargetItem, TargetLanguage: "fr"}
	job, err := svc.Enqueue(ctx, req)
	if err != nil || job == nil {
		<-sem
		t.Fatalf("accepted job not returned: %v", err)
	}
	if checks.Load() != 1 {
		<-sem
		t.Fatal("authorization repeated after durable acceptance")
	}
	duplicate, err := svc.Enqueue(selectedPhaseContext(t, pool, ""), req)
	if err != nil || duplicate == nil || duplicate.ID != job.ID {
		<-sem
		t.Fatalf("active duplicate changed: %v", err)
	}
	<-sem
	select {
	case <-repo.done:
	case <-time.After(5 * time.Second):
		t.Fatal("durable accepted job was stranded")
	}
	stored, err := pg.GetJob(t.Context(), job.ID)
	if err != nil || stored == nil || stored.Status != "completed" {
		t.Fatalf("accepted job state=%+v error=%v", stored, err)
	}
	// Prove the same originating callback really is now a refusal; it was not
	// silently changed to trusted merely to make the acceptance test pass.
	if err := catalog.RequireNativePhase(ctx, pool, catalog.NativePhaseTargets{ContentIDs: []string{id}}); !catalog.IsNativePhaseRefusal(err) {
		t.Fatal("post-acceptance origin was not revoked", err)
	}
}
