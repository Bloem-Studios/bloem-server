package catalog

import (
	"context"
	"fmt"
	"os"
	"slices"
	"strings"
	"testing"
	"time"

	"github.com/jackc/pgx/v5/pgxpool"
)

func TestListContainingItemDB(t *testing.T) {
	dsn := os.Getenv("SILO_TEST_DATABASE_URL")
	if dsn == "" {
		t.Skip("SILO_TEST_DATABASE_URL is not set")
	}
	ctx := t.Context()
	pool, err := pgxpool.New(ctx, dsn)
	if err != nil {
		t.Fatal(err)
	}
	defer pool.Close()

	prefix := fmt.Sprintf("item-collections-%d", time.Now().UnixNano())
	newFolder := func(name string, enabled bool, sortOrder int) int {
		t.Helper()
		var id int
		if err := pool.QueryRow(ctx, `INSERT INTO media_folders(type,name,enabled,sort_order) VALUES('movies',$1,$2,$3) RETURNING id`,
			prefix+"-"+name, enabled, sortOrder).Scan(&id); err != nil {
			t.Fatal(err)
		}
		return id
	}
	libA := newFolder("a", true, 1)
	libB := newFolder("b", true, 2)
	libOff := newFolder("off", false, 3)
	defer func() { deleteCatalogTestMediaFolders(t, context.Background(), pool, libA, libB, libOff) }()

	item := prefix + "-item"
	other := prefix + "-other"
	for _, id := range []string{item, other} {
		if _, err := pool.Exec(ctx, `INSERT INTO media_items(content_id,type,title,status,genres) VALUES($1,'movie',$1,'released','{}')`, id); err != nil {
			t.Fatal(err)
		}
	}
	defer func() {
		_, _ = pool.Exec(context.Background(), `DELETE FROM media_items WHERE content_id LIKE $1`, prefix+"%")
	}()
	// The item lives in B only, so a collection scoped to both A and B must be
	// reported through B.
	if _, err := pool.Exec(ctx, `INSERT INTO media_item_libraries(content_id,media_folder_id) VALUES($1,$2),($3,$4)`, item, libB, other, libA); err != nil {
		t.Fatal(err)
	}

	repo := NewLibraryCollectionRepository(pool)
	var created []string
	defer func() {
		for _, id := range created {
			_ = repo.Delete(context.Background(), id)
			_, _ = pool.Exec(context.Background(), `DELETE FROM library_collection_revisions WHERE collection_id=$1`, id)
		}
	}()
	newCollection := func(slug string, libs []int, typ, visibility string, members ...string) string {
		t.Helper()
		c, err := repo.Create(ctx, CreateLibraryCollectionInput{LibraryID: libs[0], LibraryIDs: libs, Slug: prefix + "-" + slug,
			Title: slug, CollectionType: typ, Visibility: visibility, PosterURL: "https://img.example/" + slug + ".jpg"})
		if err != nil {
			t.Fatal(err)
		}
		created = append(created, c.ID)
		for _, m := range members {
			// Direct insert: smart collections never materialize members, and
			// the stale row is exactly what the query must ignore.
			if _, err := pool.Exec(ctx, `INSERT INTO library_collection_items(collection_id,media_item_id) VALUES($1,$2)`, c.ID, m); err != nil {
				t.Fatal(err)
			}
		}
		return c.ID
	}
	inA := newCollection("in-a", []int{libA}, "manual", "visible", item, other)
	inB := newCollection("in-b", []int{libB}, "tmdb", "visible", item)
	inBoth := newCollection("in-both", []int{libA, libB}, "manual", "visible", item)
	// Each of these contains the item (or not) in a way the query must reject.
	newCollection("hidden", []int{libA}, "manual", "hidden", item)
	newCollection("disabled-lib", []int{libOff}, "manual", "visible", item)
	newCollection("smart", []int{libA}, "smart", "visible", item)
	newCollection("not-member", []int{libA}, "manual", "visible", other)

	groupID := prefix + "-group"
	if _, err := pool.Exec(ctx, `INSERT INTO library_collection_groups(library_id,label,title,id,name,slug) VALUES($1,$2,$2,$3,'Franchises',$2)`, libA, prefix+"-g", groupID); err != nil {
		t.Fatal(err)
	}
	if _, err := pool.Exec(ctx, `UPDATE library_collection_libraries SET group_id=$1 WHERE collection_id=$2 AND library_id=$3`, groupID, inA, libA); err != nil {
		t.Fatal(err)
	}

	ids := func(ms []ItemCollectionMembership) []string {
		out := make([]string, 0, len(ms))
		for _, m := range ms {
			out = append(out, m.ID)
		}
		slices.Sort(out)
		return out
	}
	sorted := func(v ...string) []string { slices.Sort(v); return v }

	t.Run("unrestricted viewer sees every visible materialized collection once", func(t *testing.T) {
		got, err := repo.ListContainingItem(ctx, item, AccessFilter{})
		if err != nil {
			t.Fatal(err)
		}
		if want := sorted(inA, inB, inBoth); !slices.Equal(ids(got), want) {
			t.Fatalf("got %v, want %v", ids(got), want)
		}
		for _, m := range got {
			switch m.ID {
			case inA:
				if m.LibraryID != libA || m.ItemCount != 2 || m.GroupID == nil || *m.GroupID != groupID || m.GroupName != "Franchises" {
					t.Fatalf("in-a: %+v", m)
				}
				if m.PosterURL == "" || m.CollectionType != "manual" {
					t.Fatalf("in-a artwork/type: %+v", m)
				}
			case inBoth:
				if m.LibraryID != libB {
					t.Fatalf("in-both should resolve through the item's own library %d: %+v", libB, m)
				}
			case inB:
				if m.CollectionType != "tmdb" || m.ItemCount != 1 {
					t.Fatalf("in-b: %+v", m)
				}
			}
		}
	})

	t.Run("allowlist excludes collections only in libraries the profile cannot see", func(t *testing.T) {
		got, err := repo.ListContainingItem(ctx, item, AccessFilter{AllowedLibraryIDs: []int{libA}})
		if err != nil {
			t.Fatal(err)
		}
		if want := sorted(inA, inBoth); !slices.Equal(ids(got), want) {
			t.Fatalf("got %v, want %v", ids(got), want)
		}
		for _, m := range got {
			if m.LibraryID != libA {
				t.Fatalf("collection %s reported through inaccessible library %d", m.ID, m.LibraryID)
			}
		}
	})

	t.Run("disabled library is excluded for an unrestricted profile", func(t *testing.T) {
		got, err := repo.ListContainingItem(ctx, item, AccessFilter{DisabledLibraryIDs: []int{libB}})
		if err != nil {
			t.Fatal(err)
		}
		if want := sorted(inA, inBoth); !slices.Equal(ids(got), want) {
			t.Fatalf("got %v, want %v", ids(got), want)
		}
		for _, m := range got {
			if m.LibraryID == libB {
				t.Fatalf("collection %s reported through disabled library", m.ID)
			}
		}
	})

	t.Run("empty allowlist sees nothing", func(t *testing.T) {
		got, err := repo.ListContainingItem(ctx, item, AccessFilter{AllowedLibraryIDs: []int{}})
		if err != nil {
			t.Fatal(err)
		}
		if len(got) != 0 {
			t.Fatalf("got %v", ids(got))
		}
	})

	t.Run("unknown item yields an empty non-nil list", func(t *testing.T) {
		got, err := repo.ListContainingItem(ctx, prefix+"-missing", AccessFilter{})
		if err != nil {
			t.Fatal(err)
		}
		if got == nil || len(got) != 0 {
			t.Fatalf("got %#v", got)
		}
	})
}

func TestBuildListContainingItemSQLBindsAccessLists(t *testing.T) {
	t.Parallel()
	_, args := buildListContainingItemSQL("movie:x", AccessFilter{})
	if len(args) != 2 {
		t.Fatalf("unrestricted args = %v", args)
	}
	q, args := buildListContainingItemSQL("movie:x", AccessFilter{AllowedLibraryIDs: []int{1}, DisabledLibraryIDs: []int{2}})
	if len(args) != 4 || args[3] != ItemCollectionsLimit {
		t.Fatalf("restricted args = %v", args)
	}
	for _, want := range []string{"lcl.library_id = ANY($2)", "NOT (lcl.library_id = ANY($3))", "LIMIT $4", "lc.collection_type <> 'smart'", "lc.visibility = 'visible'", "mf.enabled"} {
		if !strings.Contains(q, want) {
			t.Errorf("query lacks %q", want)
		}
	}
}
