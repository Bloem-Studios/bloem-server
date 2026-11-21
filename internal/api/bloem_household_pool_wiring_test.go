package api

import (
	"context"
	"errors"
	"testing"

	"github.com/Silo-Server/silo-server/internal/playback"
)

type householdPoolResolverStub struct {
	limits playback.HouseholdLimits
	err    error
	asked  []int
}

func (s *householdPoolResolverStub) HouseholdPoolLimits(_ context.Context, accountID int) (playback.HouseholdLimits, error) {
	s.asked = append(s.asked, accountID)
	return s.limits, s.err
}

func TestHouseholdPoolLimitsWrapTheProvider(t *testing.T) {
	next := func(context.Context, int, string) (playback.SessionLimits, error) {
		return playback.SessionLimits{MaxStreams: 1, MaxTranscodes: 1, TenantID: "tenant"}, nil
	}
	pools := &householdPoolResolverStub{limits: playback.HouseholdLimits{ID: 5, MaxStreams: 3, MaxTranscodes: 2}}
	limits, err := withHouseholdPoolLimits(next, pools)(context.Background(), 7, "profile")
	if err != nil {
		t.Fatal(err)
	}
	want := playback.SessionLimits{MaxStreams: 1, MaxTranscodes: 1, TenantID: "tenant", Household: playback.HouseholdLimits{ID: 5, MaxStreams: 3, MaxTranscodes: 2}}
	if limits != want || len(pools.asked) != 1 || pools.asked[0] != 7 {
		t.Fatalf("limits = %+v asked %v, want %+v for account 7", limits, pools.asked, want)
	}
}

// A pool that cannot be read refuses the start, as a tenant lookup does:
// playing unmetered would overrun the household's pool.
func TestHouseholdPoolLimitsFailClosed(t *testing.T) {
	next := func(context.Context, int, string) (playback.SessionLimits, error) {
		return playback.SessionLimits{MaxStreams: 1}, nil
	}
	lookup := errors.New("database is down")
	if _, err := withHouseholdPoolLimits(next, &householdPoolResolverStub{err: lookup})(context.Background(), 7, "profile"); !errors.Is(err, lookup) {
		t.Fatalf("error = %v, want the lookup error", err)
	}
}
