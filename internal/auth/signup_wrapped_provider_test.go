package auth_test

import (
	"fmt"
	"testing"

	"github.com/Silo-Server/silo-server/internal/auth"
	"github.com/Silo-Server/silo-server/internal/models"
	"github.com/Silo-Server/silo-server/internal/notifications"
	"github.com/Silo-Server/silo-server/internal/tenancy"
	"github.com/Silo-Server/silo-server/internal/userstore"
	"github.com/Silo-Server/silo-server/internal/userstore/pgstore"
)

func TestInvitedAccountWrappedPostgresDB(t *testing.T) {
	ctx := t.Context()
	pool := auth.NewBloemAuthTestDatabase(t)
	var creator int
	if err := pool.QueryRow(ctx, `INSERT INTO users(username,email,password_hash,role,enabled) VALUES('creator','creator@example.invalid','x','admin',true) RETURNING id`).Scan(&creator); err != nil {
		t.Fatal(err)
	}
	tenants := tenancy.NewStore(pool)
	if _, err := tenants.ProvisionDefaultMembership(ctx, creator, "admin"); err != nil {
		t.Fatal(err)
	}
	if _, err := tenants.ActivateInitialOwnership(ctx, creator); err != nil {
		t.Fatal(err)
	}
	for _, wrapped := range []bool{false, true} {
		name := fmt.Sprintf("account-%v", wrapped)
		code := fmt.Sprintf("invite-%v", wrapped)
		if _, err := pool.Exec(ctx, `INSERT INTO invite_codes(code,max_uses,created_by) VALUES($1,1,$2)`, code, creator); err != nil {
			t.Fatal(err)
		}
		var provider userstore.UserStoreProvider = pgstore.NewPostgresProvider(pool)
		if wrapped {
			provider = notifications.WrapUserStoreProvider(provider, &notifications.System{})
		}
		accounts := auth.NewAccountProvisioner(auth.NewUserRepository(pool), provider)
		accounts.SetMembershipProvisioner(&bloemAccountMemberships{store: tenants})
		_, err := accounts.CreateInvitedAccount(ctx, auth.CreateAccountInput{User: models.CreateUserInput{Username: name, Email: name + "@example.invalid", Password: "test-password", Role: "user"}, DefaultProfile: auth.DefaultProfileOptions{Enabled: true, Name: "Home"}}, code)
		var users, profiles, uses int
		if e := pool.QueryRow(ctx, `SELECT count(*) FROM users WHERE username=$1`, name).Scan(&users); e != nil {
			t.Fatal(e)
		}
		if e := pool.QueryRow(ctx, `SELECT count(*) FROM user_profiles p JOIN users u ON u.id=p.user_id WHERE u.username=$1`, name).Scan(&profiles); e != nil {
			t.Fatal(e)
		}
		if e := pool.QueryRow(ctx, `SELECT use_count FROM invite_codes WHERE code=$1`, code).Scan(&uses); e != nil {
			t.Fatal(e)
		}
		t.Logf("wrapped=%v err=%v users=%d profiles=%d inviteUses=%d", wrapped, err, users, profiles, uses)
		if err != nil || users != 1 || profiles != 1 || uses != 1 {
			t.Errorf("production provider must provision account/profile with one invite use")
		}
	}
}
