package auth

// Household logins as stock Silo sees them. Upstream Silo, if it ever serves
// this database, knows nothing of households: it suspends an account by
// writing users.enabled, and admits an account only while users.enabled holds
// and through the sign-ins it stores (auth_sessions, abs_sessions,
// jellycompat_sessions, approved device sign-ins). These tests write the way
// stock Silo writes, in plain SQL on its columns, and read only the columns
// stock Silo reads, so a household holds under either server.

import (
	"slices"
	"strings"
	"testing"
)

// siloSetEnabled writes an account's enabled state the way stock Silo's
// account update does.
func (f householdFixture) siloSetEnabled(id int, enabled bool) error {
	_, err := f.pool.Exec(f.t.Context(), `UPDATE users SET enabled = $2, updated_at = NOW() WHERE id = $1`, id, enabled)
	return err
}

// siloView is what stock Silo reads of an account: may it sign in, and do the
// given sign-ins still work.
func (f householdFixture) siloView(id int, in accountSignIns) (enabled bool, live []bool) {
	f.t.Helper()
	if err := f.pool.QueryRow(f.t.Context(), `SELECT enabled FROM users WHERE id = $1`, id).Scan(&enabled); err != nil {
		f.t.Fatal(err)
	}
	return enabled, f.live(in)
}

// Stock Silo suspending a main account suspends its logins too, with every
// sign-in ended in the tables stock Silo checks: none of them can sign in or
// keep playing on either server.
func TestStockSiloSuspendingMainSuspendsItsLogins(t *testing.T) {
	f := newHouseholdFixture(t)
	in := f.signIns(f.login, f.session(f.login, nil))
	if err := f.siloSetEnabled(f.main, false); err != nil {
		t.Fatal(err)
	}
	enabled, live := f.siloView(f.login, in)
	if enabled || slices.Contains(live, true) {
		t.Fatalf("login after stock Silo suspended its main account: enabled=%t sign-ins live=%v; want disabled with every sign-in ended", enabled, live)
	}
	if login := f.state(f.login); !login.householdDisabled {
		t.Fatalf("login %+v; want marked as switched off by the household", login)
	}
	if adminDisabled := f.state(f.adminDisabled); adminDisabled.enabled || adminDisabled.householdDisabled {
		t.Fatalf("administrator-disabled login: %+v; want disabled, not by the household", adminDisabled)
	}
}

// Stock Silo restoring the main account restores only the logins the
// household switched off, never one an administrator disabled.
func TestStockSiloRestoringMainRestoresOnlyItsHouseholdsLogins(t *testing.T) {
	f := newHouseholdFixture(t)
	if err := f.siloSetEnabled(f.main, false); err != nil {
		t.Fatal(err)
	}
	if err := f.siloSetEnabled(f.main, true); err != nil {
		t.Fatal(err)
	}
	if login := f.state(f.login); !login.enabled || login.householdDisabled {
		t.Fatalf("login after stock Silo restored its main account: %+v; want enabled", login)
	}
	if adminDisabled := f.state(f.adminDisabled); adminDisabled.enabled {
		t.Fatalf("administrator-disabled login came back with its main account: %+v", adminDisabled)
	}
}

// Stock Silo cannot enable a login while its main account is suspended: the
// database refuses it, as Bloem does.
func TestStockSiloCannotEnableALoginUnderASuspendedMain(t *testing.T) {
	f := newHouseholdFixture(t)
	if err := f.siloSetEnabled(f.main, false); err != nil {
		t.Fatal(err)
	}
	err := f.siloSetEnabled(f.login, true)
	if err == nil || !strings.Contains(err.Error(), "bloem_household_main_disabled") {
		t.Fatalf("stock Silo enabling a login under a suspended main account: %v; want bloem_household_main_disabled", err)
	}
	if login := f.state(f.login); login.enabled {
		t.Fatal("the refused enable was written")
	}
}

// An administrator's write to a login under stock Silo is the login's own
// state from then on: disabling a login the household switched off keeps it
// off when the main account comes back.
func TestStockSiloAdministratorDisablingALoginKeepsItOff(t *testing.T) {
	f := newHouseholdFixture(t)
	if err := f.siloSetEnabled(f.main, false); err != nil {
		t.Fatal(err)
	}
	if err := f.siloSetEnabled(f.login, false); err != nil {
		t.Fatal(err)
	}
	if err := f.siloSetEnabled(f.main, true); err != nil {
		t.Fatal(err)
	}
	if login := f.state(f.login); login.enabled {
		t.Fatalf("login an administrator disabled came back with its main account: %+v", login)
	}
}
