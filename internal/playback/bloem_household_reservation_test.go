package playback

import (
	"context"
	"errors"
	"fmt"
	"strconv"
	"sync"
	"testing"
	"time"

	"github.com/jackc/pgx/v5/pgxpool"
)

// A household's pool, as the fleet reservation keeps it: every account of the
// household counts against the household's streams and transcodes, beside
// its own limits.

const householdPoolTestPrefix = "household-pool-test-"

func householdPoolTestStore(t *testing.T) (*PostgresReservationStore, *pgxpool.Pool) {
	t.Helper()
	pool := playbackReservationTestPool(t)
	clean := func() {
		_, _ = pool.Exec(context.Background(), `DELETE FROM playback_capacity_reservations WHERE session_id LIKE $1`, householdPoolTestPrefix+"%")
	}
	clean()
	t.Cleanup(clean)
	return NewPostgresReservationStore(pool), pool
}

// householdRequest is one start by account in household with the given pool.
func householdRequest(session string, account, household, streams, transcodes int, transcode bool) ReservationRequest {
	return ReservationRequest{
		SessionID: householdPoolTestPrefix + session, AccountID: account, ProfileID: "profile",
		IsTranscode: transcode, HouseholdID: household, HouseholdStreams: streams, HouseholdTranscodes: transcodes,
		LeaseUntil: time.Now().Add(time.Minute),
	}
}

func TestHouseholdPoolCountsEveryAccount(t *testing.T) {
	store, _ := householdPoolTestStore(t)
	ctx := context.Background()
	for i, account := range []int{901, 902} {
		if _, err := store.Acquire(ctx, householdRequest(fmt.Sprintf("count-%d", i), account, 900001, 2, 0, false)); err != nil {
			t.Fatalf("account %d: %v", account, err)
		}
	}
	_, err := store.Acquire(ctx, householdRequest("count-full", 903, 900001, 2, 0, false))
	if !errors.Is(err, ErrTooManyStreams) || !errors.Is(err, ErrHouseholdStreamsExceeded) {
		t.Fatalf("third account of a 2-stream household = %v, want the household's stream limit", err)
	}
	if _, err := store.Acquire(ctx, householdRequest("count-other", 904, 900002, 2, 0, false)); err != nil {
		t.Fatalf("another household: %v", err)
	}
}

func TestHouseholdPoolTranscodes(t *testing.T) {
	store, _ := householdPoolTestStore(t)
	ctx := context.Background()
	if _, err := store.Acquire(ctx, householdRequest("transcode-a", 911, 900001, 3, 1, true)); err != nil {
		t.Fatal(err)
	}
	_, err := store.Acquire(ctx, householdRequest("transcode-b", 912, 900001, 3, 1, true))
	if !errors.Is(err, ErrTooManyTranscodes) || !errors.Is(err, ErrHouseholdTranscodesExceeded) {
		t.Fatalf("second transcode of a 1-transcode household = %v", err)
	}
	if _, err := store.Acquire(ctx, householdRequest("transcode-direct", 912, 900001, 3, 1, false)); err != nil {
		t.Fatalf("direct play beside the household's transcode: %v", err)
	}
}

// Two logins on two replicas starting on the household's last stream: one
// plays.
func TestHouseholdPoolIsAtomicAcrossConnections(t *testing.T) {
	_, pool := householdPoolTestStore(t)
	ctx := context.Background()
	stores := []ReservationStore{NewPostgresReservationStore(pool), NewPostgresReservationStore(pool)}
	start := make(chan struct{})
	results := make(chan error, len(stores))
	var wg sync.WaitGroup
	for index, store := range stores {
		wg.Add(1)
		go func(index int, store ReservationStore) {
			defer wg.Done()
			<-start
			_, err := store.Acquire(ctx, householdRequest(fmt.Sprintf("race-%d", index), 921+index, 900001, 1, 0, false))
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
		case errors.Is(err, ErrHouseholdStreamsExceeded):
			denied++
		default:
			t.Fatalf("Acquire error = %v", err)
		}
	}
	if admitted != 1 || denied != 1 {
		t.Fatalf("admitted=%d denied=%d, want 1/1", admitted, denied)
	}
}

// A session moving between direct play and transcoding inside a full pool
// keeps its own stream.
func TestHouseholdPoolReacquireKeepsOwnStream(t *testing.T) {
	store, _ := householdPoolTestStore(t)
	ctx := context.Background()
	for i, account := range []int{931, 932} {
		if _, err := store.Acquire(ctx, householdRequest(fmt.Sprintf("own-%d", i), account, 900001, 2, 1, false)); err != nil {
			t.Fatal(err)
		}
	}
	if _, err := store.Acquire(ctx, householdRequest("own-0", 931, 900001, 2, 1, true)); err != nil {
		t.Fatalf("switching a session of a full household to transcoding: %v", err)
	}
}

