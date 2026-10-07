//go:build integration

package catalog_test

import (
	"context"
	"errors"
	"os"
	"strings"
	"testing"
	"time"

	"github.com/Silo-Server/silo-server/internal/bloemtestdb"
	"github.com/Silo-Server/silo-server/internal/models"
	"github.com/Silo-Server/silo-server/internal/scanner"
	"github.com/google/uuid"
	"github.com/jackc/pgx/v5"
	"github.com/jackc/pgx/v5/pgconn"
	"github.com/jackc/pgx/v5/pgxpool"
)

func nativeLocalFileDB(t *testing.T) *pgxpool.Pool {
	t.Helper()
	if os.Getenv("SILO_TEST_DATABASE_URL") != "" {
		t.Fatal("SILO_TEST_DATABASE_URL must be unset")
	}
	path := "../../.superpowers/sdd/2026-10-06-native-storage-persistence/database-url"
	st, err := os.Stat(path)
	if err != nil || st.Mode().Perm() != 0600 {
		t.Fatal("private fixture required")
	}
	b, err := os.ReadFile(path)
	if err != nil {
		t.Fatal("private fixture read")
	}
	template, err := pgxpool.ParseConfig(strings.TrimSpace(string(b)))
	if err != nil {
		t.Fatal("parse private template identity")
	}
	dsn, cleanup, err := bloemtestdb.CloneNativeOnboarding(t.Context(), strings.TrimSpace(string(b)), false)
	if err != nil {
		t.Fatal(err)
	}
	var p *pgxpool.Pool
	t.Cleanup(func() {
		if p != nil {
			p.Close()
		}
		ctx, cancel := context.WithTimeout(context.Background(), 15*time.Second)
		defer cancel()
		if err := cleanup(ctx); err != nil {
			t.Error(err)
		} else {
			t.Log("private UUID clone cleanup verified")
		}
	})
	cfg, err := bloemtestdb.NativeOnboardingPoolConfig(dsn)
	if err != nil {
		t.Fatal(err)
	}
	p, err = pgxpool.NewWithConfig(t.Context(), cfg)
	if err != nil {
		t.Fatal("open clone")
	}
	t.Logf("owned private UUID clone: %s", p.Config().ConnConfig.Database)
	var actual string
	if err = p.QueryRow(t.Context(), "SELECT current_database()").Scan(&actual); err != nil ||
		actual == template.ConnConfig.Database || actual != p.Config().ConnConfig.Database || !strings.HasPrefix(actual, "bloem_storage_test_acore_") {
		t.Fatal("refusing fixture SQL outside the owned UUID clone")
	}
	if err := bloemtestdb.PrepareNativeOnboardingPool(t.Context(), p); err != nil {
		t.Fatal(err)
	}
	return p
}
func TestNativeOnboardingLocalConflictDB(t *testing.T) {
	p := nativeLocalFileDB(t)
	var folder int
	if err := p.QueryRow(t.Context(), "INSERT INTO media_folders(type,name) VALUES('ebook','Local') RETURNING id").Scan(&folder); err != nil {
		t.Fatal(err)
	}
	key := uuid.NewString()
	if _, err := p.Exec(t.Context(), "INSERT INTO media_items(content_id,type,title) VALUES($1,'ebook','Local')", key); err != nil {
		t.Fatal(err)
	}
	r := scanner.NewFileRepository(p)
	mf := models.MediaFile{ContentID: key, MediaFolderID: folder, FilePath: "local-" + uuid.NewString(), FileSize: 1}
	saved, err := r.Upsert(t.Context(), mf)
	if err != nil {
		t.Fatal(err)
	}
	hold, err := p.Begin(t.Context())
	if err != nil {
		t.Fatal(err)
	}
	defer func() { _ = hold.Rollback(context.Background()) }()
	if _, err = hold.Exec(t.Context(), "SELECT content_id FROM media_items WHERE content_id=$1 FOR UPDATE", key); err != nil {
		t.Fatal(err)
	}
	// The retained exact file conflict must return before any new item lock.
	// An added FOR UPDATE NOWAIT or advisory acquisition would refuse here.
	for _, iso := range []pgx.TxIsoLevel{pgx.ReadCommitted, pgx.RepeatableRead, pgx.Serializable} {
		tx, err := p.BeginTx(t.Context(), pgx.TxOptions{IsoLevel: iso})
		if err != nil {
			t.Fatal(err)
		}
		defer func() { _ = tx.Rollback(context.Background()) }()
		incoming := mf
		incoming.ContentID = ""
		incoming.FileSize++
		actual, err := r.UpsertTx(t.Context(), tx, incoming)
		if err != nil {
			t.Fatal(err)
		}
		if actual.ID != saved.ID || actual.ContentID != key {
			t.Fatal("effective NULL-retaining conflict changed identity")
		}
		if err = tx.Commit(t.Context()); err != nil {
			t.Fatal(err)
		}
	}
}
func TestNativeOnboardingItemKeyContentionDB(t *testing.T) {
	p := nativeLocalFileDB(t)
	var folder int
	if err := p.QueryRow(t.Context(), "INSERT INTO media_folders(type,name) VALUES('ebook','Local') RETURNING id").Scan(&folder); err != nil {
		t.Fatal(err)
	}
	key := uuid.NewString()
	hold, err := p.Begin(t.Context())
	if err != nil {
		t.Fatal(err)
	}
	defer func() { _ = hold.Rollback(context.Background()) }()
	if _, err = hold.Exec(t.Context(), "SELECT bloem_native_lock_item_keys($1::text[])", []string{key}); err != nil {
		t.Fatal(err)
	}
	_, err = scanner.NewFileRepository(p).Upsert(t.Context(), models.MediaFile{MediaFolderID: folder, FilePath: "local-" + uuid.NewString(), ContentID: key})
	var pe *pgconn.PgError
	if !errors.As(err, &pe) || pe.Code != "BN003" {
		t.Fatalf("new association ignored retained key exclusion; class %T", err)
	}
	if err = hold.Rollback(t.Context()); err != nil {
		t.Fatal(err)
	}
	if _, err = scanner.NewFileRepository(p).Upsert(t.Context(), models.MediaFile{MediaFolderID: folder, FilePath: "local-" + uuid.NewString(), ContentID: key}); err != nil {
		t.Fatal(err)
	}
}
func TestNativeOnboardingEffectiveTupleDB(t *testing.T) {
	p := nativeLocalFileDB(t)
	var folder int
	if err := p.QueryRow(t.Context(), "INSERT INTO media_folders(type,name) VALUES('ebook','Local') RETURNING id").Scan(&folder); err != nil {
		t.Fatal(err)
	}
	parent, key := uuid.NewString(), uuid.NewString()
	extra := uuid.NewString()
	if _, err := p.Exec(t.Context(), "INSERT INTO media_items(content_id,type,title) VALUES($1,'movie','Parent'),($2,'ebook','Local')", parent, key); err != nil {
		t.Fatal(err)
	}
	if _, err := p.Exec(t.Context(), "INSERT INTO media_extras(content_id,parent_id,kind) VALUES($1,$2,'trailer')", extra, parent); err != nil {
		t.Fatal(err)
	}
	r := scanner.NewFileRepository(p)
	mf := models.MediaFile{ContentID: key, MediaFolderID: folder, FilePath: "local-" + uuid.NewString()}
	saved, err := r.Upsert(t.Context(), mf)
	if err != nil {
		t.Fatal(err)
	}
	mf.ExtraID = extra
	actual, err := r.Upsert(t.Context(), mf)
	if err != nil {
		t.Fatal(err)
	}
	if actual.ID != saved.ID || actual.ContentID != "" || actual.EpisodeID != "" || actual.ExtraID != extra {
		t.Fatal("incoming extra did not clear both links")
	}
	mf.ContentID = ""
	mf.ExtraID = ""
	actual, err = r.Upsert(t.Context(), mf)
	if err != nil {
		t.Fatal(err)
	}
	if actual.ExtraID != "" || actual.ContentID != "" || actual.EpisodeID != "" {
		t.Fatal("NULL extra retained stored extra")
	}
}
func TestNativeOnboardingLocalConflictCurrentRowDB(t *testing.T) {
	p := nativeLocalFileDB(t)
	var folder int
	if err := p.QueryRow(t.Context(), "INSERT INTO media_folders(type,name) VALUES('ebook','Local') RETURNING id").Scan(&folder); err != nil {
		t.Fatal(err)
	}
	a, b := uuid.NewString(), uuid.NewString()
	if _, err := p.Exec(t.Context(), "INSERT INTO media_items(content_id,type,title) VALUES($1,'ebook','A'),($2,'ebook','B')", a, b); err != nil {
		t.Fatal(err)
	}
	r := scanner.NewFileRepository(p)
	mf := models.MediaFile{ContentID: a, MediaFolderID: folder, FilePath: "local-" + uuid.NewString()}
	saved, err := r.Upsert(t.Context(), mf)
	if err != nil {
		t.Fatal(err)
	}
	relink, err := p.Begin(t.Context())
	if err != nil {
		t.Fatal(err)
	}
	defer func() { _ = relink.Rollback(context.Background()) }()
	if _, err = relink.Exec(t.Context(), "UPDATE media_files SET content_id=$1 WHERE id=$2", b, saved.ID); err != nil {
		t.Fatal(err)
	}
	// The row is relinked before the conflict-row wait. Release only after the
	// second connection's actual backend is observably waiting on this lock.
	result := make(chan error, 1)
	var actual *models.MediaFile
	ctx, cancel := context.WithTimeout(t.Context(), 10*time.Second)
	defer cancel()
	rescan, err := p.Acquire(ctx)
	if err != nil {
		t.Fatal(err)
	}
	defer rescan.Release()
	pid := rescan.Conn().PgConn().PID()
	go func() {
		tx, e := rescan.Begin(ctx)
		if e != nil {
			result <- e
			return
		}
		defer func() { _ = tx.Rollback(context.Background()) }()
		incoming := mf
		incoming.ContentID = ""
		actual, e = r.UpsertTx(ctx, tx, incoming)
		if e == nil {
			e = tx.Commit(ctx)
		}
		result <- e
	}()
	for {
		var waiting bool
		err = p.QueryRow(ctx, "SELECT COALESCE(wait_event_type='Lock',false) FROM pg_stat_activity WHERE pid=$1", pid).Scan(&waiting)
		if err != nil {
			t.Fatal("observe conflict wait", err)
		}
		if waiting {
			break
		}
		select {
		case e := <-result:
			t.Fatalf("rescan completed before retained-row wait: %v", e)
		default:
		}
	}
	if err = relink.Commit(ctx); err != nil {
		t.Fatal(err)
	}
	if err = <-result; err != nil {
		t.Fatal(err)
	}
	if actual.ContentID != b || actual.ID != saved.ID {
		t.Fatal("rescan inherited stale pre-wait identity")
	}

}

