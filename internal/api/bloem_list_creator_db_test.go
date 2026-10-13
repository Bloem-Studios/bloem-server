//go:build integration

package api

import (
	"testing"

	"github.com/google/uuid"

	"github.com/Silo-Server/silo-server/internal/catalog"
)

func TestListFollowServerCreationUsesNativeLibraryOrdering(t *testing.T) {
	pool := coverRecipeDatabase(t)
	ctx := t.Context()
	var folder int
	if err := pool.QueryRow(ctx, `INSERT INTO media_folders(type,name,enabled) VALUES('movies','Synthetic list creation',true) RETURNING id`).Scan(&folder); err != nil {
		t.Fatal(err)
	}
	ordinary, err := catalog.NewLibraryCollectionRepository(pool).Create(ctx, catalog.CreateLibraryCollectionInput{LibraryID: folder, Title: "Ordinary", Slug: uuid.NewString()})
	if err != nil {
		t.Fatal(err)
	}
	tx, err := pool.Begin(ctx)
	if err != nil {
		t.Fatal(err)
	}
	defer tx.Rollback(ctx)
	binding := uuid.NewString()
	id, revision, err := catalog.BloemCreateListCollectionTx(ctx, tx, "Linked server collection", []int{folder, folder}, binding)
	if err != nil || id == "" || revision < 1 {
		t.Fatalf("create: %s %d %v", id, revision, err)
	}
	var source, kind string
	var order, count int
	if err = tx.QueryRow(ctx, `SELECT collection_type,source_config->>'bloem_list_binding' FROM library_collections WHERE id=$1`, id).Scan(&kind, &source); err != nil {
		t.Fatal(err)
	}
	if kind != "manual" || source != binding {
		t.Fatalf("native representation: %s %s", kind, source)
	}
	if err = tx.QueryRow(ctx, `SELECT count(*),max(sort_order) FROM library_collection_libraries WHERE collection_id=$1`, id).Scan(&count, &order); err != nil {
		t.Fatal(err)
	}
	if count != 1 || order != 1 {
		t.Fatalf("native ordering: count=%d order=%d", count, order)
	}
	if err = tx.Rollback(ctx); err != nil {
		t.Fatal(err)
	}
	if err = pool.QueryRow(ctx, `SELECT count(*) FROM library_collections WHERE id=$1`, id).Scan(&count); err != nil || count != 0 {
		t.Fatalf("creation escaped caller rollback: %d %v", count, err)
	}
	if _, err = catalog.NewLibraryCollectionRepository(pool).GetByID(ctx, ordinary.ID); err != nil {
		t.Fatal("ordinary collection changed", err)
	}
}
