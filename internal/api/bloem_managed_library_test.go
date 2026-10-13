package api

import (
	"context"
	"os"
	"strings"
	"testing"
	"time"

	"github.com/google/uuid"
	"github.com/jackc/pgx/v5/pgxpool"

	"github.com/Silo-Server/silo-server/internal/access"
	"github.com/Silo-Server/silo-server/internal/managedtracking"
)

func TestManagedLibraryFailsClosedWithoutCatalog(t *testing.T) {
	p := managedtracking.Profile{AccountID: "42", ProfileID: "profile", AccountActive: true, Active: true}
	s := &managedCalendarSource{resolver: managedCalendarResolverFixture{scope: access.Scope{UserID: 42, ProfileID: "profile", ProfileVerified: true}}}
	if _, err := s.ReadLibrary(context.Background(), p, managedtracking.LibraryQuery{View: "catalog", Limit: 20}); err == nil {
		t.Fatal("missing catalog accepted")
	}
	if _, err := s.LibraryAuthority(t.Context(), p); err == nil {
		t.Fatal("missing catalog authority accepted")
	}
}

func TestManagedLibraryUsesNativeAccessAndAvailability(t *testing.T) {
	dsn := os.Getenv("BLOEM_MANAGED_TEST_DATABASE_URL")
	if dsn == "" {
		t.Skip("explicit disposable fixture database required")
	}
	cfg, err := pgxpool.ParseConfig(dsn)
	if err != nil || cfg.ConnConfig.Host != "127.0.0.1" || !strings.HasPrefix(cfg.ConnConfig.Database, "bloem_managed_test") {
		t.Fatal("not fixture database")
	}
	pool, err := pgxpool.NewWithConfig(t.Context(), cfg)
	if err != nil {
		t.Fatal(err)
	}
	defer pool.Close()
	ctx := t.Context()
	id := uuid.NewString()
	title := "Synthetic inventory " + id
	var folder int
	if err = pool.QueryRow(ctx, `INSERT INTO media_folders(type,name,enabled) VALUES('ebooks','Synthetic inventory',true) RETURNING id`).Scan(&folder); err != nil {
		t.Fatal(err)
	}
	defer func() {
		_, _ = pool.Exec(context.Background(), `DELETE FROM media_files WHERE content_id=$1`, id)
		_, _ = pool.Exec(context.Background(), `DELETE FROM media_items WHERE content_id=$1`, id)
		_, _ = pool.Exec(context.Background(), `DELETE FROM media_folders WHERE id=$1`, folder)
	}()
	for _, q := range []struct {
		sql  string
		args []any
	}{
		{`INSERT INTO media_items(content_id,type,title,sort_title) VALUES($1,'ebook',$2,$2)`, []any{id, title}},
		{`INSERT INTO media_item_libraries(content_id,media_folder_id) VALUES($1,$2)`, []any{id, folder}},
		{`INSERT INTO media_item_provider_ids(content_id,item_type,provider,provider_id) VALUES($1,'ebook','isbn','9780000000002')`, []any{id}},
	} {
		if _, err = pool.Exec(ctx, q.sql, q.args...); err != nil {
			t.Fatal(err)
		}
	}
	scope := access.Scope{UserID: 42, ProfileID: "profile", ProfileVerified: true, PlaybackAllowed: true, AllowedLibraryIDs: []int{folder}}
	source := &managedCalendarSource{pool: pool, resolver: managedCalendarResolverFixture{scope: scope}}
	p := managedtracking.Profile{AccountID: "42", ProfileID: "profile", AccountActive: true, Active: true}
	q := managedtracking.LibraryQuery{View: "catalog", Kind: "ebook", Query: title, Limit: 1, SnapshotAt: time.Now()}
	assert := func(count int, available bool) {
		t.Helper()
		out, e := source.ReadLibrary(ctx, p, q)
		if e != nil || len(out.Items) != count {
			t.Fatal(out, e)
		}
		if count > 0 && (out.Items[0].ContentId != id || out.Items[0].Available != available || out.Items[0].ExternalIds["isbn"] != "9780000000002") {
			t.Fatal(out.Items[0])
		}
	}
	assert(1, false)
	if _, err = pool.Exec(ctx, `INSERT INTO media_files(content_id,media_folder_id,file_path) VALUES($1,$2,$3)`, id, folder, "/synthetic/"+id+".epub"); err != nil {
		t.Fatal(err)
	}
	assert(1, true)
	scope.PlaybackAllowed = false
	source.resolver = managedCalendarResolverFixture{scope: scope}
	assert(1, false)
	scope.AllowedLibraryIDs = []int{}
	source.resolver = managedCalendarResolverFixture{scope: scope}
	assert(0, false)
	scope.AllowedLibraryIDs = []int{folder}
	scope.DisabledLibraryIDs = []int{folder}
	source.resolver = managedCalendarResolverFixture{scope: scope}
	assert(0, false)
}
