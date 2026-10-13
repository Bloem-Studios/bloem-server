//go:build integration

package api

import (
	"errors"
	"strconv"
	"testing"

	"github.com/google/uuid"

	"github.com/Silo-Server/silo-server/internal/access"
	"github.com/Silo-Server/silo-server/internal/catalog"
	"github.com/Silo-Server/silo-server/internal/managedtracking"
	library "github.com/Silo-Server/silo-server/internal/managedtracking/librarywire"
	"github.com/Silo-Server/silo-server/internal/userstore"
	"github.com/Silo-Server/silo-server/internal/userstore/pgstore"
)

func TestManagedLibraryCollectionScopes(t *testing.T) {
	pool := coverRecipeDatabase(t)
	ctx := t.Context()
	var user int
	if err := pool.QueryRow(ctx, `INSERT INTO users(username,password_hash,role,enabled) VALUES($1,'synthetic','user',true) RETURNING id`, "collections-"+uuid.NewString()).Scan(&user); err != nil {
		t.Fatal(err)
	}
	stores := pgstore.NewPostgresProvider(pool)
	store, err := stores.ForUser(ctx, user)
	if err != nil {
		t.Fatal(err)
	}
	mine, err := store.CreateCollection(ctx, userstore.CreateCollectionInput{CreatorProfileID: "viewer", Name: "Mine"})
	if err != nil {
		t.Fatal(err)
	}
	shared, err := store.CreateCollection(ctx, userstore.CreateCollectionInput{CreatorProfileID: "owner", Name: "Shared", IsShared: true})
	if err != nil {
		t.Fatal(err)
	}
	private, err := store.CreateCollection(ctx, userstore.CreateCollectionInput{CreatorProfileID: "owner", Name: "Private"})
	if err != nil {
		t.Fatal(err)
	}
	var folder int
	if err = pool.QueryRow(ctx, `INSERT INTO media_folders(type,name,enabled) VALUES('movies','Synthetic collections',true) RETURNING id`).Scan(&folder); err != nil {
		t.Fatal(err)
	}
	server, err := catalog.NewLibraryCollectionRepository(pool).Create(ctx, catalog.CreateLibraryCollectionInput{LibraryID: folder, Title: "Server curated", Slug: "server-" + uuid.NewString(), CollectionType: "manual"})
	if err != nil {
		t.Fatal(err)
	}
	scope := access.Scope{UserID: user, ProfileID: "viewer", ProfileVerified: true}
	source := &managedCalendarSource{pool: pool, stores: stores, resolver: managedCalendarResolverFixture{scope: scope}}
	p := managedtracking.Profile{AccountID: strconv.Itoa(user), ProfileID: "viewer", AccountActive: true, Active: true}
	out, err := source.ReadLibrary(ctx, p, managedtracking.LibraryQuery{View: "collections", Limit: 20})
	if err != nil {
		t.Fatal(err)
	}
	found := map[string]bool{}
	for _, c := range out.Collections {
		found[c.Id] = true
		switch c.Id {
		case "personal:" + mine.ID:
			if c.Scope != "personal" || !c.Writable {
				t.Fatal(c)
			}
		case "personal:" + shared.ID:
			if c.Scope != "shared" || c.Writable {
				t.Fatal(c)
			}
		case "server:" + server.ID:
			if c.Scope != "server" || c.Writable {
				t.Fatal(c)
			}
		}
	}
	if len(found) != 3 || found["personal:"+private.ID] {
		t.Fatal("collection scopes", found)
	}
	members, err := source.ReadLibrary(ctx, p, managedtracking.LibraryQuery{View: "collection_items", ParentID: "personal:" + mine.ID, Limit: 20})
	if err != nil || len(members.Collections) != 1 || !members.Collections[0].Writable || members.Revision == 0 {
		t.Fatal("parent collection metadata", members, err)
	}

}

func TestManagedCollectionMutationUsesNativeOwnershipAndRevision(t *testing.T) {
	pool := coverRecipeDatabase(t)
	ctx := t.Context()
	var user int
	if err := pool.QueryRow(ctx, `INSERT INTO users(username,password_hash,role,enabled) VALUES($1,'synthetic','user',true) RETURNING id`, "collection-write-"+uuid.NewString()).Scan(&user); err != nil {
		t.Fatal(err)
	}
	stores := pgstore.NewPostgresProvider(pool)
	source := &managedCalendarSource{pool: pool, stores: stores, resolver: managedCalendarResolverFixture{scope: access.Scope{UserID: user, ProfileID: "viewer", ProfileVerified: true}}}
	p := managedtracking.Profile{AccountID: strconv.Itoa(user), ProfileID: "viewer", AccountActive: true, Active: true}
	tx, err := pool.Begin(ctx)
	if err != nil {
		t.Fatal(err)
	}
	defer tx.Rollback(ctx)
	in := &library.LibraryMutation{Operation: "collection.create", EventId: "create", Name: "Native bridge collection"}
	out, err := source.MutateLibrary(ctx, p, tx, in)
	if err != nil {
		t.Fatal(err)
	}
	if err = tx.Commit(ctx); err != nil {
		t.Fatal(err)
	}
	list, err := source.ReadLibrary(ctx, p, managedtracking.LibraryQuery{View: "collections", Limit: 20})
	if err != nil || len(list.Collections) != 1 || list.Collections[0].Id != out.TargetId {
		t.Fatal(list, err)
	}
	tx, err = pool.Begin(ctx)
	if err != nil {
		t.Fatal(err)
	}
	defer tx.Rollback(ctx)
	in = &library.LibraryMutation{Operation: "collection.update", TargetId: out.TargetId, EventId: "rename", Name: "New name", ExpectedRevision: out.Revision + 1}
	if _, err = source.MutateLibrary(ctx, p, tx, in); !errors.Is(err, managedtracking.ErrConflict) {
		t.Fatal("stale revision accepted", err)
	}
	if err = tx.Rollback(ctx); err != nil {
		t.Fatal(err)
	}
	tx, err = pool.Begin(ctx)
	if err != nil {
		t.Fatal(err)
	}
	defer tx.Rollback(ctx)
	in.ExpectedRevision = out.Revision
	changed, err := source.MutateLibrary(ctx, p, tx, in)
	if err != nil || changed.Revision <= out.Revision {
		t.Fatal("valid rename rejected", changed, err)
	}
	if err = tx.Commit(ctx); err != nil {
		t.Fatal(err)
	}
	list, err = source.ReadLibrary(ctx, p, managedtracking.LibraryQuery{View: "collections", Limit: 20})
	if err != nil || len(list.Collections) != 1 || list.Collections[0].Name != "New name" {
		t.Fatal("rename not reflected", list, err)
	}
	store, _ := stores.ForUser(ctx, user)
	shared, err := store.CreateCollection(ctx, userstore.CreateCollectionInput{CreatorProfileID: "owner", Name: "Other owner's list", IsShared: true})
	if err != nil {
		t.Fatal(err)
	}
	pager := store.(userstore.CollectionItemsPager)
	revision, err := pager.CollectionRevision(ctx, shared.ID)
	if err != nil {
		t.Fatal(err)
	}
	tx, err = pool.Begin(ctx)
	if err != nil {
		t.Fatal(err)
	}
	defer tx.Rollback(ctx)
	in.TargetId = "personal:" + shared.ID
	in.ExpectedRevision = uint64(revision)
	if _, err = source.MutateLibrary(ctx, p, tx, in); !errors.Is(err, managedtracking.ErrAuthority) {
		t.Fatal("shared viewer became editor", err)
	}
}
