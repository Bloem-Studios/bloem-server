package auth

import (
	"context"
	"errors"
	"fmt"
	"testing"

	"github.com/Silo-Server/silo-server/internal/models"
)

// householdFixture is a main account with two logins: the first enabled, the
// second disabled by an administrator.
type householdFixture struct {
	*signInFixture
	main, login, adminDisabled int
}

func newHouseholdFixture(t *testing.T) householdFixture {
	t.Helper()
	f := householdFixture{signInFixture: newSignInFixture(t)}
	f.main = f.account(models.RoleUser)
	f.login = f.account(models.RoleUser)
	f.adminDisabled = f.account(models.RoleUser)
	ctx := t.Context()
	if _, err := f.pool.Exec(ctx, `UPDATE users SET household_main_user_id=$1 WHERE id = ANY($2)`, f.main, []int{f.login, f.adminDisabled}); err != nil {
		t.Fatal(err)
	}
	if _, err := f.pool.Exec(ctx, `UPDATE users SET enabled=false WHERE id=$1`, f.adminDisabled); err != nil {
		t.Fatal(err)
	}
	// Runs before the sign-in fixture's cleanup: a main account cannot be
	// deleted while it has logins.
	t.Cleanup(func() {
		_, _ = f.pool.Exec(context.WithoutCancel(t.Context()), `DELETE FROM users WHERE household_main_user_id=$1`, f.main)
	})
	return f
}

type householdState struct {
	enabled, householdDisabled bool
	policyRevision             int64
}

func (f householdFixture) state(id int) householdState {
	f.t.Helper()
	var s householdState
	if err := f.pool.QueryRow(f.t.Context(), `SELECT enabled, household_disabled, access_policy_revision FROM users WHERE id=$1`, id).Scan(&s.enabled, &s.householdDisabled, &s.policyRevision); err != nil {
		f.t.Fatal(err)
	}
	return s
}

func (f householdFixture) setEnabled(id int, enabled bool) error {
	return f.users.Update(f.t.Context(), id, models.UpdateUserInput{Enabled: &enabled})
}

func TestDisablingMainDisablesItsLogins(t *testing.T) {
	f := newHouseholdFixture(t)
	session := f.session(f.login, nil)
	before := f.state(f.login)
	if err := f.setEnabled(f.main, false); err != nil {
		t.Fatal(err)
	}
	login := f.state(f.login)
	if login.enabled || !login.householdDisabled {
		t.Fatalf("login after its main account was disabled: %+v; want disabled by the household", login)
	}
	if login.policyRevision <= before.policyRevision {
		t.Fatalf("login access policy revision %d, was %d; want a bump", login.policyRevision, before.policyRevision)
	}
	if f.active(session) {
		t.Fatal("the login's session survived its main account being disabled")
	}
	if adminDisabled := f.state(f.adminDisabled); adminDisabled.enabled || adminDisabled.householdDisabled {
		t.Fatalf("administrator-disabled login: %+v; want disabled, not by the household", adminDisabled)
	}
}

func TestEnablingMainRestoresOnlyHouseholdDisabledLogins(t *testing.T) {
	f := newHouseholdFixture(t)
	if err := f.setEnabled(f.main, false); err != nil {
		t.Fatal(err)
	}
	if err := f.setEnabled(f.main, true); err != nil {
		t.Fatal(err)
	}
	if login := f.state(f.login); !login.enabled || login.householdDisabled {
		t.Fatalf("login after its main account came back: %+v; want enabled", login)
	}
	if adminDisabled := f.state(f.adminDisabled); adminDisabled.enabled {
		t.Fatalf("administrator-disabled login came back with its main account: %+v", adminDisabled)
	}
}

func TestLoginCannotBeEnabledWhileMainDisabled(t *testing.T) {
	f := newHouseholdFixture(t)
	if err := f.setEnabled(f.main, false); err != nil {
		t.Fatal(err)
	}
	if err := f.setEnabled(f.adminDisabled, true); !errors.Is(err, ErrHouseholdMainDisabled) {
		t.Fatalf("enabling a login under a disabled main account: %v; want ErrHouseholdMainDisabled", err)
	}
	if adminDisabled := f.state(f.adminDisabled); adminDisabled.enabled {
		t.Fatal("the refused enable was written")
	}
}

