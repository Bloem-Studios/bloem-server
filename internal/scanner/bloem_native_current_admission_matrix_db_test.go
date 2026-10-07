//go:build integration

package scanner

import (
	"context"
	"errors"
	"strings"
	"testing"

	"github.com/Silo-Server/silo-server/internal/catalog"
	"github.com/Silo-Server/silo-server/internal/models"
	storagev1 "github.com/Silo-Server/silo-server/internal/storageproto/bloem/plugin/v1"
	"github.com/jackc/pgx/v5"
	"github.com/jackc/pgx/v5/pgconn"
)

// These are actual trigger-bearing statements, each with only the named child
// evidence. Member/root FK prerequisites are explicitly separate from absent-K
// file/video/extra/drop cases; no provider/progress cover masks the site.
func currentAdmissionProducer(ctx context.Context, tx pgx.Tx, kind, key string, folder, user int) error {
	var err error
	switch kind {
	case "file":
		_, err = NewFileRepository(nil).UpsertTx(ctx, tx, models.MediaFile{ContentID: key, MediaFolderID: folder, FilePath: "/admission/" + key})
	case "member":
		_, err = tx.Exec(ctx, "INSERT INTO media_item_libraries(content_id,media_folder_id) VALUES($1,$2)", key, folder)
	case "video":
		_, err = tx.Exec(ctx, "INSERT INTO seasons(content_id,series_id,season_number) VALUES($1,'admission-parent',1)", key)
	case "root":
		_, err = tx.Exec(ctx, "INSERT INTO media_item_roots(content_id,media_folder_id,canonical_root_path) VALUES($1,$2,$1)", key, folder)
	case "extra":
		_, err = tx.Exec(ctx, "INSERT INTO media_extras(content_id,parent_id,kind,title) VALUES($1,'admission-parent','trailer','Admission')", key)
	case "drop":
		_, err = tx.Exec(ctx, "INSERT INTO user_dropped_series(user_id,profile_id,series_id) VALUES($1,'admission-synthetic-profile',$2)", user, key)
	default:
		panic(kind)
	}
	return err
}
func currentAdmissionState(t *testing.T, err error, want string) {
	t.Helper()
	var sql *pgconn.PgError
	if !errors.As(err, &sql) || sql.Code != want {
		t.Fatalf("expected admission SQLSTATE %s; got %T %v", want, err, err)
	}
}
func TestNativeOnboardingNegativeAdmissionDB(t *testing.T) {
	x := nativeIngestSetup(t)
	var folder, user int
	if err := x.pool.QueryRow(t.Context(), "INSERT INTO media_folders(type,name,owner_id) VALUES('movies','Admission',bloem_platform_resource_owner_id()) RETURNING id").Scan(&folder); err != nil {
		t.Fatal(err)
	}
	if err := x.pool.QueryRow(t.Context(), "SELECT id FROM users WHERE username='a-current-scanner-admin'").Scan(&user); err != nil {
		t.Fatal(err)
	}
	nativeIngestSQL(t, x.pool, "INSERT INTO media_items(content_id,type,status,title) VALUES('admission-parent','series','matched','Parent')")
	kinds := []string{"file", "member", "video", "root", "extra", "drop"}
	for _, isolation := range []pgx.TxIsoLevel{pgx.RepeatableRead, pgx.Serializable} {
		for _, kind := range kinds {
			t.Run(string(isolation)+"/"+kind, func(t *testing.T) {
				tx, err := x.pool.BeginTx(t.Context(), pgx.TxOptions{IsoLevel: isolation})
				if err != nil {
					t.Fatal(err)
				}
				defer tx.Rollback(context.Background())
				key := "absent-" + string(isolation) + "-" + kind
				var present bool
				if err = tx.QueryRow(t.Context(), "SELECT EXISTS(SELECT 1 FROM media_items WHERE content_id=$1)", key).Scan(&present); err != nil || present {
					t.Fatal("absent snapshot not established", err)
				}
				currentAdmissionState(t, currentAdmissionProducer(t.Context(), tx, kind, key, folder, user), "BN003")
			})
		}
	}
	// READ COMMITTED local producer commits first. Begin must reject retained
	// local evidence. Its refusal and the still-pending real claim are observable.
	for _, kind := range kinds {
		t.Run("localCommitsFirst/"+kind, func(t *testing.T) {
			p, err := x.s.prepareNativeEbook(t.Context(), x.claim, x.folder, x.file, NativeEbookSidecars{Complete: true})
			if err != nil {
				t.Fatal(err)
			}
			tx, err := x.pool.Begin(t.Context())
			if err != nil {
				t.Fatal(err)
			}
			defer tx.Rollback(context.Background())
			if kind == "member" || kind == "root" {
				if _, err = tx.Exec(t.Context(), "INSERT INTO media_items(content_id,type,status,title) VALUES($1,'ebook','matched','Local FK prerequisite')", p.contentID); err != nil {
					t.Fatal(err)
				}
			}
			if err = currentAdmissionProducer(t.Context(), tx, kind, p.contentID, folder, user); err != nil {
				t.Fatal("local producer", err)
			}
			blockedErr := x.r.PublishAuthorizedIngestion(t.Context(), x.claim, x.authorize, func(ctx context.Context, contender pgx.Tx, _ *storagev1.Entry) error {
				return catalog.BeginNativePublicationPermitTx(ctx, contender, currentPublicationInput(x, p))
			})
			currentAdmissionState(t, blockedErr, "BN003")
			x.pending(t)
			if err = tx.Commit(t.Context()); err != nil {
				t.Fatal("local commit", err)
			}
			err = x.r.PublishAuthorizedIngestion(t.Context(), x.claim, x.authorize, func(ctx context.Context, tx pgx.Tx, _ *storagev1.Entry) error {
				return catalog.BeginNativePublicationPermitTx(ctx, tx, currentPublicationInput(x, p))
			})
			var typed *catalog.NativeOnboardingError
			if !errors.As(err, &typed) {
				t.Fatalf("local evidence did not refuse Begin: %T %v", err, err)
			}
			x.pending(t)
			var refs, permits int
			if err = x.pool.QueryRow(t.Context(), "SELECT (SELECT count(*) FROM bloem_storage_file_refs),(SELECT count(*) FROM bloem_native_publication_permits)").Scan(&refs, &permits); err != nil || refs != 0 || permits != 0 {
				t.Fatal("ref/permit escaped", err)
			}
			// Legal ordinary DELETE cleans only this named producer's local evidence.
			tables := map[string]string{"file": "media_files", "member": "media_item_libraries", "video": "seasons", "root": "media_item_roots", "extra": "media_extras", "drop": "user_dropped_series"}
			col := "content_id"
			if kind == "drop" {
				col = "series_id"
			}
			nativeIngestSQL(t, x.pool, "DELETE FROM "+pgx.Identifier{tables[kind]}.Sanitize()+" WHERE "+pgx.Identifier{col}.Sanitize()+"=$1", p.contentID)
			if kind == "member" || kind == "root" {
				nativeIngestSQL(t, x.pool, "DELETE FROM media_items WHERE content_id=$1", p.contentID)
			}
		})
	}
	// Actual Begin holds absent K before any child writer arrives. Each producer
	// must fail immediately through the shared key admission, before mutation.
	p, err := x.s.prepareNativeEbook(t.Context(), x.claim, x.folder, x.file, NativeEbookSidecars{Complete: true})
	if err != nil {
		t.Fatal(err)
	}
	err = x.r.PublishAuthorizedIngestion(t.Context(), x.claim, x.authorize, func(ctx context.Context, tx pgx.Tx, e *storagev1.Entry) error {
		if err := catalog.BeginNativePublicationPermitTx(ctx, tx, currentPublicationInput(x, p)); err != nil {
			return err
		}
		for _, kind := range kinds {
			t.Run("nativeBeginFirst/"+kind, func(t *testing.T) {
				writer, err := x.pool.Begin(ctx)
				if err != nil {
					t.Fatal(err)
				}
				defer writer.Rollback(context.Background())
				currentAdmissionState(t, currentAdmissionProducer(ctx, writer, kind, p.contentID, folder, user), "BN003")
			})
		}
		if err := x.s.nativeEbookPublication(x.r, x.claim, x.folder, p)(ctx, tx, e); err != nil {
			return err
		}
		saved, err := catalog.NativePublicationStoredFileTx(ctx, tx)
		if err != nil {
			return err
		}
		return catalog.FinishNativePublicationPermitTx(ctx, tx, saved)
	})
	if err != nil {
		t.Fatal("actual publication after all producer refusals", err)
	}
	var complete bool
	if err = x.pool.QueryRow(t.Context(), "SELECT pending_token IS NULL AND last_entry_id='book' FROM bloem_storage_ingestion WHERE run_id=$1 AND binding_id=$2", x.claim.Lease.RunID, x.binding.ID).Scan(&complete); err != nil || !complete {
		t.Fatal("publication checkpoint not committed", err)
	}
	for _, kind := range kinds {
		t.Run("nativeCommitsFirst/"+kind, func(t *testing.T) {
			tx, err := x.pool.Begin(t.Context())
			if err != nil {
				t.Fatal(err)
			}
			defer tx.Rollback(context.Background())
			currentAdmissionState(t, currentAdmissionProducer(t.Context(), tx, kind, p.contentID, folder, user), "BN001")
		})
	}
}