func TestNativeOnboardingExtraDeleteCleanupDB(t *testing.T) {
	p := nativeLocalFileDB(t)
	var folder int
	if err := p.QueryRow(t.Context(), "INSERT INTO media_folders(type,name) VALUES('movie','Local') RETURNING id").Scan(&folder); err != nil {
		t.Fatal(err)
	}
	for _, iso := range []pgx.TxIsoLevel{pgx.ReadCommitted, pgx.RepeatableRead, pgx.Serializable} {
		t.Run(string(iso), func(t *testing.T) {
			parent, extra := uuid.NewString(), uuid.NewString()
			if _, err := p.Exec(t.Context(), "INSERT INTO media_items(content_id,type,title) VALUES($1,'movie','Parent')", parent); err != nil {
				t.Fatal(err)
			}
			if _, err := p.Exec(t.Context(), "INSERT INTO media_extras(content_id,parent_id,kind) VALUES($1,$2,'trailer')", extra, parent); err != nil {
				t.Fatal(err)
			}
			saved, err := scanner.NewFileRepository(p).Upsert(t.Context(), models.MediaFile{MediaFolderID: folder, ExtraID: extra, FilePath: "local-" + uuid.NewString()})
			if err != nil {
				t.Fatal(err)
			}
			tx, err := p.BeginTx(t.Context(), pgx.TxOptions{IsoLevel: iso})
			if err != nil {
				t.Fatal(err)
			}
			defer func() { _ = tx.Rollback(context.Background()) }()
			if _, err = tx.Exec(t.Context(), "DELETE FROM media_extras WHERE content_id=$1", extra); err != nil {
				t.Fatal("ordinary FK SET NULL cleanup refused", err)
			}
			if err = tx.Commit(t.Context()); err != nil {
				t.Fatal(err)
			}
			var cleared bool
			if err = p.QueryRow(t.Context(), "SELECT extra_id IS NULL FROM media_files WHERE id=$1", saved.ID).Scan(&cleared); err != nil || !cleared {
				t.Fatal("ordinary extra deletion retained backing-file alias")
			}
		})
	}
}
