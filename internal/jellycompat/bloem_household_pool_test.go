package jellycompat

import (
	"context"
	"errors"
	"sync"
	"testing"
	"time"

	"github.com/Silo-Server/silo-server/internal/playback"
)

// householdPoolStore is a fleet reservation store that holds a household to
// its pool, as the Postgres store does, and records what it was asked.
type householdPoolStore struct {
	mu       sync.Mutex
	held     map[string]playback.ReservationRequest
	requests []playback.ReservationRequest
}

func (s *householdPoolStore) Acquire(_ context.Context, request playback.ReservationRequest) (playback.Reservation, error) {
	s.mu.Lock()
	defer s.mu.Unlock()
	s.requests = append(s.requests, request)
	if request.HouseholdID > 0 && request.HouseholdStreams > 0 {
		streams := 0
		for id, held := range s.held {
			if id != request.SessionID && held.HouseholdID == request.HouseholdID {
				streams++
			}
		}
		if streams >= request.HouseholdStreams {
			return playback.Reservation{}, playback.ErrHouseholdStreamsExceeded
		}
	}
	if s.held == nil {
		s.held = map[string]playback.ReservationRequest{}
	}
	s.held[request.SessionID] = request
	return playback.Reservation{SessionID: request.SessionID, Generation: int64(len(s.requests)), LeaseUntil: request.LeaseUntil}, nil
}

func (*householdPoolStore) Renew(_ context.Context, sessionID string, generation int64, leaseUntil time.Time) (playback.Reservation, error) {
	return playback.Reservation{SessionID: sessionID, Generation: generation, LeaseUntil: leaseUntil}, nil
}

func (s *householdPoolStore) Release(_ context.Context, sessionID string, _ int64) error {
	s.mu.Lock()
	defer s.mu.Unlock()
	delete(s.held, sessionID)
	return nil
}

// A login signs in to a Jellyfin app as itself, so its compat playback
// starts as the login's own account, reserves against its household's pool,
// and a full pool refuses the next start in the household as the stream limit.
func TestCompatStreamStartCountsAgainstTheHousehold(t *testing.T) {
	const mainID, loginID = 100, 201
	mgr := playback.NewSessionManager(0, 0)
	store := &householdPoolStore{}
	mgr.SetReservationStore(store, time.Minute)
	mgr.SetProfileLimitProvider(func(_ context.Context, userID int, _ string) (playback.SessionLimits, error) {
		if userID != mainID && userID != loginID {
			return playback.SessionLimits{}, nil
		}
		return playback.SessionLimits{MaxStreams: 3, Household: playback.HouseholdLimits{ID: mainID, MaxStreams: 1}}, nil
	})
	store2 := NewPlaybackSessionStore(time.Hour, nil)
	h := &PlaybackHandler{playbackStore: store2, sessionMgr: mgr, tm: keyedTranscodeManager()}
	source := PlaybackMediaSource{ID: "source-1", FileID: 42, Version: testCompatVersion()}
	for _, play := range []string{"play-login", "play-main"} {
		store2.Put(PlaybackSession{ID: play, CompatToken: "token-" + play, ItemID: "movie-1", MediaSources: []PlaybackMediaSource{source}})
	}

	login := &Session{Token: "token-play-login", StreamAppUserID: loginID, ProfileID: "login-profile"}
	if _, err := h.ensureUpstreamPlayback(context.Background(), login, "play-login", source, "direct"); err != nil {
		t.Fatalf("the login's start: %v", err)
	}
	if len(store.requests) != 1 || store.requests[0].AccountID != loginID || store.requests[0].HouseholdID != mainID {
		t.Fatalf("reservations = %+v, want one by the login in the main account's household", store.requests)
	}
	main := &Session{Token: "token-play-main", StreamAppUserID: mainID, ProfileID: "main-profile"}
	_, err := h.ensureUpstreamPlayback(context.Background(), main, "play-main", source, "direct")
	if !errors.Is(err, playback.ErrTooManyStreams) {
		t.Fatalf("the main account's start in a full household = %v, want the stream limit", err)
	}
}
