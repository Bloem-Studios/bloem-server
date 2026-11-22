package playback

import (
	"context"
	"errors"
	"fmt"
	"strconv"
	"sync"
	"sync/atomic"
	"testing"
	"time"

	"github.com/jackc/pgx/v5/pgxpool"
)

// A household's pool, as the fleet reservation keeps it: every account of the
// household counts against the household's streams and transcodes, beside
// its own limits. The store reads the household and its pool itself, inside
// the reservation's transaction, so a start always meets the pool as it is
// now rather than the one its session started with.

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

var householdFixtureSequence atomic.Int64

// testHousehold is a main account and its logins in the test database.
type testHousehold struct {
	t      *testing.T
	pool   *pgxpool.Pool
	main   int
	logins []int
}

// newTestHousehold creates a main account with the given number of logins,
// and no pool.
func newTestHousehold(t *testing.T, pool *pgxpool.Pool, logins int) *testHousehold {
	t.Helper()
	ctx := context.Background()
	name := func(kind string) string {
		return fmt.Sprintf("pool-%s-%d-%d", kind, time.Now().UnixNano(), householdFixtureSequence.Add(1))
	}
	h := &testHousehold{t: t, pool: pool}
	if err := pool.QueryRow(ctx, `INSERT INTO users(username,email,password_hash,role) VALUES($1::text,$1::text||'@example.test','x','user') RETURNING id`,
		name("main")).Scan(&h.main); err != nil {
		t.Fatal(err)
	}
	t.Cleanup(func() {
		_, _ = pool.Exec(context.Background(), `DELETE FROM users WHERE household_main_user_id = $1`, h.main)
		_, _ = pool.Exec(context.Background(), `DELETE FROM users WHERE id = $1`, h.main)
	})
	for range logins {
		h.addLogin()
	}
	return h
}

// account creates an account in no household.
func newTestAccount(t *testing.T, pool *pgxpool.Pool) int {
	t.Helper()
	var id int
	if err := pool.QueryRow(context.Background(), `INSERT INTO users(username,email,password_hash,role) VALUES($1::text,$1::text||'@example.test','x','user') RETURNING id`,
		fmt.Sprintf("pool-plain-%d-%d", time.Now().UnixNano(), householdFixtureSequence.Add(1))).Scan(&id); err != nil {
		t.Fatal(err)
	}
	t.Cleanup(func() { _, _ = pool.Exec(context.Background(), `DELETE FROM users WHERE id = $1`, id) })
	return id
}

func (h *testHousehold) addLogin() int {
	h.t.Helper()
	var id int
	if err := h.pool.QueryRow(context.Background(), `INSERT INTO users(username,email,password_hash,role,household_main_user_id) VALUES($1::text,$1::text||'@example.test','x','user',$2) RETURNING id`,
		fmt.Sprintf("pool-login-%d-%d", time.Now().UnixNano(), householdFixtureSequence.Add(1)), h.main).Scan(&id); err != nil {
		h.t.Fatal(err)
	}
	h.logins = append(h.logins, id)
	return id
}

// setPool gives the household a pool, or changes it.
func (h *testHousehold) setPool(streams, transcodes int) {
	h.t.Helper()
	if _, err := h.pool.Exec(context.Background(), `
		INSERT INTO bloem_household_pools(main_user_id, max_streams, max_transcodes) VALUES ($1, $2, $3)
		ON CONFLICT (main_user_id) DO UPDATE SET max_streams = EXCLUDED.max_streams, max_transcodes = EXCLUDED.max_transcodes`,
		h.main, streams, transcodes); err != nil {
		h.t.Fatal(err)
	}
}

func (h *testHousehold) removePool() {
	h.t.Helper()
	if _, err := h.pool.Exec(context.Background(), `DELETE FROM bloem_household_pools WHERE main_user_id = $1`, h.main); err != nil {
		h.t.Fatal(err)
	}
}

// lapse lets a reservation's lease run out, as it does for a client that
// sends nothing for longer than the lease.
func lapseReservation(t *testing.T, pool *pgxpool.Pool, session string) {
	t.Helper()
	tag, err := pool.Exec(context.Background(), `UPDATE playback_capacity_reservations SET lease_until = now() - interval '1 second' WHERE session_id = $1`, householdPoolTestPrefix+session)
	if err != nil || tag.RowsAffected() != 1 {
		t.Fatalf("lapse %s: rows=%d error=%v", session, tag.RowsAffected(), err)
	}
}

