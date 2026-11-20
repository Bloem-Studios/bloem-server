package auth

import (
	"context"
	"errors"
	"fmt"

	"github.com/jackc/pgx/v5"
	"github.com/jackc/pgx/v5/pgconn"

	"github.com/Silo-Server/silo-server/internal/models"
)

// Household logins (Bloem): an account whose household_main_user_id names a
// main account signs in with its own credentials but follows that main
// account's enabled state. Silo checks users.enabled wherever it admits an
// account, so keeping the login's enabled column in step with its main
// account covers every one of those checks. updateUser, the one place an
// account's enabled state is written, calls the two hooks below.

// ErrHouseholdMainDisabled refuses enabling a household login whose main
// account is disabled.
var ErrHouseholdMainDisabled = errors.New("the main account is disabled")

type householdDB interface {
	Exec(context.Context, string, ...any) (pgconn.CommandTag, error)
	QueryRow(context.Context, string, ...any) pgx.Row
}

// householdBeforeUpdate runs before an update writes account id. It reports
// whether id is a household login, and refuses enabling a login whose main
// account is disabled. The main account is locked first, in the same order
// a main account's own update takes (main, then its logins).
func householdBeforeUpdate(ctx context.Context, db householdDB, id int, input models.UpdateUserInput) (bool, error) {
	if input.Enabled == nil {
		return false, nil
	}
	var main *int
	err := db.QueryRow(ctx, `SELECT household_main_user_id FROM users WHERE id = $1`, id).Scan(&main)
	if errors.Is(err, pgx.ErrNoRows) || (err == nil && main == nil) {
		return false, nil
	}
	if err != nil {
		return false, fmt.Errorf("reading household link: %w", err)
	}
	if !*input.Enabled {
		return true, nil
	}
	var mainEnabled bool
	if err := db.QueryRow(ctx, `SELECT enabled FROM users WHERE id = $1 FOR SHARE`, *main).Scan(&mainEnabled); err != nil {
		return true, fmt.Errorf("reading main account: %w", err)
	}
	if !mainEnabled {
		return true, ErrHouseholdMainDisabled
	}
	return true, nil
}

// householdAfterUpdate runs after an update wrote account id's enabled state.
// An administrator's write to a login is the login's own state from then on,
// so it clears household_disabled. A write to a main account switches its
// enabled logins off (marking them household_disabled and signing them out,
// as disabling an account does) or switches back on only the logins it
// switched off. Each changed login gets the access-policy bump an enabled
// write gives.
func householdAfterUpdate(ctx context.Context, db householdDB, id int, input models.UpdateUserInput, login bool) error {
	if input.Enabled == nil {
		return nil
	}
	if login {
		if _, err := db.Exec(ctx, `UPDATE users SET household_disabled = false WHERE id = $1 AND household_disabled`, id); err != nil {
			return fmt.Errorf("clearing household state: %w", err)
		}
		return nil
	}
	if *input.Enabled {
		if _, err := db.Exec(ctx, `UPDATE users SET enabled = true, household_disabled = false,
			access_policy_revision = access_policy_revision + 1, updated_at = NOW()
			WHERE household_main_user_id = $1 AND household_disabled`, id); err != nil {
			return fmt.Errorf("enabling household logins: %w", err)
		}
		return nil
	}
	var switchedOff []int
	if err := db.QueryRow(ctx, `WITH off AS (
			UPDATE users SET enabled = false, household_disabled = true,
				access_policy_revision = access_policy_revision + 1, updated_at = NOW()
			WHERE household_main_user_id = $1 AND enabled
			RETURNING id)
		SELECT COALESCE(array_agg(id ORDER BY id), '{}') FROM off`, id).Scan(&switchedOff); err != nil {
		return fmt.Errorf("disabling household logins: %w", err)
	}
	if len(switchedOff) == 0 {
		return nil
	}
	tx, ok := db.(pgx.Tx)
	if !ok {
		return errors.New("disabling household logins needs a transaction")
	}
	return RevokeSignInsForUsersInTransaction(ctx, tx, switchedOff)
}
