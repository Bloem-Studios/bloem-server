package playback

import (
	"context"
	"errors"
	"fmt"
	"strconv"

	"github.com/jackc/pgx/v5"
)

// A household's shared pool in the fleet reservation (Bloem separate logins).
// A household is a main account and its household logins; when a storefront
// gives it a pool (bloem_household_pools), every start by any of its
// accounts reserves against the household's streams and transcodes, beside
// the account's own limits and before an optional tenant pool. It counts
// every live reservation of the household's accounts. Only the fleet
// reservation counts it: production always installs one, and it already
// leaves out the session being reserved again, so a session changing between
// direct play and transcoding keeps its stream in a full pool.
//
// The store reads the household and its pool itself, inside each Acquire's
// transaction, instead of trusting the request. A request is frozen when its
// session starts; a session reacquired after its lease lapsed, rebuilt, or
// restored after a canceled replacement would otherwise meet the pool its
// session started with: none, for a session from before the household's first
// login or from before a move to a pool, and the old size after the pool was
// lowered.

// HouseholdLimits is the shared pool of the household an account plays in:
// a main account and its household logins draw on the same streams and
// transcodes. ID is the household's main account, 0 when the account plays
// in no pooled household. 0 limits are unlimited.
type HouseholdLimits struct {
	ID            int
	MaxStreams    int
	MaxTranscodes int
}

// householdRowQuerier is a pool, a connection or a transaction.
type householdRowQuerier interface {
	QueryRow(ctx context.Context, sql string, args ...any) pgx.Row
}

// HouseholdPoolForAccount is the pool accountID plays in: its own, as a main
// account with at least one login, or its main account's, as a login. It is
// the zero HouseholdLimits when the account plays in none, so a household of
// one plays as it always has.
func HouseholdPoolForAccount(ctx context.Context, db householdRowQuerier, accountID int) (HouseholdLimits, error) {
	var household HouseholdLimits
	err := db.QueryRow(ctx, `
		SELECT household.main_user_id, household.max_streams, household.max_transcodes
		FROM users AS account
		JOIN bloem_household_pools AS household
		  ON household.main_user_id = COALESCE(account.household_main_user_id, account.id)
		WHERE account.id = $1
		  AND EXISTS (SELECT 1 FROM users AS login WHERE login.household_main_user_id = household.main_user_id)`,
		accountID).Scan(&household.ID, &household.MaxStreams, &household.MaxTranscodes)
	if errors.Is(err, pgx.ErrNoRows) {
		return HouseholdLimits{}, nil
	}
	if err != nil {
		return HouseholdLimits{}, fmt.Errorf("load household pool: %w", err)
	}
	return household, nil
}

// withHousehold names the household from limits in request. Only the
// decision whether a profileless session reserves reads it; the store reads
// the household again.
func withHousehold(request *ReservationRequest, limits SessionLimits) {
	request.HouseholdID = limits.Household.ID
}

// reserveHouseholdCapacity reads the household request's account plays in
// now, takes the household's lock and refuses a start the household's pool
// has no room for. It runs inside Acquire's transaction, after the account
// lock and before the tenant lock, so every contender takes the three in one
// order. The lock holds until the reservation commits. It returns the
// household the reservation counts against, 0 for none.
func reserveHouseholdCapacity(ctx context.Context, tx pgx.Tx, request ReservationRequest) (int, error) {
	household, err := HouseholdPoolForAccount(ctx, tx, request.AccountID)
	if err != nil {
		return 0, err
	}
	if household.ID <= 0 {
		return 0, nil
	}
	if _, err := tx.Exec(ctx, `SELECT pg_advisory_xact_lock(hashtextextended($1, 0))`, "playback-household:"+strconv.Itoa(household.ID)); err != nil {
		return 0, fmt.Errorf("lock playback household capacity: %w", err)
	}
	if household.MaxStreams <= 0 && (!request.IsTranscode || household.MaxTranscodes <= 0) {
		return household.ID, nil
	}
	// The household's reservations are those of its main account and its
	// logins as well as those made in its name: a session that started before
	// the pool applied (the main account playing when its first login was
	// made, or a household moved from shares to a pool) was reserved with no
	// household, and counts all the same.
	var streams, transcodes int
	if err := tx.QueryRow(ctx, `
		SELECT count(*), count(*) FILTER (WHERE is_transcode)
		FROM playback_capacity_reservations
		WHERE session_id <> $2 AND lease_until > now()
		  AND (household_id = $1 OR account_id = $1
		       OR account_id IN (SELECT id FROM users WHERE household_main_user_id = $1))`,
		household.ID, request.SessionID,
	).Scan(&streams, &transcodes); err != nil {
		return 0, fmt.Errorf("count household playback reservations: %w", err)
	}
	if household.MaxStreams > 0 && streams >= household.MaxStreams {
		return 0, ErrHouseholdStreamsExceeded
	}
	if request.IsTranscode && household.MaxTranscodes > 0 && transcodes >= household.MaxTranscodes {
		return 0, ErrHouseholdTranscodesExceeded
	}
	return household.ID, nil
}
