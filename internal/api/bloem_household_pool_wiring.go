package api

import (
	"context"

	"github.com/jackc/pgx/v5/pgxpool"

	"github.com/Silo-Server/silo-server/internal/auth"
	"github.com/Silo-Server/silo-server/internal/playback"
)

// A household's shared pool reaches playback admission here: the limit
// provider adds the pool of the household the account plays in, and the fleet
// reservation counts every account of the household against it
// (internal/playback/bloem_household_reservation.go).

// householdPoolResolver answers which pool an account plays in.
type householdPoolResolver interface {
	HouseholdPoolLimits(ctx context.Context, accountID int) (playback.HouseholdLimits, error)
}

// withHouseholdPoolLimits adds the account's household pool to next's
// limits. A pool that cannot be read refuses the start, as a tenant lookup
// does: playing unmetered would overrun the household's pool.
func withHouseholdPoolLimits(next playback.ProfileSessionLimitProvider, pools householdPoolResolver) playback.ProfileSessionLimitProvider {
	if pools == nil {
		return next
	}
	return func(ctx context.Context, userID int, profileID string) (playback.SessionLimits, error) {
		limits, err := next(ctx, userID, profileID)
		if err != nil {
			return playback.SessionLimits{}, err
		}
		household, err := pools.HouseholdPoolLimits(ctx, userID)
		if err != nil {
			return playback.SessionLimits{}, err
		}
		limits.Household = household
		return limits, nil
	}
}

// databaseHouseholdPools reads pools from bloem_household_pools.
type databaseHouseholdPools struct{ pool *pgxpool.Pool }

func (d databaseHouseholdPools) HouseholdPoolLimits(ctx context.Context, accountID int) (playback.HouseholdLimits, error) {
	household, found, err := auth.HouseholdPoolForAccount(ctx, d.pool, accountID)
	if err != nil || !found {
		return playback.HouseholdLimits{}, err
	}
	return playback.HouseholdLimits{ID: household.MainUserID, MaxStreams: household.MaxStreams, MaxTranscodes: household.MaxTranscodes}, nil
}
