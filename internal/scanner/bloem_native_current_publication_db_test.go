//go:build integration

package scanner

import (
	"context"
	"errors"
	"github.com/Silo-Server/silo-server/internal/catalog"
	"github.com/Silo-Server/silo-server/internal/models"
	storagev1 "github.com/Silo-Server/silo-server/internal/storageproto/bloem/plugin/v1"
	"github.com/Silo-Server/silo-server/internal/storagesource"
	"github.com/google/uuid"
	"github.com/jackc/pgx/v5"
	"github.com/jackc/pgx/v5/pgconn"
	"google.golang.org/protobuf/proto"
	"testing"
	"time"
)

func currentPublicationRefusal(t *testing.T, err error) {
	t.Helper()
	var typed *catalog.NativeOnboardingError
	var sql *pgconn.PgError
	if err == nil || (!errors.As(err, &typed) && !errors.As(err, &sql)) {
		t.Fatalf("expected actual publication guard refusal, got %T %v", err, err)
	}
}
func TestNativeOnboardingNoAuthorizerDB(t *testing.T) {
	x := nativeIngestSetup(t)
	_, err := x.s.PublishNativeEbook(t.Context(), x.r, x.claim, x.folder, x.file, NativeEbookSidecars{Complete: true})
	currentPublicationRefusal(t, err)
	x.empty(t)
	x.pending(t)
	var n int
	if err = x.pool.QueryRow(t.Context(), "SELECT count(*) FROM media_items WHERE title='The Test Ebook'").Scan(&n); err != nil || n != 0 {
		t.Fatal("trusted no-authorizer path leaked item", err)
	}
}
func TestNativeOnboardingScannerPublicationDB(t *testing.T) {
	x := nativeIngestSetup(t)
	id := x.publish(t)
	var fileID int
	if err := x.pool.QueryRow(t.Context(), "SELECT id FROM media_files WHERE content_id=$1", id).Scan(&fileID); err != nil {
		t.Fatal(err)
	}
	x.discover(t, "v2")
	if current := x.publish(t); current != id {
		t.Fatal("changed revision replaced content identity")
	}
	var savedID int
	if err := x.pool.QueryRow(t.Context(), "SELECT id FROM media_files WHERE content_id=$1", id).Scan(&savedID); err != nil || savedID != fileID {
		t.Fatal("actual conflict Upsert replaced stored ID", err)
	}
	for _, statement := range []string{
		"UPDATE media_files SET content_id=NULL WHERE id=$1",
		"DELETE FROM media_files WHERE id=$1",
	} {
		_, err := x.pool.Exec(t.Context(), statement, fileID)
		var sql *pgconn.PgError
		if !errors.As(err, &sql) || sql.Code != "BN001" {
			t.Fatalf("retained file control must refuse BN001: %T %v", err, err)
		}
	}
	tx, err := x.pool.Begin(t.Context())
	if err != nil {
		t.Fatal(err)
	}
	defer tx.Rollback(context.Background())
	_, err = tx.Exec(t.Context(), "DELETE FROM media_item_libraries WHERE content_id=$1", id)
	var sql *pgconn.PgError
	if !errors.As(err, &sql) || sql.Code != "BN001" {
		t.Fatalf("retained membership must refuse BN001: %T %v", err, err)
	}
}

