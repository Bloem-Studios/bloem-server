package playback

import (
	"context"
	"fmt"
	"strconv"

	"github.com/jackc/pgx/v5"
)

// A household's shared pool in the fleet reservation (Bloem separate logins).
// A household is a main account and its household logins; when a storefront
// gives it a pool (internal/auth/bloem_household_pool.go), every start by any
// of its accounts reserves against the household's streams and transcodes,
// beside the account's own limits and before an optional tenant pool. It
// counts every live reservation of the household's accounts. Only
// the fleet reservation counts it: production always installs one, and it
// already leaves out the session being reserved again, so a session changing
// between direct play and transcoding keeps its stream in a full pool.

// HouseholdLimits is the shared pool of the household an account plays in:
// a main account and its household logins draw on the same streams and
// transcodes (internal/auth/bloem_household_pool.go). ID is the household's
// main account, 0 when the account plays in no pooled household. 0 limits
// are unlimited.
type HouseholdLimits struct {
	ID            int
	MaxStreams    int
	MaxTranscodes int
}

// withHousehold copies the household's pool from limits into request.
func withHousehold(request *ReservationRequest, limits SessionLimits) {
	request.HouseholdID = limits.Household.ID
	request.HouseholdStreams = limits.Household.MaxStreams
	request.HouseholdTranscodes = limits.Household.MaxTranscodes
}

// reserveHouseholdCapacity takes the household's lock and refuses a start the
// household's pool has no room for. It runs inside Acquire's transaction,
// after the account lock and before the tenant lock, so every contender takes
// the three in one order. The lock holds until the reservation commits.
func reserveHouseholdCapacity(ctx context.Context, tx pgx.Tx, request ReservationRequest) error {
	if request.HouseholdID <= 0 {
		return nil
	}
	if _, err := tx.Exec(ctx, `SELECT pg_advisory_xact_lock(hashtextextended($1, 0))`, "playback-household:"+strconv.Itoa(request.HouseholdID)); err != nil {
		return fmt.Errorf("lock playback household capacity: %w", err)
	}
	if request.HouseholdStreams <= 0 && (!request.IsTranscode || request.HouseholdTranscodes <= 0) {
		return nil
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
		request.HouseholdID, request.SessionID,
	).Scan(&streams, &transcodes); err != nil {
		return fmt.Errorf("count household playback reservations: %w", err)
	}
	if request.HouseholdStreams > 0 && streams >= request.HouseholdStreams {
		return ErrHouseholdStreamsExceeded
	}
	if request.IsTranscode && request.HouseholdTranscodes > 0 && transcodes >= request.HouseholdTranscodes {
		return ErrHouseholdTranscodesExceeded
	}
	return nil
}
