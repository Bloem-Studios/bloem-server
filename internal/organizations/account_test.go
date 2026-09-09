package organizations_test

import (
	"testing"

	"github.com/Silo-Server/silo-server/internal/auth"
	"github.com/Silo-Server/silo-server/internal/database"
	"github.com/Silo-Server/silo-server/internal/models"
	"github.com/Silo-Server/silo-server/migrations"
)

func TestAccountCreationAssignsOrganization(t *testing.T) {
	pool := ownershipDatabase(t)
	ctx := t.Context()
	if err := database.RunMigrations(ctx, pool, migrations.FS, "sql"); err != nil {
		t.Fatal(err)
	}
	users := auth.NewUserRepository(pool)
	for _, tc := range []struct{ role, slug string }{{"user", "default"}, {"admin", "platform"}} {
		user, err := users.Create(ctx, models.CreateUserInput{Username: tc.role, Email: tc.role + "@example.test", Password: "fixture-password", Role: tc.role})
		if err != nil {
			t.Fatal(err)
		}
		var slug string
		if err := pool.QueryRow(ctx, `SELECT o.slug FROM users u JOIN organizations o ON o.id=u.organization_id WHERE u.id=$1`, user.ID).Scan(&slug); err != nil {
			t.Fatal(err)
		}
		if slug != tc.slug {
			t.Fatalf("%s account assigned to %s; want %s", tc.role, slug, tc.slug)
		}
	}
}

func TestAccountCreationUsesExplicitOrganization(t *testing.T) {
	pool := ownershipDatabase(t)
	ctx := t.Context()
	if err := database.RunMigrations(ctx, pool, migrations.FS, "sql"); err != nil {
		t.Fatal(err)
	}
	var orgID int64
	if err := pool.QueryRow(ctx, `INSERT INTO organizations (slug,name) VALUES ('tenant-a','Tenant A') RETURNING id`).Scan(&orgID); err != nil {
		t.Fatal(err)
	}
	users := auth.NewUserRepository(pool)
	in := models.CreateUserInput{Username: "tenant-user", Email: "tenant@example.test", Password: "fixture-password", Role: models.RoleUser, OrganizationID: orgID}
	created, err := users.Create(ctx, in)
	if err != nil {
		t.Fatal(err)
	}
	fetched, err := users.GetByID(ctx, created.ID)
	if err != nil {
		t.Fatal(err)
	}
	listed, err := users.List(ctx)
	if err != nil {
		t.Fatal(err)
	}
	for _, user := range append(listed, created, fetched) {
		if user.OrganizationID != orgID || user.OrganizationRole != "member" {
			t.Fatalf("account lost organization ownership: %+v", user)
		}
	}
	in.Username, in.Email, in.OrganizationID = "invalid", "invalid@example.test", -1
	if _, err := users.Create(ctx, in); err == nil {
		t.Fatal("unknown organization accepted")
	}
	if _, err := pool.Exec(ctx, `UPDATE organizations SET status='suspended' WHERE id=$1`, orgID); err != nil {
		t.Fatal(err)
	}
	in.OrganizationID = orgID
	if _, err := users.Create(ctx, in); err == nil {
		t.Fatal("suspended organization accepted a new account")
	}
	var count int
	if err := pool.QueryRow(ctx, `SELECT count(*) FROM users`).Scan(&count); err != nil {
		t.Fatal(err)
	}
	if count != 1 {
		t.Fatalf("failed creation left accounts: %d", count)
	}
}