func currentPublicationInput(x *nativeIngestFixture, p *nativeEbookPrepared) catalog.NativePublicationInput {
	e := x.claim.Entry
	f := buildEbookMediaFile(x.folder, p.contentID, p.location, e.Size, normalizeFileModifiedAt(time.Unix(0, e.ModifiedUnixNano)), &p.book, p.groupKey)
	f.ProbeSource = "native"
	return catalog.NativePublicationInput{Claim: x.claim, FolderID: x.folder.ID, ItemKey: p.contentID, ExistingItemKey: p.existingID, ItemVersion: p.itemVersion, File: f, Sidecars: p.sidecars}
}
func TestNativeOnboardingPublicationPhasesDB(t *testing.T) {
	x := nativeIngestSetup(t)
	p, err := x.s.prepareNativeEbook(t.Context(), x.claim, x.folder, x.file, NativeEbookSidecars{Complete: true})
	if err != nil {
		t.Fatal(err)
	}
	for _, phase := range []string{"withoutBegin", "armedOnly", "itemOnly", "memberOnly", "fileWithoutRecord", "recordWithoutRef", "refWithoutFinish", "finishBeforeCheckpoint", "earlyConstraints", "checkpointFailure"} {
		t.Run(phase, func(t *testing.T) {
			if phase == "checkpointFailure" {
				nativeIngestSQL(t, x.pool, "ALTER TABLE bloem_storage_ingestion ADD CONSTRAINT a_current_checkpoint_failure CHECK(last_entry_id='')")
				defer nativeIngestSQL(t, x.pool, "ALTER TABLE bloem_storage_ingestion DROP CONSTRAINT a_current_checkpoint_failure")
			}
			reached := ""
			err := x.r.PublishAuthorizedIngestion(t.Context(), x.claim, x.authorize, func(ctx context.Context, tx pgx.Tx, e *storagev1.Entry) error {
				base := x.s.nativeEbookPublication(x.r, x.claim, x.folder, p)
				if phase == "withoutBegin" {
					reached = "base"
					return base(ctx, tx, e)
				}
				input := currentPublicationInput(x, p)
				if err := catalog.BeginNativePublicationPermitTx(ctx, tx, input); err != nil {
					return err
				}
				reached = "armed"
				if phase == "armedOnly" {
					return nil
				}
				if phase == "earlyConstraints" {
					returnErr := error(nil)
					_, returnErr = tx.Exec(ctx, "SET CONSTRAINTS ALL IMMEDIATE")
					return returnErr
				}
				if phase == "refWithoutFinish" || phase == "finishBeforeCheckpoint" || phase == "checkpointFailure" {
					if err := base(ctx, tx, e); err != nil {
						return err
					}
					reached = "ref"
					if phase == "refWithoutFinish" {
						return nil
					}
					saved, err := catalog.NativePublicationStoredFileTx(ctx, tx)
					if err != nil {
						return err
					}
					if err = catalog.FinishNativePublicationPermitTx(ctx, tx, saved); err != nil {
						return err
					}
					reached = "finished"
					if phase == "finishBeforeCheckpoint" {
						_, err = tx.Exec(ctx, "SET CONSTRAINTS ALL IMMEDIATE")
						return err
					}
					return nil
				}
				item := &models.MediaItem{ContentID: p.contentID, Type: "ebook", Status: "pending", Title: p.book.Title}
				if err := x.s.itemRepo.UpsertTx(ctx, tx, item); err != nil {
					return err
				}
				reached = "item"
				if phase == "itemOnly" {
					return nil
				}
				if err := insertEbookLibraryMembership(ctx, tx, p.contentID, x.folder.ID); err != nil {
					return err
				}
				reached = "member"
				if phase == "memberOnly" {
					return nil
				}
				saved, err := x.s.fileRepo.UpsertTx(ctx, tx, input.File)
				if err != nil {
					return err
				}
				reached = "file"
				if phase == "fileWithoutRecord" {
					return nil
				}
				if err = catalog.RecordNativePublicationFileTx(ctx, tx, saved.ID); err != nil {
					return err
				}
				reached = "recorded"
				return nil
			})
			var sql *pgconn.PgError
			if !errors.As(err, &sql) || (sql.Code != "BN002" && !(phase == "withoutBegin" && sql.Code == "BN001") && !(phase == "checkpointFailure" && sql.Code == "23514")) {
				t.Fatalf("%s reached=%s expected phase refusal, got %T %v", phase, reached, err, err)
			}
			x.empty(t)
			x.pending(t)
			var count int
			if err = x.pool.QueryRow(t.Context(), "SELECT (SELECT count(*) FROM media_items WHERE content_id=$1)+(SELECT count(*) FROM bloem_native_publication_permits)", p.contentID).Scan(&count); err != nil || count != 0 {
				t.Fatal("phase left item/permit partial commit", err)
			}
			t.Logf("actual %s reached=%s refused SQLSTATE=%s; item/member/file/ref/checkpoint rolled back", phase, reached, sql.Code)
		})
	}
	t.Run("savepointEarlyConstraintRecovery", func(t *testing.T) {
		err := x.r.PublishAuthorizedIngestion(t.Context(), x.claim, x.authorize, func(ctx context.Context, tx pgx.Tx, e *storagev1.Entry) error {
			if _, err := tx.Exec(ctx, "SAVEPOINT publication_probe"); err != nil {
				return err
			}
			if err := catalog.BeginNativePublicationPermitTx(ctx, tx, currentPublicationInput(x, p)); err != nil {
				return err
			}
			_, err := tx.Exec(ctx, "SET CONSTRAINTS ALL IMMEDIATE")
			var sql *pgconn.PgError
			if !errors.As(err, &sql) || sql.Code != "BN002" {
				t.Fatalf("early deferred check did not refuse: %T %v", err, err)
			}
			if _, err = tx.Exec(ctx, "ROLLBACK TO SAVEPOINT publication_probe"); err != nil {
				return err
			}
			if err = catalog.BeginNativePublicationPermitTx(ctx, tx, currentPublicationInput(x, p)); err != nil {
				return err
			}
			if err = x.s.nativeEbookPublication(x.r, x.claim, x.folder, p)(ctx, tx, e); err != nil {
				return err
			}
			saved, err := catalog.NativePublicationStoredFileTx(ctx, tx)
			if err != nil {
				return err
			}
			return catalog.FinishNativePublicationPermitTx(ctx, tx, saved)
		})
		if err != nil {
			t.Fatal("actual same-transaction savepoint recovery", err)
		}
		var n int
		if err = x.pool.QueryRow(t.Context(), "SELECT count(*) FROM bloem_native_publication_permits").Scan(&n); err != nil || n != 0 {
			t.Fatal("permit leaked after recovered COMMIT", err)
		}
	})
}

