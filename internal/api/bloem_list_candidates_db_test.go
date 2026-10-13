//go:build integration

package api

import (
	"strconv"
	"testing"

	"github.com/google/uuid"

	"github.com/Silo-Server/silo-server/internal/access"
	"github.com/Silo-Server/silo-server/internal/catalog"
	"github.com/Silo-Server/silo-server/internal/listcollections"
)

func TestListFollowCandidatesUseNativeCatalogPolicy(t *testing.T) {
	pool := coverRecipeDatabase(t)
	ctx := t.Context()
	var allowed, outside int
	for _, dest := range []*int{&allowed, &outside} {
		if err := pool.QueryRow(ctx, `INSERT INTO media_folders(type,name,enabled) VALUES('movies','Synthetic candidate library',true) RETURNING id`).Scan(dest); err != nil {
			t.Fatal(err)
		}
	}
	wanted := []listcollections.Entry{}
	visible := uuid.NewString()
	for i, id := range []string{visible, uuid.NewString(), uuid.NewString()} {
		rating, folder := "PG", allowed
		if i == 1 {
			rating = "R"
		}
		if i == 2 {
			folder = outside
		}
		identity := strconv.Itoa(42 + i)
		if _, err := pool.Exec(ctx, `INSERT INTO media_items(content_id,type,title,tmdb_id,content_rating,content_rating_age) VALUES($1,'movie','Synthetic identity',$2,$3,$4)`, id, identity, rating, []int{8, 18, 8}[i]); err != nil {
			t.Fatal(err)
		}
		if _, err := pool.Exec(ctx, `INSERT INTO media_item_libraries(content_id,media_folder_id) VALUES($1,$2)`, id, folder); err != nil {
			t.Fatal(err)
		}
		wanted = append(wanted, listcollections.Entry{Kind: "movie", Title: "Synthetic identity", ExternalIDs: map[string]string{"tmdb": identity}})
	}
	tx, err := pool.Begin(ctx)
	if err != nil {
		t.Fatal(err)
	}
	defer tx.Rollback(ctx)
	rows, err := catalog.BloemListCandidatesTx(ctx, tx, catalog.BrowseFilters{LibraryIDs: []int{allowed}, MaturityLimits: access.MaturityLimits{MaxContentRating: "PG"}}, wanted)
	if err != nil || len(rows) != 1 || rows[0].ContentID != visible {
		t.Fatalf("native catalog filtering: %v %v", rows, err)
	}
	matched, err := listcollections.MatchEntries(wanted, rows)
	if err != nil || len(matched.ContentIDs) != 1 || len(matched.Unmatched) != 2 {
		t.Fatalf("policy matching: %v %v", matched, err)
	}
	rows, err = catalog.BloemListCandidatesTx(ctx, tx, catalog.BrowseFilters{LibraryIDs: []int{}}, wanted)
	if err != nil || len(rows) != 0 {
		t.Fatalf("empty allowlist widened: %v %v", rows, err)
	}
}
