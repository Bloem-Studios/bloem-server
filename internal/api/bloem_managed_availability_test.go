package api

import (
	"context"
	"os"
	"strings"
	"testing"

	"github.com/google/uuid"
	"github.com/jackc/pgx/v5/pgxpool"

	"github.com/Silo-Server/silo-server/internal/access"
	"github.com/Silo-Server/silo-server/internal/managedtracking"
	managedv1 "github.com/Silo-Server/silo-server/internal/managedtracking/wire"
)

func TestAvailabilityDeniedPlaybackDoesNotReturnMedia(t *testing.T) {
	s := &managedCalendarSource{resolver: managedCalendarResolverFixture{scope: access.Scope{UserID: 42, ProfileID: "profile", ProfileVerified: true, PlaybackAllowed: false}}}
	out, err := s.LookupAvailability(t.Context(), managedtracking.Profile{AccountID: "42", ProfileID: "profile", AccountActive: true, Active: true}, []*managedv1.AvailabilityTarget{{RequestId: "request", Kind: "movie", Namespace: "tmdb", ExternalId: "1"}})
	if err != nil || len(out) != 1 || out[0].Available || out[0].ContentId != "" {
		t.Fatalf("disabled playback media: %v %v", out, err)
	}
}

func TestAvailabilityUsesVisibleNonMissingFiles(t *testing.T) {
	dsn := os.Getenv("BLOEM_MANAGED_TEST_DATABASE_URL")
	if dsn == "" {
		t.Skip("explicit task-owned database required")
	}
	cfg, err := pgxpool.ParseConfig(dsn)
	if err != nil || cfg.ConnConfig.Host != "127.0.0.1" || !strings.HasPrefix(cfg.ConnConfig.Database, "bloem_managed_test") {
		t.Fatal("refusing nonfixture database")
	}
	pool, err := pgxpool.NewWithConfig(t.Context(), cfg)
	if err != nil {
		t.Fatal(err)
	}
	defer pool.Close()
	ctx := t.Context()
	id := uuid.NewString()
	external := uuid.NewString()
	var folder int
	if err = pool.QueryRow(ctx, `INSERT INTO media_folders(type,name,enabled) VALUES('movies','Synthetic availability',true) RETURNING id`).Scan(&folder); err != nil {
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
	}{{`INSERT INTO media_items(content_id,type,title,tmdb_id) VALUES($1,'movie','Synthetic film',$2)`, []any{id, external}}, {`INSERT INTO media_item_libraries(content_id,media_folder_id) VALUES($1,$2)`, []any{id, folder}}} {
		if _, err = pool.Exec(ctx, q.sql, q.args...); err != nil {
			t.Fatal(err)
		}
	}
	scope := access.Scope{UserID: 42, ProfileID: "profile", ProfileVerified: true, PlaybackAllowed: true, AllowedLibraryIDs: []int{folder}}
	s := &managedCalendarSource{pool: pool, resolver: managedCalendarResolverFixture{scope: scope}}
	p := managedtracking.Profile{AccountID: "42", ProfileID: "profile", AccountActive: true, Active: true}
	targets := []*managedv1.AvailabilityTarget{{RequestId: "request", Kind: "movie", Namespace: "tmdb", ExternalId: external}}
	assert := func(want bool) {
		t.Helper()
		out, e := s.LookupAvailability(ctx, p, targets)
		if e != nil || len(out) != 1 || out[0].Available != want {
			t.Fatalf("availability wanted %v: %v %v", want, out, e)
		}
	}
	assert(false)
	if _, err = pool.Exec(ctx, `INSERT INTO media_files(content_id,media_folder_id,file_path) VALUES($1,$2,$3)`, id, folder, "/synthetic/"+id+".mkv"); err != nil {
		t.Fatal(err)
	}
	assert(true)
	if _, err = pool.Exec(ctx, `UPDATE media_files SET missing_since=clock_timestamp() WHERE content_id=$1`, id); err != nil {
		t.Fatal(err)
	}
	assert(false)
	if _, err = pool.Exec(ctx, `UPDATE media_files SET missing_since=NULL WHERE content_id=$1`, id); err != nil {
		t.Fatal(err)
	}
	scope.AllowedLibraryIDs = []int{}
	s.resolver = managedCalendarResolverFixture{scope: scope}
	assert(false)
	scope.AllowedLibraryIDs = []int{folder}
	scope.DisabledLibraryIDs = []int{folder}
	s.resolver = managedCalendarResolverFixture{scope: scope}
	assert(false)
}
