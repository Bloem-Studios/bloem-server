//go:build integration

package api

import (
	"strconv"
	"testing"

	"github.com/google/uuid"

	"github.com/Silo-Server/silo-server/internal/access"
	"github.com/Silo-Server/silo-server/internal/managedtracking"
	library "github.com/Silo-Server/silo-server/internal/managedtracking/librarywire"
	"github.com/Silo-Server/silo-server/internal/userstore/pgstore"
)

func TestManagedCopyOwnershipIsExplicitAndProfilePrivate(t *testing.T) {
	pool := coverRecipeDatabase(t)
	ctx := t.Context()
	var user int
	if err := pool.QueryRow(ctx, `INSERT INTO users(username,password_hash,role,enabled) VALUES($1,'synthetic','user',true) RETURNING id`, "copies-"+uuid.NewString()).Scan(&user); err != nil {
		t.Fatal(err)
	}
	source := &managedCalendarSource{pool: pool, stores: pgstore.NewPostgresProvider(pool), resolver: managedCalendarResolverFixture{scope: access.Scope{UserID: user, ProfileID: "owner", ProfileVerified: true}}}
	p := managedtracking.Profile{AccountID: strconv.Itoa(user), ProfileID: "owner", AccountActive: true, Active: true}
	tx, err := pool.Begin(ctx)
	if err != nil {
		t.Fatal(err)
	}
	defer tx.Rollback(ctx)
	in := &library.LibraryMutation{Operation: "copy.upsert", EventId: "own-1", TargetId: "pastime:1", Copy: &library.LibraryCopy{Id: "pastime:1", Kind: "movie", Title: "A film I own", Format: "bluray", Edition: "First pressing", ExternalIds: map[string]string{"tmdb": "42"}, PriceCents: 2300, Currency: "EUR", Notes: "Personal note", Custom: map[string]string{"signed": "true"}}}
	out, err := source.MutateLibrary(ctx, p, tx, in)
	if err != nil || out.Revision != 1 {
		t.Fatal(out, err)
	}
	if err = tx.Commit(ctx); err != nil {
		t.Fatal(err)
	}
	data, err := source.ReadLibrary(ctx, p, managedtracking.LibraryQuery{View: "copies", Limit: 20})
	if err != nil || len(data.Copies) != 1 || data.Copies[0].Edition != "First pressing" || data.Copies[0].Notes != "Personal note" {
		t.Fatal(data, err)
	}
	source.resolver = managedCalendarResolverFixture{scope: access.Scope{UserID: user, ProfileID: "other", ProfileVerified: true}}
	p.ProfileID = "other"
	data, err = source.ReadLibrary(ctx, p, managedtracking.LibraryQuery{View: "copies", Limit: 20})
	if err != nil || len(data.Copies) != 0 {
		t.Fatal("ownership leaked", data, err)
	}
}
