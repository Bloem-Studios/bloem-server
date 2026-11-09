package pgstore

// Bloem-owned tests for bloemCreateRestoredProfileInTx (bloem_restored_profiles.go).

import (
	"context"
	"testing"
)

// Silo #3915: a primary profile inherits its account's libraries, so a restore
// that creates the account's primary drops the library limit the backup
// carried and reports that it made a primary. Bloem's restore writes the
// profile itself (into the account's organization), so it must do the same.
func TestBloemRestoredPrimaryProfileInheritsAccountLibraries(t *testing.T) {
	pool, userID := newProfileIdentityTestUser(t)
	ctx := context.Background()
	tx, err := pool.Begin(ctx)
	if err != nil {
		t.Fatal(err)
	}
	defer func() { _ = tx.Rollback(ctx) }()

	restore := func(id string) (bool, bool) {
		t.Helper()
		primary, err := CreateRestoredProfileInTx(ctx, tx, userID, RestoredProfile{
			ID: id, Name: id, IsPrimary: true, LibraryRestrictionsEnabled: true,
		})
		if err != nil {
			t.Fatalf("restore %s: %v", id, err)
		}
		var restricted bool
		if err := tx.QueryRow(ctx, `SELECT library_restrictions_enabled FROM user_profiles WHERE user_id = $1 AND id = $2`,
			userID, id).Scan(&restricted); err != nil {
			t.Fatalf("read %s: %v", id, err)
		}
		return primary, restricted
	}
	if primary, restricted := restore("restored-primary"); !primary || restricted {
		t.Fatalf("first restored profile: primary=%v restricted=%v, want the primary without a library limit", primary, restricted)
	}
	// The account has its primary now: a second profile the backup marks primary
	// is an ordinary profile and keeps its limit.
	if primary, restricted := restore("restored-second"); primary || !restricted {
		t.Fatalf("second restored profile: primary=%v restricted=%v, want an ordinary profile that keeps its limit", primary, restricted)
	}
}
