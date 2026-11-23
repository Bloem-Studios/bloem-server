package tenancy_test

import "testing"

// A household's login cap is Bloem's alone. While Silo switching is on, stock
// Silo's delete of a main account (users only) still succeeds: its logins go
// with it, and so does its cap.
func TestSwitchableSiloDeletesAMainAccountWithALoginCap(t *testing.T) {
	ctx, f := newHouseholdsFixture(t)
	if _, err := f.pool.Exec(ctx, `INSERT INTO bloem_household_login_caps (main_user_id, max_logins) VALUES ($1, 2)`, f.pooledMain); err != nil {
		t.Fatal(err)
	}
	if _, err := f.pool.Exec(ctx, `DELETE FROM users WHERE id = $1`, f.pooledMain); err != nil {
		t.Fatalf("stock Silo deleting a main account with a login cap: %v", err)
	}
	var caps, logins int
	if err := f.pool.QueryRow(ctx, `
		SELECT (SELECT count(*) FROM bloem_household_login_caps WHERE main_user_id = $1),
		       (SELECT count(*) FROM users WHERE household_main_user_id = $1)`, f.pooledMain).Scan(&caps, &logins); err != nil {
		t.Fatal(err)
	}
	if caps != 0 || logins != 0 {
		t.Fatalf("after the delete: %d caps, %d logins; want both gone with the main account", caps, logins)
	}
}