// start is one start by account, with the household its limits named when
// the session started (household 0: none).
func start(session string, account, household int, transcode bool) ReservationRequest {
	return ReservationRequest{
		SessionID: householdPoolTestPrefix + session, AccountID: account, ProfileID: "profile",
		IsTranscode: transcode, HouseholdID: household,
		LeaseUntil: time.Now().Add(time.Minute),
	}
}

func TestHouseholdPoolCountsEveryAccount(t *testing.T) {
	store, pool := householdPoolTestStore(t)
	ctx := context.Background()
	h := newTestHousehold(t, pool, 2)
	h.setPool(2, 0)
	for i, account := range []int{h.main, h.logins[0]} {
		if _, err := store.Acquire(ctx, start(fmt.Sprintf("count-%d", i), account, h.main, false)); err != nil {
			t.Fatalf("account %d: %v", account, err)
		}
	}
	_, err := store.Acquire(ctx, start("count-full", h.logins[1], h.main, false))
	if !errors.Is(err, ErrTooManyStreams) || !errors.Is(err, ErrHouseholdStreamsExceeded) {
		t.Fatalf("third account of a 2-stream household = %v, want the household's stream limit", err)
	}
	other := newTestHousehold(t, pool, 1)
	other.setPool(2, 0)
	if _, err := store.Acquire(ctx, start("count-other", other.logins[0], other.main, false)); err != nil {
		t.Fatalf("another household: %v", err)
	}
}

func TestHouseholdPoolTranscodes(t *testing.T) {
	store, pool := householdPoolTestStore(t)
	ctx := context.Background()
	h := newTestHousehold(t, pool, 1)
	h.setPool(3, 1)
	if _, err := store.Acquire(ctx, start("transcode-a", h.main, h.main, true)); err != nil {
		t.Fatal(err)
	}
	_, err := store.Acquire(ctx, start("transcode-b", h.logins[0], h.main, true))
	if !errors.Is(err, ErrTooManyTranscodes) || !errors.Is(err, ErrHouseholdTranscodesExceeded) {
		t.Fatalf("second transcode of a 1-transcode household = %v", err)
	}
	if _, err := store.Acquire(ctx, start("transcode-direct", h.logins[0], h.main, false)); err != nil {
		t.Fatalf("direct play beside the household's transcode: %v", err)
	}
}

// Two logins on two replicas starting on the household's last stream: one
// plays.
func TestHouseholdPoolIsAtomicAcrossConnections(t *testing.T) {
	_, pool := householdPoolTestStore(t)
	ctx := context.Background()
	h := newTestHousehold(t, pool, 2)
	h.setPool(1, 0)
	stores := []ReservationStore{NewPostgresReservationStore(pool), NewPostgresReservationStore(pool)}
	begin := make(chan struct{})
	results := make(chan error, len(stores))
	var wg sync.WaitGroup
	for index, store := range stores {
		wg.Add(1)
		go func(index int, store ReservationStore) {
			defer wg.Done()
			<-begin
			_, err := store.Acquire(ctx, start(fmt.Sprintf("race-%d", index), h.logins[index], h.main, false))
			results <- err
		}(index, store)
	}
	close(begin)
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
	store, pool := householdPoolTestStore(t)
	ctx := context.Background()
	h := newTestHousehold(t, pool, 1)
	h.setPool(2, 1)
	for i, account := range []int{h.main, h.logins[0]} {
		if _, err := store.Acquire(ctx, start(fmt.Sprintf("own-%d", i), account, h.main, false)); err != nil {
			t.Fatal(err)
		}
	}
	if _, err := store.Acquire(ctx, start("own-0", h.main, h.main, true)); err != nil {
		t.Fatalf("switching a session of a full household to transcoding: %v", err)
	}
}