func TestNativeOnboardingPermitInputsDB(t *testing.T) {
	x := nativeIngestSetup(t)
	p, err := x.s.prepareNativeEbook(t.Context(), x.claim, x.folder, x.file, NativeEbookSidecars{Complete: true})
	if err != nil {
		t.Fatal(err)
	}
	for _, wrong := range []string{"item", "folder", "fileOwnerTuple", "location", "group", "format", "mtime", "size", "claimToken", "leaseOwner", "leaseEpoch", "configuration", "entryRevision", "logicalPath"} {
		t.Run(wrong, func(t *testing.T) {
			input := currentPublicationInput(x, p)
			input.Claim.Entry = proto.Clone(input.Claim.Entry).(*storagev1.Entry)
			switch wrong {
			case "item":
				input.ItemKey = "other"
			case "folder":
				input.FolderID++
			case "fileOwnerTuple":
				input.File.MediaFolderID++
			case "location":
				input.File.FilePath += "x"
			case "group":
				input.File.ContentGroupKey = "unscoped"
			case "format":
				input.File.Container = "mobi"
			case "mtime":
				shifted := input.File.FileModifiedAt.Add(time.Second)
				input.File.FileModifiedAt = &shifted
			case "size":
				input.File.FileSize++
			case "claimToken":
				input.Claim.Token = uuid.New()
			case "leaseOwner":
				input.Claim.Lease.Owner = "foreign-owner"
			case "leaseEpoch":
				input.Claim.Lease.Epoch++
			case "configuration":
				input.Claim.Lease.ConfigurationRevision++
			case "entryRevision":
				input.Claim.Entry.Revision = "wrong"
			case "logicalPath":
				input.Claim.Entry.LogicalPath = "different/book.epub"
			}
			claim := x.claim
			if wrong == "claimToken" || wrong == "leaseOwner" || wrong == "leaseEpoch" || wrong == "configuration" || wrong == "entryRevision" || wrong == "logicalPath" {
				claim = input.Claim
			}
			err := x.r.PublishAuthorizedIngestion(t.Context(), claim, x.authorize, func(ctx context.Context, tx pgx.Tx, _ *storagev1.Entry) error {
				return catalog.BeginNativePublicationPermitTx(ctx, tx, input)
			})
			if err == nil {
				t.Fatal("wrong input admitted")
			}
			var typed *catalog.NativeOnboardingError
			var sql *pgconn.PgError
			if !errors.As(err, &typed) && !errors.As(err, &sql) && !errors.Is(err, storagesource.ErrCheckpointConflict) && !errors.Is(err, storagesource.ErrStaleLease) {
				t.Fatalf("unexpected refusal class %T %v", err, err)
			}
			x.empty(t)
			x.pending(t)
		})
	}
	t.Run("expiredLease", func(t *testing.T) {
		nativeIngestSQL(t, x.pool, "UPDATE bloem_storage_ingestion SET lease_until=clock_timestamp()-interval '1 second' WHERE run_id=$1 AND binding_id=$2", x.claim.Lease.RunID, x.binding.ID)
		_, err := x.s.PublishAuthorizedNativeEbook(t.Context(), x.r, x.claim, x.folder, x.file, NativeEbookSidecars{Complete: true}, x.authorize)
		if !errors.Is(err, storagesource.ErrStaleLease) {
			t.Fatalf("expired lease refusal class %T %v", err, err)
		}
		x.empty(t)
	})
}
func TestNativeOnboardingActualWriterRefusalDB(t *testing.T) {
	x := nativeIngestSetup(t)
	id := x.publish(t)
	var file int
	if err := x.pool.QueryRow(t.Context(), "SELECT id FROM media_files WHERE content_id=$1", id).Scan(&file); err != nil {
		t.Fatal(err)
	}
	nativeIngestSQL(t, x.pool, "INSERT INTO media_items(content_id,type,title,status) VALUES('writer-local','ebook','Local','matched')")
	location, err := storagesource.CatalogLocation(x.binding.ID, x.claim.Entry.Id)
	if err != nil {
		t.Fatal(err)
	}
	if n, err := x.s.fileRepo.UpdateContentIDByPathPrefix(t.Context(), x.folder.ID, location, "writer-local"); err != nil || n != 0 {
		t.Fatal("linked native file must be outside unlinked prefix selection", err)
	}
	for _, writer := range []struct {
		name string
		call func() error
	}{
		{"fileUpdateContentID", func() error { return x.s.fileRepo.UpdateContentID(t.Context(), file, "writer-local") }},
		{"fileUpdateObservedRoot", func() error {
			_, _, err := x.s.fileRepo.UpdateContentIDByObservedRootPath(t.Context(), x.folder.ID, location, "writer-local")
			return err
		}},
		{"markMissing", func() error { return x.s.fileRepo.MarkMissing(t.Context(), file, time.Now()) }},
		{"memberReconcile", func() error {
			tx, err := x.pool.Begin(t.Context())
			if err != nil {
				return err
			}
			defer tx.Rollback(context.Background())
			// Retained-corruption fixture: suppress only the exact file guard
			// and deferred completeness trigger while seeding OLD missing state.
			// Restore their original definitions before the actual writer runs.
			var definitions []string
			for _, name := range []string{"bloem_native_media_files_guard", "bloem_native_media_files_complete"} {
				var definition string
				if err = tx.QueryRow(t.Context(), "SELECT pg_get_triggerdef(oid) FROM pg_trigger WHERE tgname=$1", name).Scan(&definition); err != nil {
					return err
				}
				definitions = append(definitions, definition)
				if _, err = tx.Exec(t.Context(), "DROP TRIGGER "+pgx.Identifier{name}.Sanitize()+" ON media_files"); err != nil {
					return err
				}
			}
			if _, err = tx.Exec(t.Context(), "UPDATE media_files SET missing_since=clock_timestamp() WHERE id=$1", file); err != nil {
				return err
			}
			for _, definition := range definitions {
				if _, err = tx.Exec(t.Context(), definition); err != nil {
					return err
				}
			}
			if err = tx.Commit(t.Context()); err != nil {
				return err
			}
			if !catalog.NativeStorageSchemaReady(t.Context(), x.pool) {
				t.Fatal("missing-file resilience fixture failed to restore exact guard")
			}

			_, _, _, err = catalog.NewLibraryItemRepository(x.pool).ReconcileFolderMembership(t.Context(), x.folder.ID, nil)
			return err
		}},
	} {
		t.Run(writer.name, func(t *testing.T) {
			err := writer.call()
			var sql *pgconn.PgError
			if !errors.As(err, &sql) || sql.Code != "BN001" {
				t.Fatalf("actual native writer %s must refuse BN001, got %T %v", writer.name, err, err)
			}
		})
	}
}