// A login's own cap still holds inside the pool, as its own limit.
func TestLoginCapInsideThePool(t *testing.T) {
	store, _ := householdPoolTestStore(t)
	ctx := context.Background()
	first := householdRequest("cap-a", 941, 900001, 3, 0, false)
	first.AccountStreams = 1
	if _, err := store.Acquire(ctx, first); err != nil {
		t.Fatal(err)
	}
	second := householdRequest("cap-b", 941, 900001, 3, 0, false)
	second.AccountStreams = 1
	_, err := store.Acquire(ctx, second)
	if !errors.Is(err, ErrTooManyStreams) || errors.Is(err, ErrHouseholdStreamsExceeded) {
		t.Fatalf("a capped login's second stream = %v, want its own stream limit", err)
	}
}

func TestHouseholdPoolZeroIsUnlimited(t *testing.T) {
	store, _ := householdPoolTestStore(t)
	ctx := context.Background()
	for i := range 4 {
		if _, err := store.Acquire(ctx, householdRequest(fmt.Sprintf("unlimited-%d", i), 951+i, 900001, 0, 0, true)); err != nil {
			t.Fatalf("start %d in an unlimited household: %v", i, err)
		}
	}
}

// A start in no pooled household is stored with household 0 and never
// counts against a household.
func TestNoHouseholdStoresZero(t *testing.T) {
	store, pool := householdPoolTestStore(t)
	ctx := context.Background()
	if _, err := store.Acquire(ctx, householdRequest("none-full", 961, 900001, 1, 0, false)); err != nil {
		t.Fatal(err)
	}
	request := householdRequest("none-plain", 962, 0, 0, 0, false)
	if _, err := store.Acquire(ctx, request); err != nil {
		t.Fatalf("a start in no household: %v", err)
	}
	var household int64
	if err := pool.QueryRow(ctx, `SELECT household_id FROM playback_capacity_reservations WHERE session_id=$1`, request.SessionID).Scan(&household); err != nil || household != 0 {
		t.Fatalf("household_id=%d error=%v, want 0", household, err)
	}
	if err := pool.QueryRow(ctx, `SELECT household_id FROM playback_capacity_reservations WHERE session_id=$1`, householdPoolTestPrefix+"none-full").Scan(&household); err != nil || household != 900001 {
		t.Fatalf("household_id=%d error=%v, want 900001", household, err)
	}
}

// Sessions that started before the household's pool applied (the main
// account playing when its first login is made, or a household moved from
// shares to a pool) were reserved without a household. They are the
// household's all the same, and count against its pool.
func TestHouseholdPoolCountsSessionsFromBeforeThePool(t *testing.T) {
	store, pool := householdPoolTestStore(t)
	ctx := context.Background()
	suffix := strconv.FormatInt(time.Now().UnixNano(), 10)
	var mainID, loginID int
	if err := pool.QueryRow(ctx, `INSERT INTO users(username,email,password_hash,role) VALUES($1::text,$1::text||'@example.test','x','user') RETURNING id`,
		"pool-main-"+suffix).Scan(&mainID); err != nil {
		t.Fatal(err)
	}
	if err := pool.QueryRow(ctx, `INSERT INTO users(username,email,password_hash,role,household_main_user_id) VALUES($1::text,$1::text||'@example.test','x','user',$2) RETURNING id`,
		"pool-login-"+suffix, mainID).Scan(&loginID); err != nil {
		t.Fatal(err)
	}
	t.Cleanup(func() {
		_, _ = pool.Exec(context.Background(), `DELETE FROM users WHERE id = $1`, loginID)
		_, _ = pool.Exec(context.Background(), `DELETE FROM users WHERE id = $1`, mainID)
	})

	// The main account and the login each playing from before the pool.
	for i, account := range []int{mainID, loginID} {
		if _, err := store.Acquire(ctx, householdRequest(fmt.Sprintf("before-%d", i), account, 0, 0, 0, false)); err != nil {
			t.Fatal(err)
		}
	}
	for _, tc := range []struct {
		session string
		account int
		streams int
	}{
		{"before-main-full", loginID, 1}, // the main account's earlier stream fills a 1-stream pool
		{"before-both-full", mainID, 2},  // and both earlier streams a 2-stream one
	} {
		_, err := store.Acquire(ctx, householdRequest(tc.session, tc.account, mainID, tc.streams, 0, false))
		if !errors.Is(err, ErrHouseholdStreamsExceeded) {
			t.Fatalf("a start in a %d-stream pool with 2 earlier household streams = %v, want the household's limit", tc.streams, err)
		}
	}
	if _, err := store.Acquire(ctx, householdRequest("before-room", loginID, mainID, 3, 0, false)); err != nil {
		t.Fatalf("a start in a 3-stream pool with 2 earlier streams: %v", err)
	}
}