// A login's own cap still holds inside the pool, as its own limit.
func TestLoginCapInsideThePool(t *testing.T) {
	store, pool := householdPoolTestStore(t)
	ctx := context.Background()
	h := newTestHousehold(t, pool, 1)
	h.setPool(3, 0)
	first := start("cap-a", h.logins[0], h.main, false)
	first.AccountStreams = 1
	if _, err := store.Acquire(ctx, first); err != nil {
		t.Fatal(err)
	}
	second := start("cap-b", h.logins[0], h.main, false)
	second.AccountStreams = 1
	_, err := store.Acquire(ctx, second)
	if !errors.Is(err, ErrTooManyStreams) || errors.Is(err, ErrHouseholdStreamsExceeded) {
		t.Fatalf("a capped login's second stream = %v, want its own stream limit", err)
	}
}

func TestHouseholdPoolZeroIsUnlimited(t *testing.T) {
	store, pool := householdPoolTestStore(t)
	ctx := context.Background()
	h := newTestHousehold(t, pool, 1)
	h.setPool(0, 0)
	for i := range 4 {
		if _, err := store.Acquire(ctx, start(fmt.Sprintf("unlimited-%d", i), h.logins[0], h.main, true)); err != nil {
			t.Fatalf("start %d in an unlimited household: %v", i, err)
		}
	}
}

// The reservation records the household it counted against: the main
// account's id, or 0 for a start in no pooled household. A main account with
// a pool but no login yet plays as a household of one, outside any pool.
func TestReservationRecordsItsHousehold(t *testing.T) {
	store, pool := householdPoolTestStore(t)
	ctx := context.Background()
	h := newTestHousehold(t, pool, 1)
	h.setPool(3, 2)
	lonely := newTestHousehold(t, pool, 0)
	lonely.setPool(1, 1)
	plain := newTestAccount(t, pool)
	for _, tc := range []struct {
		session string
		request ReservationRequest
		want    int64
	}{
		{"records-login", start("records-login", h.logins[0], h.main, false), int64(h.main)},
		{"records-main", start("records-main", h.main, 0, false), int64(h.main)},
		{"records-lonely", start("records-lonely", lonely.main, 0, true), 0},
		{"records-lonely-2", start("records-lonely-2", lonely.main, 0, true), 0},
		{"records-plain", start("records-plain", plain, h.main, false), 0},
	} {
		if _, err := store.Acquire(ctx, tc.request); err != nil {
			t.Fatalf("%s: %v", tc.session, err)
		}
		var household int64
		if err := pool.QueryRow(ctx, `SELECT household_id FROM playback_capacity_reservations WHERE session_id=$1`, tc.request.SessionID).Scan(&household); err != nil || household != tc.want {
			t.Fatalf("%s: household_id=%d error=%v, want %d", tc.session, household, err, tc.want)
		}
	}
}

// Sessions that started before the household's pool applied (the main
// account playing when its first login is made, or a household moved from
// shares to a pool) were reserved without a household. They are the
// household's all the same, and count against its pool.
func TestHouseholdPoolCountsSessionsFromBeforeThePool(t *testing.T) {
	store, pool := householdPoolTestStore(t)
	ctx := context.Background()
	h := newTestHousehold(t, pool, 1)

	// The main account and the login each playing from before the pool.
	for i, account := range []int{h.main, h.logins[0]} {
		if _, err := store.Acquire(ctx, start(fmt.Sprintf("before-%d", i), account, 0, false)); err != nil {
			t.Fatal(err)
		}
	}
	for _, tc := range []struct {
		session string
		account int
		streams int
	}{
		{"before-main-full", h.logins[0], 1}, // the main account's earlier stream fills a 1-stream pool
		{"before-both-full", h.main, 2},      // and both earlier streams a 2-stream one
	} {
		h.setPool(tc.streams, 0)
		_, err := store.Acquire(ctx, start(tc.session, tc.account, h.main, false))
		if !errors.Is(err, ErrHouseholdStreamsExceeded) {
			t.Fatalf("a start in a %d-stream pool with 2 earlier household streams = %v, want the household's limit", tc.streams, err)
		}
	}
	h.setPool(3, 0)
	if _, err := store.Acquire(ctx, start("before-room", h.logins[0], h.main, false)); err != nil {
		t.Fatalf("a start in a 3-stream pool with 2 earlier streams: %v", err)
	}
}

