package organizations_test

import (
	"github.com/Silo-Server/silo-server/internal/access"
	"github.com/Silo-Server/silo-server/internal/auth"
	"github.com/Silo-Server/silo-server/internal/database"
	"github.com/Silo-Server/silo-server/internal/models"
	"github.com/Silo-Server/silo-server/migrations"
	"testing"
)

func TestGroupsCannotCrossOrganizations(t *testing.T) {
	pool := ownershipDatabase(t)
	ctx := t.Context()
	if err := database.RunMigrations(ctx, pool, migrations.FS, "sql"); err != nil {
		t.Fatal(err)
	}
	var a, b int64
	for slug, id := range map[string]*int64{"a": &a, "b": &b} {
		if err := pool.QueryRow(ctx, `INSERT INTO organizations(slug,name) VALUES ($1,$1) RETURNING id`, slug).Scan(id); err != nil {
			t.Fatal(err)
		}
	}
	groups := access.NewGroupStore(pool)
	ga, err := groups.Create(ctx, access.CreateGroupInput{Name: "Members", OrganizationID: a, IsDefault: true})
	if err != nil {
		t.Fatal(err)
	}
	gb, err := groups.Create(ctx, access.CreateGroupInput{Name: "Members", OrganizationID: b, IsDefault: true})
	if err != nil {
		t.Fatal(err)
	}
	again, err := groups.Get(ctx, ga.ID)
	if err != nil {
		t.Fatal(err)
	}
	if !again.IsDefault {
		t.Fatal("B's default change cleared A's default")
	}
	users := auth.NewUserRepository(pool)
	in := models.CreateUserInput{OrganizationID: a, Username: "member", Email: "member@example.test", Password: "fixture-password", Role: models.RoleUser}
	user, err := users.Create(ctx, in)
	if err != nil {
		t.Fatal(err)
	}
	if user.AccessGroupID == nil || *user.AccessGroupID != ga.ID {
		t.Fatal("account did not inherit its own organization's default group")
	}
	if err := users.Update(ctx, user.ID, models.UpdateUserInput{AccessGroupID: models.SetValue(gb.ID)}); err == nil {
		t.Fatal("cross-organization group assigned")
	}
	in.Username = "other"
	in.Email = "other@example.test"
	in.AccessGroupID = &gb.ID
	if _, err := users.Create(ctx, in); err == nil {
		t.Fatal("account created with foreign group")
	}
	temporary, err := groups.Create(ctx, access.CreateGroupInput{Name: "Temporary", OrganizationID: a})
	if err != nil {
		t.Fatal(err)
	}
	if err := users.Update(ctx, user.ID, models.UpdateUserInput{AccessGroupID: models.SetValue(temporary.ID)}); err != nil {
		t.Fatal(err)
	}
	if err := groups.Delete(ctx, temporary.ID); err != nil {
		t.Fatal(err)
	}
	after, err := users.GetByID(ctx, user.ID)
	if err != nil {
		t.Fatal(err)
	}
	if after.OrganizationID != a || after.AccessGroupID != nil {
		t.Fatal("group deletion changed account organization")
	}

}
