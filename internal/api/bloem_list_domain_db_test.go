//go:build integration

package api

import (
	"errors"
	"testing"

	"github.com/google/uuid"

	"github.com/Silo-Server/silo-server/internal/access"
	"github.com/Silo-Server/silo-server/internal/auth"
	"github.com/Silo-Server/silo-server/internal/listcollections"
	"github.com/Silo-Server/silo-server/internal/managedtracking"
	"github.com/Silo-Server/silo-server/internal/models"
	"github.com/Silo-Server/silo-server/internal/secret"
	"github.com/Silo-Server/silo-server/internal/userstore"
	"github.com/Silo-Server/silo-server/internal/userstore/pgstore"
)

func listNativeDomainFixture(t *testing.T) (*listCollectionDomain, listcollections.Subject, int) {
	t.Helper()
	pool, ctx := coverRecipeDatabase(t), t.Context()
	tx, err := pool.Begin(ctx)
	if err != nil {
		t.Fatal(err)
	}
	defer tx.Rollback(ctx)
	u, err := auth.NewUserRepository(pool).CreateInTransaction(ctx, tx, models.CreateUserInput{Username: uuid.NewString(), Password: "synthetic-only", Role: "admin"})
	if err != nil {
		t.Fatal(err)
	}
	var tenant string
	if err = tx.QueryRow(ctx, `SELECT id::text FROM organizations WHERE is_default`).Scan(&tenant); err != nil {
		t.Fatal(err)
	}
	profile := uuid.NewString()
	stores := pgstore.NewPostgresProvider(pool)
	if err = stores.CreateProfileInTransaction(ctx, tx, u.ID, userstore.Profile{ID: profile, Name: "List owner", IsPrimary: true, OrganizationID: tenant}); err != nil {
		t.Fatal(err)
	}
	var installation, folder int
	if err = tx.QueryRow(ctx, `INSERT INTO plugin_installations(plugin_id,version,install_path,enabled) VALUES('bloem.pastime','fixture','synthetic',true) RETURNING id`).Scan(&installation); err != nil {
		t.Fatal(err)
	}
	if err = tx.QueryRow(ctx, `INSERT INTO media_folders(type,name,enabled) VALUES('movies','Synthetic linked library',true) RETURNING id`).Scan(&folder); err != nil {
		t.Fatal(err)
	}
	if err = tx.Commit(ctx); err != nil {
		t.Fatal(err)
	}
	cipher, err := secret.New([]byte("synthetic-list-key-32-bytes-long!!"))
	if err != nil {
		t.Fatal(err)
	}
	managed := &managedtracking.Service{Pool: pool, Cipher: cipher}
	if err = managed.Grant(ctx, installation, tenant); err != nil {
		t.Fatal(err)
	}
	info, err := managed.GetInfo(ctx, installation)
	if err != nil {
		t.Fatal(err)
	}
	page, err := managed.ListProfiles(ctx, installation, "", "", 100)
	if err != nil {
		t.Fatal(err)
	}
	var p managedtracking.Profile
	for _, row := range page.Profiles {
		if row.ProfileID == profile {
			p = row
		}
	}
	if p.ProfileID == "" {
		t.Fatal("profile absent")
	}
	e := managedtracking.Enrollment{Profile: p, ProviderID: "pastime", DestinationAccountID: "10", DestinationProfileID: "20", Credential: "ptbs_fixture", Active: true, Generation: info.Generation}
	e.ID = managedtracking.EnrollmentID(info.InstanceID, e)
	if err = managed.EnsureConnection(ctx, installation, e); err != nil {
		t.Fatal(err)
	}
	scope := access.Scope{UserID: u.ID, ProfileID: profile, ProfileVerified: true, AllowedLibraryIDs: []int{folder}}
	return &listCollectionDomain{managed: managed, source: &managedCalendarSource{pool: pool, stores: stores, resolver: managedCalendarResolverFixture{scope: scope}}}, listcollections.Subject{UserID: u.ID, ProfileID: profile, InstallationID: installation, InstanceID: info.InstanceID, IsAdmin: true}, folder
}

