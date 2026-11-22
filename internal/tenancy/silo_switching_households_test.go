package tenancy_test

// Households while stock Silo serves a switchable database. Stock Silo does
// not enforce a household's shared pool: each account plays to its own limits,
// and in a pool the main account carries the whole budget. So before stock
// Silo serves, PrepareForSilo holds the logins of every pooled household (the
// main account and its profiles keep the household's budget), and Bloem
// releases them when it serves again (ResumeAfterSilo). A household of shares
// already fits its budget account by account, and keeps its logins.

import (
	"context"
	"testing"
	"time"

	"github.com/google/uuid"
	"github.com/jackc/pgx/v5/pgxpool"

	"github.com/Silo-Server/silo-server/internal/tenancy"
)

type loginState struct{ enabled, householdDisabled, held bool }

func readLogin(t *testing.T, ctx context.Context, pool *pgxpool.Pool, id int) loginState {
	t.Helper()
	var s loginState
	if err := pool.QueryRow(ctx, `
		SELECT enabled, household_disabled, EXISTS (SELECT 1 FROM bloem_household_silo_holds WHERE user_id = $1)
		FROM users WHERE id = $1`, id).Scan(&s.enabled, &s.householdDisabled, &s.held); err != nil {
		t.Fatal(err)
	}
	return s
}

// switchableAccount creates an account while switching is on, as stock Silo
// does: the users row only; the database gives it its membership.
func switchableAccount(t *testing.T, ctx context.Context, pool *pgxpool.Pool, name string) int {
	t.Helper()
	var id int
	if err := pool.QueryRow(ctx, `
		INSERT INTO users (username, email, password_hash, role, enabled, max_streams, access_group_id)
		VALUES ($1::text, $1::text || '@example.invalid', 'x', 'user', true, 3, (SELECT id FROM access_groups WHERE is_default))
		RETURNING id`, name).Scan(&id); err != nil {
		t.Fatalf("create account %s: %v", name, err)
	}
	return id
}

func linkLogin(t *testing.T, ctx context.Context, pool *pgxpool.Pool, main int, name string) int {
	t.Helper()
	login := switchableAccount(t, ctx, pool, name)
	if _, err := pool.Exec(ctx, `UPDATE users SET household_main_user_id = $1 WHERE id = $2`, main, login); err != nil {
		t.Fatal(err)
	}
	return login
}

func openSession(t *testing.T, ctx context.Context, pool *pgxpool.Pool, user int) string {
	t.Helper()
	id := uuid.NewString()
	if _, err := pool.Exec(ctx, `INSERT INTO auth_sessions (id, user_id, expires_at) VALUES ($1, $2, $3)`, id, user, time.Now().Add(time.Hour)); err != nil {
		t.Fatal(err)
	}
	return id
}

func sessionRevoked(t *testing.T, ctx context.Context, pool *pgxpool.Pool, id string) bool {
	t.Helper()
	var revoked bool
	if err := pool.QueryRow(ctx, `SELECT revoked_at IS NOT NULL FROM auth_sessions WHERE id = $1`, id).Scan(&revoked); err != nil {
		t.Fatal(err)
	}
	return revoked
}

// householdsFixture is a switchable database with a pooled household (main,
// an enabled login and one an administrator disabled) and a household of
// shares (main and one login).
type householdsFixture struct {
	pool                                         *pgxpool.Pool
	pooledMain, pooledLogin, pooledAdminDisabled int
	sharesMain, sharesLogin                      int
	pooledSession                                string
}

func newHouseholdsFixture(t *testing.T) (context.Context, householdsFixture) {
	t.Helper()
	ctx, pool, pooledMain, _ := switchablePool(t)
	f := householdsFixture{pool: pool, pooledMain: pooledMain}
	f.pooledLogin = linkLogin(t, ctx, pool, pooledMain, "pooled-login")
	f.pooledAdminDisabled = linkLogin(t, ctx, pool, pooledMain, "pooled-admin-off")
	if _, err := pool.Exec(ctx, `UPDATE users SET enabled = false WHERE id = $1`, f.pooledAdminDisabled); err != nil {
		t.Fatal(err)
	}
	if _, err := pool.Exec(ctx, `INSERT INTO bloem_household_pools (main_user_id, max_streams, max_transcodes) VALUES ($1, 3, 2)`, pooledMain); err != nil {
		t.Fatal(err)
	}
	f.sharesMain = switchableAccount(t, ctx, pool, "shares-main")
	f.sharesLogin = linkLogin(t, ctx, pool, f.sharesMain, "shares-login")
	f.pooledSession = openSession(t, ctx, pool, f.pooledLogin)
	return ctx, f
}