// An administrator who disables a login while its main account is disabled
// keeps it off when the main account comes back.
func TestDisablingHouseholdDisabledLoginKeepsItOff(t *testing.T) {
	f := newHouseholdFixture(t)
	if err := f.setEnabled(f.main, false); err != nil {
		t.Fatal(err)
	}
	if err := f.setEnabled(f.login, false); err != nil {
		t.Fatal(err)
	}
	if err := f.setEnabled(f.main, true); err != nil {
		t.Fatal(err)
	}
	if login := f.state(f.login); login.enabled {
		t.Fatalf("login an administrator disabled came back with its main account: %+v", login)
	}
}

// A login sees what its main account sees: when the main account moves to
// another access group (a plan change writes the column directly), the login
// and its profiles that sat on the old group move with it.
func TestLoginFollowsMainAccessGroup(t *testing.T) {
	f := newHouseholdFixture(t)
	ctx := t.Context()
	var oldGroup, newGroup int64
	for i, g := range []*int64{&oldGroup, &newGroup} {
		if err := f.pool.QueryRow(ctx, `INSERT INTO access_groups (organization_id, name) SELECT id, $1 FROM organizations WHERE is_default RETURNING id`,
			fmt.Sprintf("household-group-%s-%d", f.suffix, i)).Scan(g); err != nil {
			t.Fatal(err)
		}
		id := *g
		t.Cleanup(func() { _, _ = f.pool.Exec(context.WithoutCancel(ctx), `DELETE FROM access_groups WHERE id = $1`, id) })
	}
	if _, err := f.pool.Exec(ctx, `UPDATE users SET access_group_id = $1 WHERE id = ANY($2)`, oldGroup, []int{f.main, f.login}); err != nil {
		t.Fatal(err)
	}
	if _, err := f.pool.Exec(ctx, `INSERT INTO user_profiles (id, user_id, name, is_primary) VALUES ($1, $2, 'Sam', true)`, "household-profile-"+f.suffix, f.login); err != nil {
		t.Fatal(err)
	}
	if _, err := f.pool.Exec(ctx, `UPDATE user_profiles SET access_group_id = $1 WHERE user_id = $2`, oldGroup, f.login); err != nil {
		t.Fatal(err)
	}
	before := f.state(f.login)
	if _, err := f.pool.Exec(ctx, `UPDATE users SET access_group_id = $1 WHERE id = $2`, newGroup, f.main); err != nil {
		t.Fatal(err)
	}
	var loginGroup, profileGroup *int64
	if err := f.pool.QueryRow(ctx, `SELECT u.access_group_id, p.access_group_id FROM users u JOIN user_profiles p ON p.user_id = u.id WHERE u.id = $1`, f.login).Scan(&loginGroup, &profileGroup); err != nil {
		t.Fatal(err)
	}
	if loginGroup == nil || *loginGroup != newGroup || profileGroup == nil || *profileGroup != newGroup {
		t.Fatalf("login group %v, profile group %v; want both on the main account's new group %d", loginGroup, profileGroup, newGroup)
	}
	if after := f.state(f.login); after.policyRevision <= before.policyRevision {
		t.Fatal("the login's access policy revision did not move with its group")
	}
}

// A login's Jellyfin-compatible sign-ins end with its main account, as every
// other sign-in does, so a Jellyfin app on the login stops working too.
func TestDisablingMainDeletesItsLoginsJellyfinSessions(t *testing.T) {
	f := newHouseholdFixture(t)
	insertJellyfinSession(t, f.pool, f.login)
	if err := f.setEnabled(f.main, false); err != nil {
		t.Fatal(err)
	}
	var left int
	if err := f.pool.QueryRow(t.Context(), `SELECT count(*) FROM jellycompat_sessions WHERE streamapp_user_id = $1`, f.login).Scan(&left); err != nil {
		t.Fatal(err)
	}
	if left != 0 {
		t.Fatalf("Jellyfin sessions left on the login after its main account was disabled: %d", left)
	}
}
