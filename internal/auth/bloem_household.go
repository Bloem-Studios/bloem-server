package auth

import (
	"context"
	"errors"
	"fmt"

	"github.com/jackc/pgx/v5"

	"github.com/Silo-Server/silo-server/internal/models"
)

// Household logins (Bloem): an account whose household_main_user_id names a
// main account signs in with its own credentials but follows that main
// account's enabled state. Silo checks users.enabled wherever it admits an
// account, so keeping the login's enabled column in step with its main
// account covers every one of those checks. The database keeps them in step
// (migration 20261122213808_bloem_household_logins_under_silo_switching), so
// a suspension holds when stock Silo writes users.enabled too; updateUser
// calls householdBeforeUpdate only to refuse, with a typed error, enabling a
// login whose main account is disabled.

// ErrHouseholdMainDisabled refuses enabling a household login whose main
// account is disabled.
var ErrHouseholdMainDisabled = errors.New("the main account is disabled")

// householdBeforeUpdate runs before an update writes account id. It refuses
// enabling a household login whose main account is disabled. The main account
// is locked first, in the same order a main account's own update takes
// (main, then its logins); the database refuses the same write
// (bloem_household_main_disabled) for a writer that skips this check.
func householdBeforeUpdate(ctx context.Context, db userCreateQuerier, id int, input models.UpdateUserInput) error {
	if input.Enabled == nil || !*input.Enabled {
		return nil
	}
	var main *int
	err := db.QueryRow(ctx, `SELECT household_main_user_id FROM users WHERE id = $1`, id).Scan(&main)
	if errors.Is(err, pgx.ErrNoRows) || (err == nil && main == nil) {
		return nil
	}
	if err != nil {
		return fmt.Errorf("reading household link: %w", err)
	}
	var mainEnabled bool
	if err := db.QueryRow(ctx, `SELECT enabled FROM users WHERE id = $1 FOR SHARE`, *main).Scan(&mainEnabled); err != nil {
		return fmt.Errorf("reading main account: %w", err)
	}
	if !mainEnabled {
		return ErrHouseholdMainDisabled
	}
	return nil
}