func TestPrepareForSiloHoldsThePooledHouseholdsLogins(t *testing.T) {
	ctx, f := newHouseholdsFixture(t)
	held, err := tenancy.PrepareForSilo(ctx, f.pool)
	if err != nil {
		t.Fatalf("prepare for Silo: %v", err)
	}
	if held != 1 {
		t.Fatalf("held %d logins, want the pooled household's enabled login", held)
	}
	if s := readLogin(t, ctx, f.pool, f.pooledLogin); s.enabled || !s.held {
		t.Fatalf("pooled login after prepare: %+v; want held, disabled where stock Silo reads it", s)
	}
	if !sessionRevoked(t, ctx, f.pool, f.pooledSession) {
		t.Fatal("the held login's sign-in survived")
	}
	if s := readLogin(t, ctx, f.pool, f.pooledAdminDisabled); s.enabled || s.held {
		t.Fatalf("administrator-disabled login: %+v; want left as it was", s)
	}
	if s := readLogin(t, ctx, f.pool, f.sharesLogin); !s.enabled || s.held {
		t.Fatalf("a login of shares: %+v; want it playing on its share", s)
	}
	if again, err := tenancy.PrepareForSilo(ctx, f.pool); err != nil || again != 0 {
		t.Fatalf("prepare again = %d, %v; want nothing more held", again, err)
	}

	households, err := tenancy.SiloSwitchingHouseholds(ctx, f.pool)
	if err != nil {
		t.Fatal(err)
	}
	if want := (tenancy.SiloHouseholds{Households: 2, Logins: 3, Pooled: 1, PooledLogins: 2, Held: 1}); households != want {
		t.Fatalf("households %+v, want %+v", households, want)
	}
}

// While stock Silo serves, restoring a suspended main account does not bring
// its held logins back: the pool still does not hold under stock Silo.
func TestHeldLoginsStayHeldWhenStockSiloRestoresTheMain(t *testing.T) {
	ctx, f := newHouseholdsFixture(t)
	if _, err := tenancy.PrepareForSilo(ctx, f.pool); err != nil {
		t.Fatal(err)
	}
	for _, enabled := range []bool{false, true} {
		if _, err := f.pool.Exec(ctx, `UPDATE users SET enabled = $2, updated_at = NOW() WHERE id = $1`, f.pooledMain, enabled); err != nil {
			t.Fatal(err)
		}
	}
	if s := readLogin(t, ctx, f.pool, f.pooledLogin); s.enabled || !s.held {
		t.Fatalf("held login after stock Silo suspended and restored its main account: %+v; want still held", s)
	}
}

func TestResumeAfterSiloReleasesHeldLogins(t *testing.T) {
	ctx, f := newHouseholdsFixture(t)
	if _, err := tenancy.PrepareForSilo(ctx, f.pool); err != nil {
		t.Fatal(err)
	}
	released, err := tenancy.ResumeAfterSilo(ctx, f.pool)
	if err != nil || released != 1 {
		t.Fatalf("resume = %d, %v; want the one held login released", released, err)
	}
	if s := readLogin(t, ctx, f.pool, f.pooledLogin); !s.enabled || s.householdDisabled || s.held {
		t.Fatalf("login after resume: %+v; want enabled again", s)
	}
	if s := readLogin(t, ctx, f.pool, f.pooledAdminDisabled); s.enabled {
		t.Fatalf("administrator-disabled login came back: %+v", s)
	}
	if again, err := tenancy.ResumeAfterSilo(ctx, f.pool); err != nil || again != 0 {
		t.Fatalf("resume again = %d, %v; want nothing", again, err)
	}
}

