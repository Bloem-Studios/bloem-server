package playback

import (
	"context"
	"errors"
	"testing"
	"time"

	"github.com/jackc/pgx/v5/pgxpool"
)

// householdTestNode is one replica's session manager on the shared Postgres
// reservation store, with limits that name the account's household as the
// production limit provider does.
func householdTestNode(pool *pgxpool.Pool) *SessionManager {
	manager := NewSessionManager(0, 0)
	manager.SetReservationStore(NewPostgresReservationStore(pool), time.Minute)
	manager.SetProfileLimitProvider(func(ctx context.Context, userID int, _ string) (SessionLimits, error) {
		household, err := HouseholdPoolForAccount(ctx, pool, userID)
		return SessionLimits{MaxStreams: 3, MaxTranscodes: 2, Household: household}, err
	})
	return manager
}

func cleanHouseholdReservations(t *testing.T, pool *pgxpool.Pool, h *testHousehold) {
	t.Cleanup(func() {
		accounts := append([]int{h.main}, h.logins...)
		_, _ = pool.Exec(context.Background(), `DELETE FROM playback_capacity_reservations WHERE account_id = ANY($1)`, accounts)
	})
}

// lapseSession lets a session's lease run out on the store and in its
// manager, as it does for a client that sends nothing for longer than the
// lease.
func lapseSession(t *testing.T, pool *pgxpool.Pool, manager *SessionManager, sessionID string) {
	t.Helper()
	if _, err := pool.Exec(context.Background(), `UPDATE playback_capacity_reservations SET lease_until = now() - interval '1 second' WHERE session_id = $1`, sessionID); err != nil {
		t.Fatal(err)
	}
	manager.mu.Lock()
	manager.sessions[sessionID].reservationLeaseUntil = time.Now().Add(-time.Second)
	manager.mu.Unlock()
}

// The review's scenario end to end: the main account transcodes before its
// first login exists, a login is made, the main account pauses past its
// lease, the login starts both of the household's transcodes on another
// replica, and the main account's next progress report resumes its session.
// The resume is refused and the session stopped, instead of a third
// transcode.
func TestResumedSessionMeetsTheHouseholdPoolAsItIsNow(t *testing.T) {
	_, pool := householdPoolTestStore(t)
	h := newTestHousehold(t, pool, 0)
	h.setPool(3, 2)
	cleanHouseholdReservations(t, pool, h)
	mainNode, loginNode := householdTestNode(pool), householdTestNode(pool)

	paused, err := mainNode.StartSession(h.main, "main-profile", 1, PlayTranscode, false)
	if err != nil {
		t.Fatal(err)
	}
	login := h.addLogin()
	lapseSession(t, pool, mainNode, paused.ID)
	for range 2 {
		if _, err := loginNode.StartSession(login, "login-profile", 1, PlayTranscode, false); err != nil {
			t.Fatalf("the login's transcode: %v", err)
		}
	}

	err = mainNode.UpdateProgress(paused.ID, 600, false)
	if !errors.Is(err, ErrHouseholdTranscodesExceeded) {
		t.Fatalf("the main account resuming a third transcode = %v, want the household's transcode limit", err)
	}
	if _, err := mainNode.GetSession(paused.ID); !errors.Is(err, ErrSessionNotFound) {
		t.Fatalf("the refused session is still live: %v", err)
	}
}
