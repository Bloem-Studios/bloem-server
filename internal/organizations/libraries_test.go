package organizations_test

import (
	"slices"
	"testing"

	"github.com/Silo-Server/silo-server/internal/database"
	"github.com/Silo-Server/silo-server/internal/organizations"
	"github.com/Silo-Server/silo-server/migrations"
)

func TestOrganizationLibraryBoundary(t *testing.T) {
	pool := ownershipDatabase(t)
	ctx := t.Context()
	if err := database.RunMigrations(ctx, pool, migrations.FS, "sql"); err != nil {
		t.Fatal(err)
	}
	var a, b, empty int64
	for slug, target := range map[string]*int64{"a": &a, "b": &b, "empty": &empty} {
		if err := pool.QueryRow(ctx, `INSERT INTO organizations(slug,name) VALUES ($1,$1) RETURNING id`, slug).Scan(target); err != nil {
			t.Fatal(err)
		}
	}
	var privateA, privateB, shared, ungranted int
	for _, f := range []struct {
		name string
		org  any
		id   *int
	}{{"private-a", a, &privateA}, {"private-b", b, &privateB}, {"shared", nil, &shared}, {"ungranted", nil, &ungranted}} {
		if err := pool.QueryRow(ctx, `INSERT INTO media_folders(type,name,organization_id) VALUES ('movies',$1,$2) RETURNING id`, f.name, f.org).Scan(f.id); err != nil {
			t.Fatal(err)
		}
	}
	for _, id := range []int64{a, b} {
		if _, err := pool.Exec(ctx, `INSERT INTO organization_library_grants(organization_id,media_folder_id) VALUES ($1,$2)`, id, shared); err != nil {
			t.Fatal(err)
		}
	}
	repo := organizations.NewRepository(pool)
	for _, tc := range []struct {
		id   int64
		want []int
	}{{a, []int{privateA, shared}}, {b, []int{privateB, shared}}, {empty, []int{}}} {
		boundary, err := repo.ResolveViewerBoundary(ctx, tc.id)
		if err != nil {
			t.Fatal(err)
		}
		slices.Sort(tc.want)
		if boundary.OrganizationID != tc.id || boundary.AllowedLibraryIDs == nil || !slices.Equal(boundary.AllowedLibraryIDs, tc.want) {
			t.Fatalf("org %d: got %+v, want %v", tc.id, boundary, tc.want)
		}
	}
	if _, err := pool.Exec(ctx, `INSERT INTO organization_library_grants(organization_id,media_folder_id) VALUES ($1,$2)`, b, privateA); err == nil {
		t.Fatal("private library granted across organizations")
	}
	if _, err := pool.Exec(ctx, `UPDATE media_folders SET organization_id=$1 WHERE id=$2`, a, shared); err == nil {
		t.Fatal("shared granted library changed to private ownership")
	}
	before, err := repo.ResolveViewerBoundary(ctx, a)
	if err != nil {
		t.Fatal(err)
	}
	if _, err := pool.Exec(ctx, `DELETE FROM organization_library_grants WHERE organization_id=$1 AND media_folder_id=$2`, a, shared); err != nil {
		t.Fatal(err)
	}
	after, err := repo.ResolveViewerBoundary(ctx, a)
	if err != nil {
		t.Fatal(err)
	}
	if !slices.Equal(after.AllowedLibraryIDs, []int{privateA}) || after.AccessRevision <= before.AccessRevision {
		t.Fatalf("revocation not reflected: before=%+v after=%+v", before, after)
	}
	if _, err := pool.Exec(ctx, `UPDATE organizations SET status='suspended' WHERE id=$1`, a); err != nil {
		t.Fatal(err)
	}
	if _, err := repo.ResolveViewerBoundary(ctx, a); err == nil {
		t.Fatal("suspended organization resolved")
	}
	if _, err := repo.ResolveViewerBoundary(ctx, -1); err == nil {
		t.Fatal("missing organization resolved")
	}
}
