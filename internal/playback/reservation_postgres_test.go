package playback

import (
	"context"
	"errors"
	"os"
	"sync"
	"testing"
	"time"

	"github.com/jackc/pgx/v5/pgxpool"
)

func playbackReservationTestPool(t *testing.T) *pgxpool.Pool {
	t.Helper()
	dsn := os.Getenv("SILO_TEST_DATABASE_URL")
	if dsn == "" {
		if os.Getenv("SILO_REQUIRE_TEST_DATABASE") == "1" {
			t.Fatal("SILO_TEST_DATABASE_URL is required")
		}
		t.Skip("SILO_TEST_DATABASE_URL is not set")
	}
	pool, err := pgxpool.New(context.Background(), dsn)
	if err != nil {
		t.Fatal(err)
	}
	t.Cleanup(pool.Close)
	return pool
}

func TestFleetReservationTenantLimitIsAtomicAcrossConnections(t *testing.T) {
	pool := playbackReservationTestPool(t)
	ctx := context.Background()
	if _, err := pool.Exec(ctx, `DELETE FROM playback_capacity_reservations WHERE tenant_id = 'reservation-test-tenant'`); err != nil {
		t.Fatal(err)
	}
	t.Cleanup(func() {
		_, _ = pool.Exec(context.Background(), `DELETE FROM playback_capacity_reservations WHERE tenant_id = 'reservation-test-tenant'`)
	})

	stores := []ReservationStore{NewPostgresReservationStore(pool), NewPostgresReservationStore(pool)}
	start := make(chan struct{})
	results := make(chan error, len(stores))
	var wg sync.WaitGroup
	for index, store := range stores {
		wg.Add(1)
		go func(index int, store ReservationStore) {
			defer wg.Done()
			<-start
			_, err := store.Acquire(ctx, ReservationRequest{
				SessionID:        "reservation-test-session-" + string(rune('a'+index)),
				AccountID:        100 + index,
				ProfileID:        "profile",
				TenantID:         "reservation-test-tenant",
				IsTranscode:      true,
				TenantTranscodes: 1,
				LeaseUntil:       time.Now().Add(time.Minute),
			})
			results <- err
		}(index, store)
	}
	close(start)
	wg.Wait()
	close(results)

	var admitted, denied int
	for err := range results {
		switch {
		case err == nil:
			admitted++
		case errors.Is(err, ErrTenantTranscodesExceeded):
			denied++
		default:
			t.Fatalf("Acquire error = %v", err)
		}
	}
	if admitted != 1 || denied != 1 {
		t.Fatalf("admitted=%d denied=%d, want 1/1", admitted, denied)
	}
}

func TestFleetReservationStaleReleaseCannotDeleteNewGeneration(t *testing.T) {
	pool := playbackReservationTestPool(t)
	ctx := context.Background()
	store := NewPostgresReservationStore(pool)
	request := ReservationRequest{
		SessionID:      "reservation-test-stale-release",
		AccountID:      301,
		ProfileID:      "profile",
		IsTranscode:    false,
		AccountStreams: 1,
		LeaseUntil:     time.Now().Add(time.Minute),
	}
	first, err := store.Acquire(ctx, request)
	if err != nil {
		t.Fatal(err)
	}
	second, err := store.Acquire(ctx, request)
	if err != nil {
		t.Fatal(err)
	}
	if second.Generation <= first.Generation {
		t.Fatalf("generations = %d then %d", first.Generation, second.Generation)
	}
	if err := store.Release(ctx, first.SessionID, first.Generation); !errors.Is(err, ErrReservationGenerationMismatch) {
		t.Fatalf("stale Release = %v", err)
	}
	if _, err := store.Renew(ctx, second.SessionID, second.Generation, time.Now().Add(time.Minute)); err != nil {
		t.Fatalf("winner was deleted by stale release: %v", err)
	}
	if err := store.Release(ctx, second.SessionID, second.Generation); err != nil {
		t.Fatal(err)
	}
}

func TestFleetReservationGenerationDoesNotResetAfterExpiry(t *testing.T) {
	pool := playbackReservationTestPool(t)
	ctx := context.Background()
	store := NewPostgresReservationStore(pool)
	request := ReservationRequest{
		SessionID:      "reservation-test-expired-generation",
		AccountID:      302,
		ProfileID:      "profile",
		AccountStreams: 1,
		LeaseUntil:     time.Now().Add(20 * time.Millisecond),
	}
	first, err := store.Acquire(ctx, request)
	if err != nil {
		t.Fatal(err)
	}
	time.Sleep(30 * time.Millisecond)
	request.LeaseUntil = time.Now().Add(time.Minute)
	second, err := store.Acquire(ctx, request)
	if err != nil {
		t.Fatal(err)
	}
	if second.Generation <= first.Generation {
		t.Fatalf("generation reset after expiry: %d then %d", first.Generation, second.Generation)
	}
	if err := store.Release(ctx, first.SessionID, first.Generation); !errors.Is(err, ErrReservationGenerationMismatch) {
		t.Fatalf("expired generation released successor: %v", err)
	}
	if _, err := store.Renew(ctx, second.SessionID, second.Generation, time.Now().Add(time.Minute)); err != nil {
		t.Fatalf("successor missing after stale release: %v", err)
	}
	if err := store.Release(ctx, second.SessionID, second.Generation); err != nil {
		t.Fatal(err)
	}
}

func TestFleetReservationDirectToTranscodeUpgradeHonorsOtherNode(t *testing.T) {
	pool := playbackReservationTestPool(t)
	ctx := context.Background()
	storeA := NewPostgresReservationStore(pool)
	storeB := NewPostgresReservationStore(pool)
	_, _ = pool.Exec(ctx, `DELETE FROM playback_capacity_reservations WHERE session_id IN ('reservation-test-upgrade-direct', 'reservation-test-upgrade-other')`)
	t.Cleanup(func() {
		_, _ = pool.Exec(context.Background(), `DELETE FROM playback_capacity_reservations WHERE session_id IN ('reservation-test-upgrade-direct', 'reservation-test-upgrade-other')`)
	})
	deadline := time.Now().Add(time.Minute)
	direct, err := storeA.Acquire(ctx, ReservationRequest{
		SessionID: "reservation-test-upgrade-direct", AccountID: 401, ProfileID: "profile-a",
		AccountStreams: 2, AccountTranscodes: 1, LeaseUntil: deadline,
	})
	if err != nil {
		t.Fatal(err)
	}
	other, err := storeB.Acquire(ctx, ReservationRequest{
		SessionID: "reservation-test-upgrade-other", AccountID: 401, ProfileID: "profile-b",
		IsTranscode: true, AccountStreams: 2, AccountTranscodes: 1, LeaseUntil: deadline,
	})
	if err != nil {
		t.Fatal(err)
	}
	_, err = storeA.Acquire(ctx, ReservationRequest{
		SessionID: direct.SessionID, AccountID: 401, ProfileID: "profile-a",
		IsTranscode: true, AccountStreams: 2, AccountTranscodes: 1, LeaseUntil: deadline,
	})
	if !errors.Is(err, ErrTooManyTranscodes) {
		t.Fatalf("direct-to-transcode upgrade = %v", err)
	}
	if err := storeA.Release(ctx, direct.SessionID, direct.Generation); err != nil {
		t.Fatal(err)
	}
	if err := storeB.Release(ctx, other.SessionID, other.Generation); err != nil {
		t.Fatal(err)
	}
}