// Force index availability on the tiny isolated fixture; this is not a claim
// about planner costs at customer cardinalities. No index/schema is added.
func TestNativeOnboardingIndexedLookupsDB(t *testing.T) {
	x := nativeIngestSetup(t)
	key := x.publish(t)
	tx, err := x.pool.Begin(t.Context())
	if err != nil {
		t.Fatal(err)
	}
	defer tx.Rollback(context.Background())
	if _, err = tx.Exec(t.Context(), "SET LOCAL enable_seqscan=off"); err != nil {
		t.Fatal(err)
	}
	queries := map[string]string{
		"filePath":     "SELECT id FROM media_files WHERE file_path=$1 FOR NO KEY UPDATE",
		"fileItem":     "SELECT id FROM media_files WHERE content_id=$1 OR episode_id=$1 OR extra_id=$1",
		"item":         "SELECT content_id FROM media_items WHERE content_id=$1",
		"member":       "SELECT media_folder_id FROM media_item_libraries WHERE content_id=$1",
		"drop":         "SELECT series_id FROM user_dropped_series WHERE series_id=$1",
		"providerDrop": "SELECT series_id FROM watch_provider_dropped_items WHERE series_id=$1",
		"extra":        "SELECT content_id FROM media_extras WHERE content_id=$1 OR parent_id=$1",
		"episodeAlias": "SELECT content_id FROM episodes WHERE content_id=$1 OR series_id=$1 OR season_id=$1",
		"seasonAlias":  "SELECT content_id FROM seasons WHERE content_id=$1 OR series_id=$1",
	}
	for name, q := range queries {
		t.Run(name, func(t *testing.T) {
			arg := key
			if name == "filePath" {
				if err := tx.QueryRow(t.Context(), "SELECT file_path FROM media_files WHERE content_id=$1", key).Scan(&arg); err != nil {
					t.Fatal(err)
				}
			}
			rows, err := tx.Query(t.Context(), "EXPLAIN (COSTS OFF) "+q, arg)
			if err != nil {
				t.Fatal(err)
			}
			var parts []string
			for rows.Next() {
				var line string
				if err = rows.Scan(&line); err != nil {
					t.Fatal(err)
				}
				parts = append(parts, line)
			}
			err = rows.Err()
			rows.Close()
			if err != nil {
				t.Fatal(err)
			}
			plan := strings.Join(parts, "\n")
			t.Log(plan)
			if !strings.Contains(plan, "Index") || strings.Contains(plan, "Seq Scan") {
				t.Fatalf("exact lookup lacks index plan: %s", plan)
			}
		})
	}
	t.Run("refFile", func(t *testing.T) {
		var file int
		if err := tx.QueryRow(t.Context(), "SELECT id FROM media_files WHERE content_id=$1", key).Scan(&file); err != nil {
			t.Fatal(err)
		}
		rows, err := tx.Query(t.Context(), "EXPLAIN (COSTS OFF) SELECT binding_id FROM bloem_storage_file_refs WHERE media_file_id=$1", file)
		if err != nil {
			t.Fatal(err)
		}
		defer rows.Close()
		var plan string
		for rows.Next() {
			var line string
			if err = rows.Scan(&line); err != nil {
				t.Fatal(err)
			}
			plan += line + "\n"
		}
		if err = rows.Err(); err != nil {
			t.Fatal(err)
		}
		t.Log(plan)
		if !strings.Contains(plan, "Index") || strings.Contains(plan, "Seq Scan") {
			t.Fatal(plan)
		}
	})
}