func TestListFollowNativeDomainAudiencesAndAuthority(t *testing.T) {
	d, s, folder := listNativeDomainFixture(t)
	ctx, pool := t.Context(), d.source.pool
	for _, scope := range []string{"personal", "shared", "server"} {
		t.Run(scope, func(t *testing.T) {
			tx, err := pool.Begin(ctx)
			if err != nil {
				t.Fatal(err)
			}
			defer tx.Rollback(ctx)
			if err = d.AuthorizeTx(ctx, tx, s); err != nil {
				t.Fatal(err)
			}
			in := listcollections.CreateInput{Scope: "personal", Name: "Linked " + scope, IsShared: scope == "shared"}
			if scope == "server" {
				in.Scope = "server"
				in.LibraryIDs = []int{folder}
			}
			binding := uuid.NewString()
			id, revision, err := d.CreateTx(ctx, tx, s, in, binding)
			if err != nil || revision < 1 {
				t.Fatalf("create %d %v", revision, err)
			}
			if _, err = d.ReplaceTx(ctx, tx, s, listcollections.Binding{ID: binding, TargetID: id, Scope: in.Scope, Subject: s, LibraryIDs: in.LibraryIDs}, []string{}); err != nil {
				t.Fatal(err)
			}
			if err = d.DisconnectTx(ctx, tx, s, listcollections.Binding{ID: binding, TargetID: id, Scope: in.Scope, Subject: s}); err != nil {
				t.Fatal(err)
			}
		})
	}
	for _, change := range []string{"forged-admin", "wrong-profile", "disabled-source"} {
		t.Run(change, func(t *testing.T) {
			candidate := s
			switch change {
			case "forged-admin":
				candidate.IsAdmin = false
			case "wrong-profile":
				candidate.ProfileID = "wrong"
			case "disabled-source":
				if _, err := pool.Exec(ctx, `UPDATE plugin_installations SET enabled=false WHERE id=$1`, s.InstallationID); err != nil {
					t.Fatal(err)
				}
			}
			tx, err := pool.Begin(ctx)
			if err != nil {
				t.Fatal(err)
			}
			defer tx.Rollback(ctx)
			if err = d.AuthorizeTx(ctx, tx, candidate); !errors.Is(err, listcollections.ErrAuthority) {
				t.Fatalf("stale/forged authority accepted: %v", err)
			}
		})
	}
}

func TestListFollowNativePublicationRechecksCurrentCatalogPolicy(t *testing.T) {
	d, s, folder := listNativeDomainFixture(t)
	ctx, pool := t.Context(), d.source.pool
	id := uuid.NewString()
	if _, err := pool.Exec(ctx, `INSERT INTO media_items(content_id,type,title) VALUES($1,'movie','Synthetic policy change')`, id); err != nil {
		t.Fatal(err)
	}
	if _, err := pool.Exec(ctx, `INSERT INTO media_item_libraries(content_id,media_folder_id) VALUES($1,$2)`, id, folder); err != nil {
		t.Fatal(err)
	}
	tx, err := pool.Begin(ctx)
	if err != nil {
		t.Fatal(err)
	}
	defer tx.Rollback(ctx)
	link := uuid.NewString()
	target, _, err := d.CreateTx(ctx, tx, s, listcollections.CreateInput{Scope: "personal", Name: "Policy keeper"}, link)
	if err != nil {
		t.Fatal(err)
	}
	b := listcollections.Binding{ID: link, TargetID: target, Scope: "personal", Subject: s}
	if _, err = d.ReplaceTx(ctx, tx, s, b, []string{id}); err != nil {
		t.Fatal(err)
	}
	resolver := d.source.resolver.(managedCalendarResolverFixture)
	resolver.scope.AllowedLibraryIDs = []int{}
	d.source.resolver = resolver
	if _, err = d.ReplaceTx(ctx, tx, s, b, []string{id}); !errors.Is(err, listcollections.ErrAuthority) {
		t.Fatalf("publication ignored changed library policy: %v", err)
	}
}
