package auth

import (
	"context"
	"errors"
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
