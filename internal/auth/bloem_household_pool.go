package auth

import (
	"context"
	"errors"
	"fmt"

	"github.com/jackc/pgx/v5"
)

// A household's shared pool (Bloem): the concurrent streams and transcodes a
// main account and all its household logins draw on together, set by a
// storefront on the main account (bloem_household_pools). Playback reserves
// against it beside each account's own limits
// (internal/playback/bloem_household_reservation.go). It applies only while
// the main account has a login, so a household of one plays as it always has.

// HouseholdPool is a household's pool, keyed by its main account. 0 is
// unlimited.
type HouseholdPool struct {
	MainUserID    int
	MaxStreams    int
	MaxTranscodes int
}

// HouseholdPoolForAccount is the pool accountID plays in: its own, as a main
// account with at least one login, or its main account's, as a login. found
// is false when it plays in none. db is a pool, a connection or a
// transaction.
func HouseholdPoolForAccount(ctx context.Context, db rowQuerier, accountID int) (HouseholdPool, bool, error) {
	var pool HouseholdPool
	err := db.QueryRow(ctx, `
		SELECT household.main_user_id, household.max_streams, household.max_transcodes
		FROM users AS account
		JOIN bloem_household_pools AS household
		  ON household.main_user_id = COALESCE(account.household_main_user_id, account.id)
		WHERE account.id = $1
		  AND EXISTS (SELECT 1 FROM users AS login WHERE login.household_main_user_id = household.main_user_id)`,
		accountID).Scan(&pool.MainUserID, &pool.MaxStreams, &pool.MaxTranscodes)
	if errors.Is(err, pgx.ErrNoRows) {
		return HouseholdPool{}, false, nil
	}
	if err != nil {
		return HouseholdPool{}, false, fmt.Errorf("load household pool: %w", err)
	}
	return pool, true, nil
}
