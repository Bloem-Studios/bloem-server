//go:build integration

package api

import (
	"encoding/json"
	"slices"
	"testing"

	"github.com/google/uuid"
	"github.com/jackc/pgx/v5"

	"github.com/Silo-Server/silo-server/internal/catalog"
	"github.com/Silo-Server/silo-server/internal/userstore"
	"github.com/Silo-Server/silo-server/internal/userstore/pgstore"
)

// Publication must retain the caller's transaction: a rejected receipt must
// roll back membership and its native continuation witness together.
func TestListFollowMembershipTransaction(t *testing.T) {
	pool := coverRecipeDatabase(t)
	ctx := t.Context()
	var user, folder int
	if err := pool.QueryRow(ctx, `INSERT INTO users(username,password_hash,role,enabled) VALUES($1,'synthetic','user',true) RETURNING id`, uuid.NewString()).Scan(&user); err != nil {
		t.Fatal(err)
	}
	if err := pool.QueryRow(ctx, `INSERT INTO media_folders(type,name,enabled) VALUES('movies','Synthetic list',true) RETURNING id`).Scan(&folder); err != nil {
		t.Fatal(err)
	}
	ids := []string{uuid.NewString(), uuid.NewString()}
	outside := uuid.NewString()
	if _, err := pool.Exec(ctx, `INSERT INTO media_items(content_id,type,title) VALUES($1,'movie','Outside selected libraries')`, outside); err != nil {
		t.Fatal(err)
	}
	for _, id := range ids {
		if _, err := pool.Exec(ctx, `INSERT INTO media_items(content_id,type,title) VALUES($1,'movie','Synthetic title')`, id); err != nil {
			t.Fatal(err)
		}
		if _, err := pool.Exec(ctx, `INSERT INTO media_item_libraries(content_id,media_folder_id) VALUES($1,$2)`, id, folder); err != nil {
			t.Fatal(err)
		}
	}
	store, err := pgstore.NewPostgresProvider(pool).ForUser(ctx, user)
	if err != nil {
		t.Fatal(err)
	}
	binding := uuid.NewString()
	config, _ := json.Marshal(map[string]string{"bloem_list_binding": binding})
	personal, err := store.CreateCollection(ctx, userstore.CreateCollectionInput{CreatorProfileID: "owner", Name: "Personal", CollectionType: "synced", IsShared: true, SourceConfig: string(config)})
	if err != nil {
		t.Fatal(err)
	}
	server, err := catalog.NewLibraryCollectionRepository(pool).Create(ctx, catalog.CreateLibraryCollectionInput{LibraryID: folder, Title: "Server", Slug: uuid.NewString(), CollectionType: "manual", SourceConfig: config})
	if err != nil {
		t.Fatal(err)
	}
	for _, scope := range []string{"personal", "server"} {
		t.Run(scope, func(t *testing.T) {
			id := personal.ID
			if scope == "server" {
				id = server.ID
			}
			replace := func(tx pgx.Tx, owner, link string, items []string) (int64, error) {
				if scope == "personal" {
					return pgstore.BloemReplaceListCollectionItemsTx(ctx, tx, user, owner, id, link, items)
				}
				return catalog.BloemReplaceListCollectionItemsTx(ctx, tx, id, link, items)
			}
			read := func() ([]string, int64) {
				var query, revQuery string
				if scope == "personal" {
					query = `SELECT media_item_id FROM user_personal_collection_items WHERE collection_id=$1 ORDER BY position`
					revQuery = `SELECT revision FROM user_collection_revisions WHERE collection_id=$1`
				} else {
					query = `SELECT media_item_id FROM library_collection_items WHERE collection_id=$1 ORDER BY position`
					revQuery = `SELECT revision FROM library_collection_revisions WHERE collection_id=$1`
				}
				rows, e := pool.Query(ctx, query, id)
				if e != nil {
					t.Fatal(e)
				}
				got, e := pgx.CollectRows(rows, pgx.RowTo[string])
				if e != nil {
					t.Fatal(e)
				}
				var rev int64
				if e = pool.QueryRow(ctx, revQuery, id).Scan(&rev); e != nil {
					t.Fatal(e)
				}
				return got, rev
			}
			_, before := read()
			tx, e := pool.Begin(ctx)
			if e != nil {
				t.Fatal(e)
			}
			rev, e := replace(tx, "owner", binding, []string{ids[1], ids[0], ids[1]})
			if e != nil {
				tx.Rollback(ctx)
				t.Fatal(e)
			}
			if rev <= before {
				t.Fatal("native revision not advanced")
			}
			if e = tx.Rollback(ctx); e != nil {
				t.Fatal(e)
			}
			got, after := read()
			if len(got) != 0 || after != before {
				t.Fatal("rollback leaked membership/revision", got, after, before)
			}
			tx, e = pool.Begin(ctx)
			if e != nil {
				t.Fatal(e)
			}
			if _, e = replace(tx, "owner", binding, []string{ids[1], ids[0], ids[1]}); e != nil {
				tx.Rollback(ctx)
				t.Fatal(e)
			}
			if e = tx.Commit(ctx); e != nil {
				t.Fatal(e)
			}
			got, _ = read()
			if !slices.Equal(got, []string{ids[1], ids[0]}) {
				t.Fatal("source ordering/deduplication", got)
			}
			tx, e = pool.Begin(ctx)
			if e != nil {
				t.Fatal(e)
			}
			if _, e = replace(tx, "owner", binding, []string{uuid.NewString()}); e == nil {
				tx.Rollback(ctx)
				t.Fatal("missing catalog identity became membership")
			}
			tx.Rollback(ctx)
			got, _ = read()
			if !slices.Equal(got, []string{ids[1], ids[0]}) {
				t.Fatal("rejected publication changed last good membership", got)
			}
			if scope == "server" {
				tx, e = pool.Begin(ctx)
				if e != nil {
					t.Fatal(e)
				}
				if _, e = replace(tx, "owner", binding, []string{outside}); e == nil {
					tx.Rollback(ctx)
					t.Fatal("catalog row outside selected libraries became membership")
				}
				tx.Rollback(ctx)
			} else {
				tx, e = pool.Begin(ctx)
				if e != nil {
					t.Fatal(e)
				}
				if _, e = tx.Exec(ctx, `UPDATE user_personal_collections SET collection_type='manual' WHERE user_id=$1 AND id=$2`, user, id); e != nil {
					tx.Rollback(ctx)
					t.Fatal(e)
				}
				if _, e = replace(tx, "owner", binding, nil); e == nil {
					tx.Rollback(ctx)
					t.Fatal("ordinary manual collection converted implicitly")
				}
				tx.Rollback(ctx)
			}
			tx, e = pool.Begin(ctx)
			if e != nil {
				t.Fatal(e)
			}
			if _, e = replace(tx, "owner", "wrong-binding", nil); e == nil {
				t.Fatal("different source replaced membership")
			}
			tx.Rollback(ctx)
			if scope == "personal" {
				tx, e = pool.Begin(ctx)
				if e != nil {
					t.Fatal(e)
				}
				if _, e = replace(tx, "viewer", binding, nil); e == nil {
					t.Fatal("shared viewer replaced membership")
				}
				tx.Rollback(ctx)
			}
			tx, e = pool.Begin(ctx)
			if e != nil {
				t.Fatal(e)
			}
			if _, e = replace(tx, "owner", binding, nil); e != nil {
				tx.Rollback(ctx)
				t.Fatal(e)
			}
			if e = tx.Commit(ctx); e != nil {
				t.Fatal(e)
			}
			got, _ = read()
			if len(got) != 0 {
				t.Fatal("valid empty membership did not clear", got)
			}
		})
	}
}