// The review's scenario: the main account transcodes before its first login
// exists, a login is made, the main account pauses past its lease, the login
// starts both of the household's transcodes, and the main account resumes.
// Its session still carries "no household" from its start; resuming it must
// meet the pool as it is now.
func TestHouseholdPoolHoldsWhenASessionFromBeforeThePoolResumes(t *testing.T) {
	store, pool := householdPoolTestStore(t)
	ctx := context.Background()
	h := newTestHousehold(t, pool, 0)
	h.setPool(3, 2)
	paused := start("resume-main", h.main, 0, true)
	if _, err := store.Acquire(ctx, paused); err != nil {
		t.Fatal(err)
	}
	login := h.addLogin()
	lapseReservation(t, pool, "resume-main")
	for i := range 2 {
		if _, err := store.Acquire(ctx, start("resume-login-"+strconv.Itoa(i), login, h.main, true)); err != nil {
			t.Fatalf("login transcode %d: %v", i, err)
		}
	}
	paused.LeaseUntil = time.Now().Add(time.Minute)
	_, err := store.Acquire(ctx, paused)
	if !errors.Is(err, ErrHouseholdTranscodesExceeded) {
		t.Fatalf("main account resuming a third transcode in a 2-transcode household = %v, want the household's transcode limit", err)
	}
}

// A pool lowered while a session was paused holds when it resumes.
func TestHouseholdPoolLoweredWhileAPausedSessionWaits(t *testing.T) {
	store, pool := householdPoolTestStore(t)
	ctx := context.Background()
	h := newTestHousehold(t, pool, 1)
	h.setPool(3, 2)
	paused := start("lowered-main", h.main, h.main, true)
	if _, err := store.Acquire(ctx, paused); err != nil {
		t.Fatal(err)
	}
	if _, err := store.Acquire(ctx, start("lowered-login", h.logins[0], h.main, true)); err != nil {
		t.Fatal(err)
	}
	h.setPool(3, 1)
	lapseReservation(t, pool, "lowered-main")
	paused.LeaseUntil = time.Now().Add(time.Minute)
	_, err := store.Acquire(ctx, paused)
	if !errors.Is(err, ErrHouseholdTranscodesExceeded) {
		t.Fatalf("resuming a transcode after the pool went down to 1 = %v, want the household's transcode limit", err)
	}
}

// A household moved from a pool to shares no longer counts against the pool
// it left: each account's own limit (its share) is what holds.
func TestHouseholdPoolRemovedWhileAPausedSessionWaits(t *testing.T) {
	store, pool := householdPoolTestStore(t)
	ctx := context.Background()
	h := newTestHousehold(t, pool, 1)
	h.setPool(1, 0)
	paused := start("removed-main", h.main, h.main, false)
	if _, err := store.Acquire(ctx, paused); err != nil {
		t.Fatal(err)
	}
	lapseReservation(t, pool, "removed-main")
	if _, err := store.Acquire(ctx, start("removed-login", h.logins[0], h.main, false)); err != nil {
		t.Fatal(err)
	}
	h.removePool()
	paused.LeaseUntil = time.Now().Add(time.Minute)
	if _, err := store.Acquire(ctx, paused); err != nil {
		t.Fatalf("resuming after the household left its pool: %v", err)
	}
}

// A household's pool applies to its main account and to every login of it,
// and only while the main account has a login: a household of one plays as
// it always has.
func TestHouseholdPoolForAccount(t *testing.T) {
	_, pool := householdPoolTestStore(t)
	ctx := context.Background()
	h := newTestHousehold(t, pool, 1)
	h.setPool(2, 1)
	lonely := newTestHousehold(t, pool, 0)
	lonely.setPool(4, 4)
	plain := newTestAccount(t, pool)
	for _, account := range []int{h.main, h.logins[0]} {
		got, err := HouseholdPoolForAccount(ctx, pool, account)
		if want := (HouseholdLimits{ID: h.main, MaxStreams: 2, MaxTranscodes: 1}); err != nil || got != want {
			t.Fatalf("account %d: pool %+v error=%v, want %+v", account, got, err, want)
		}
	}
	for name, account := range map[string]int{"main without logins": lonely.main, "account without a pool": plain} {
		if got, err := HouseholdPoolForAccount(ctx, pool, account); err != nil || got != (HouseholdLimits{}) {
			t.Fatalf("%s: pool %+v error=%v; want none", name, got, err)
		}
	}
}