func TestNativeOnboardingPermitMutationsDB(t *testing.T) {
	x := nativeIngestSetup(t)
	p, err := x.s.prepareNativeEbook(t.Context(), x.claim, x.folder, x.file, NativeEbookSidecars{Complete: true})
	if err != nil {
		t.Fatal(err)
	}
	for _, column := range []string{"source_owner_id", "folder_owner_id", "runtime_generation", "installation_key", "library_revision", "configuration_revision", "binding_id", "lease_token", "item_key", "folder_key", "entry_revision", "stored_file_key"} {
		t.Run(column, func(t *testing.T) {
			err := x.r.PublishAuthorizedIngestion(t.Context(), x.claim, x.authorize, func(ctx context.Context, tx pgx.Tx, _ *storagev1.Entry) error {
				if err := catalog.BeginNativePublicationPermitTx(ctx, tx, currentPublicationInput(x, p)); err != nil {
					return err
				}
				value := column + "+1"
				switch column {
				case "source_owner_id", "folder_owner_id", "binding_id", "lease_token":
					value = "gen_random_uuid()"
				case "item_key", "entry_revision":
					value = "'wrong-tuple'"
				case "stored_file_key":
					value = "1"
				}
				_, err := tx.Exec(ctx, "UPDATE bloem_native_publication_permits SET "+pgx.Identifier{column}.Sanitize()+"="+value+" WHERE xid=txid_current()")
				return err
			})
			currentAdmissionState(t, err, "BN002")
			x.empty(t)
			x.pending(t)
			var n int
			if err = x.pool.QueryRow(t.Context(), "SELECT count(*) FROM bloem_native_publication_permits").Scan(&n); err != nil || n != 0 {
				t.Fatal("permit mutation escaped rollback", err)
			}
		})
	}
}
