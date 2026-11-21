package auth

import (
	"testing"

	"github.com/Silo-Server/silo-server/internal/models"
)

// A household's pool applies to its main account and to every login of it,
// and only while the main account has a login: a household of one plays as
// it always has.
func TestHouseholdPoolForAccount(t *testing.T) {
	f := newHouseholdFixture(t)
	ctx := t.Context()
	lonely := f.account(models.RoleUser)
	plain := f.account(models.RoleUser)
	if _, err := f.pool.Exec(ctx, `INSERT INTO bloem_household_pools(main_user_id,max_streams,max_transcodes) VALUES($1,2,1),($2,4,4)`, f.main, lonely); err != nil {
		t.Fatal(err)
	}

	for _, account := range []int{f.main, f.login} {
		got, found, err := HouseholdPoolForAccount(ctx, f.pool, account)
		if err != nil || !found {
			t.Fatalf("account %d: found=%t error=%v", account, found, err)
		}
		if want := (HouseholdPool{MainUserID: f.main, MaxStreams: 2, MaxTranscodes: 1}); got != want {
			t.Fatalf("account %d: pool %+v, want %+v", account, got, want)
		}
	}
	for name, account := range map[string]int{"main without logins": lonely, "account without a pool": plain} {
		if got, found, err := HouseholdPoolForAccount(ctx, f.pool, account); err != nil || found {
			t.Fatalf("%s: pool %+v found=%t error=%v; want none", name, got, found, err)
		}
	}
}