// A main account suspended while stock Silo served keeps its logins off when
// Bloem serves again; they come back with the main account.
func TestResumeAfterSiloKeepsASuspendedHouseholdOff(t *testing.T) {
	ctx, f := newHouseholdsFixture(t)
	if _, err := tenancy.PrepareForSilo(ctx, f.pool); err != nil {
		t.Fatal(err)
	}
	if _, err := f.pool.Exec(ctx, `UPDATE users SET enabled = false, updated_at = NOW() WHERE id = $1`, f.pooledMain); err != nil {
		t.Fatal(err)
	}
	if _, err := tenancy.ResumeAfterSilo(ctx, f.pool); err != nil {
		t.Fatal(err)
	}
	if s := readLogin(t, ctx, f.pool, f.pooledLogin); s.enabled || !s.householdDisabled || s.held {
		t.Fatalf("login of a suspended household after resume: %+v; want switched off by the household", s)
	}
	if _, err := f.pool.Exec(ctx, `UPDATE users SET enabled = true, updated_at = NOW() WHERE id = $1`, f.pooledMain); err != nil {
		t.Fatal(err)
	}
	if s := readLogin(t, ctx, f.pool, f.pooledLogin); !s.enabled {
		t.Fatalf("login after its main account came back: %+v; want enabled", s)
	}
}

// An administrator who enables a held login while stock Silo serves takes it
// out of the hold: Bloem's resume leaves that login as the administrator set it.
func TestAnAdministratorWriteTakesALoginOutOfTheHold(t *testing.T) {
	ctx, f := newHouseholdsFixture(t)
	if _, err := tenancy.PrepareForSilo(ctx, f.pool); err != nil {
		t.Fatal(err)
	}
	for _, enabled := range []bool{true, false} {
		if _, err := f.pool.Exec(ctx, `UPDATE users SET enabled = $2, updated_at = NOW() WHERE id = $1`, f.pooledLogin, enabled); err != nil {
			t.Fatal(err)
		}
	}
	if released, err := tenancy.ResumeAfterSilo(ctx, f.pool); err != nil || released != 0 {
		t.Fatalf("resume = %d, %v; want nothing released", released, err)
	}
	if s := readLogin(t, ctx, f.pool, f.pooledLogin); s.enabled {
		t.Fatalf("login an administrator disabled under stock Silo came back: %+v", s)
	}
}

func TestPrepareForSiloNeedsSwitching(t *testing.T) {
	ctx, pool, _ := buildTenantPool(t)
	if _, err := tenancy.PrepareForSilo(ctx, pool); err == nil {
		t.Fatal("prepared a database stock Silo cannot serve")
	}
}

// Stock Silo's writes to a household while switching is on: it creates a
// profile on a login as on any account, and moving a main account to another
// group carries its logins along, as under Bloem.
func TestSwitchableSiloWritesToAHousehold(t *testing.T) {
	ctx, f := newHouseholdsFixture(t)
	tx, err := f.pool.Begin(ctx)
	if err != nil {
		t.Fatal(err)
	}
	siloCreateProfileTx(t, ctx, tx, f.pooledLogin, "pooled-login-main")
	if err := tx.Commit(ctx); err != nil {
		t.Fatalf("stock Silo creating a profile on a login: %v", err)
	}
	var group int64
	if err := f.pool.QueryRow(ctx, `INSERT INTO access_groups (organization_id, name) VALUES (public.bloem_default_organization_id(), 'household-switch') RETURNING id`).Scan(&group); err != nil {
		t.Fatal(err)
	}
	if _, err := f.pool.Exec(ctx, `UPDATE users SET access_group_id = $1, updated_at = NOW() WHERE id = $2`, group, f.pooledMain); err != nil {
		t.Fatalf("stock Silo moving a main account to another group: %v", err)
	}
	var loginGroup *int64
	if err := f.pool.QueryRow(ctx, `SELECT access_group_id FROM users WHERE id = $1`, f.pooledLogin).Scan(&loginGroup); err != nil {
		t.Fatal(err)
	}
	if loginGroup == nil || *loginGroup != group {
		t.Fatalf("login group %v after its main account moved to %d; want it to follow", loginGroup, group)
	}
}
