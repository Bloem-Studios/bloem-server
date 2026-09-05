package planstore

import (
	"context"
	"errors"
	"testing"
	"time"

	"github.com/google/uuid"

	"github.com/Silo-Server/silo-server/internal/playback"
)

func TestOwnerLeasePreparingToActiveAndDatabaseTimestamp(t *testing.T) {
	f := newPlanstoreFixture(t)
	store := NewPostgres(f.pool)
	request := authorityRequest(f)
	reserved, err := store.ReserveAttempt(t.Context(), request)
	if err != nil {
		t.Fatal(err)
	}
	for _, state := range []playback.AttemptAuthorityStateV3{playback.AttemptPreparingV3, playback.AttemptActiveV3} {
		if state == playback.AttemptActiveV3 {
			record := f.attemptRecord(uuid.NewString(), request.PlaybackAttemptID, request.RequestDigest)
			if err := store.PublishAttempt(t.Context(), reserved.Authority, record); err != nil {
				t.Fatal(err)
			}
		}
		var before, after, persisted time.Time
		if err := f.pool.QueryRow(t.Context(), `SELECT clock_timestamp()`).Scan(&before); err != nil {
			t.Fatal(err)
		}
		// The supplied timestamp and state are not permission. The live row is.
		input := reserved.Authority
		input.LeaseExpiresAt = time.Time{}
		lease, err := store.RenewAttemptLease(t.Context(), input, 2*time.Minute)
		if err != nil {
			t.Fatal(err)
		}
		if err := f.pool.QueryRow(t.Context(), `SELECT clock_timestamp(), control_lease_expires_at FROM playback_v3_attempts WHERE playback_attempt_id=$1`, request.PlaybackAttemptID).Scan(&after, &persisted); err != nil {
			t.Fatal(err)
		}
		if lease.Authority.State != state || (lease.Authority.PlaybackAttemptID != reserved.Authority.PlaybackAttemptID || lease.Authority.OwnerID != reserved.Authority.OwnerID || lease.Authority.Incarnation != reserved.Authority.Incarnation || lease.Authority.Epoch != reserved.Authority.Epoch) || lease.IssuedAt.Before(before) || lease.IssuedAt.After(after) || !lease.Authority.LeaseExpiresAt.Equal(persisted) {
			t.Fatalf("renewed lease not bound to current row/database clock: %+v, bounds=%v..%v persisted=%v", lease, before, after, persisted)
		}
		if interval := lease.Authority.LeaseExpiresAt.Sub(lease.IssuedAt); interval != 2*time.Minute {
			t.Fatalf("lease and issuance do not share one database clock sample: %v", interval)
		}
	}
}

func TestOwnerLeaseShorterRenewalPreservesRecordedMaximum(t *testing.T) {
	f := newPlanstoreFixture(t)
	store := NewPostgres(f.pool)
	reserved, err := store.ReserveAttempt(t.Context(), authorityRequest(f))
	if err != nil {
		t.Fatal(err)
	}
	lease, err := store.RenewAttemptLease(t.Context(), reserved.Authority, time.Second)
	if err != nil {
		t.Fatal(err)
	}
	if !lease.Authority.LeaseExpiresAt.Equal(reserved.Authority.LeaseExpiresAt) || lease.Authority.LeaseExpiresAt.Sub(lease.IssuedAt) <= time.Second {
		t.Fatalf("short renewal erased recorded lease maximum: initial=%+v renewed=%+v", reserved.Authority, lease)
	}
}

func TestOwnerLeaseRejectsDrainingAndStopped(t *testing.T) {
	for _, state := range []playback.AttemptAuthorityStateV3{playback.AttemptDrainingV3, playback.AttemptStoppedV3} {
		t.Run(string(state), func(t *testing.T) {
			f := newGrantFixture(t)
			f.stage(t)
			if err := f.store.PublishAttempt(t.Context(), f.authority, f.record); err != nil {
				t.Fatal(err)
			}
			if state == playback.AttemptDrainingV3 {
				if _, err := f.store.BeginAttemptDrain(t.Context(), f.authority); err != nil {
					t.Fatal(err)
				}
			} else if err := f.store.StopAttempt(t.Context(), f.authority); err != nil {
				t.Fatal(err)
			}
			lease, err := f.store.RenewAttemptLease(t.Context(), f.authority, time.Minute)
			if !errors.Is(err, playback.ErrStaleAttemptAuthorityV3) || !lease.IssuedAt.IsZero() {
				t.Fatalf("%s renewal returned permission: %+v %v", state, lease, err)
			}
		})
	}
}

func TestOwnerLeaseExpiresWhileWaitingForRowLock(t *testing.T) {
	f := newPlanstoreFixture(t)
	store := NewPostgres(f.pool)
	peer, application := authorityPeer(t, f)
	request := authorityRequest(f)
	reserved, err := store.ReserveAttempt(t.Context(), request)
	if err != nil {
		t.Fatal(err)
	}
	ctx, cancel := context.WithTimeout(t.Context(), 5*time.Second)
	defer cancel()
	tx, err := f.pool.Begin(ctx)
	if err != nil {
		t.Fatal(err)
	}
	defer func() { _ = tx.Rollback(context.WithoutCancel(ctx)) }()
	if _, err := tx.Exec(ctx, `SELECT 1 FROM playback_v3_attempts WHERE playback_attempt_id=$1 FOR UPDATE`, request.PlaybackAttemptID); err != nil {
		t.Fatal(err)
	}
	type outcome struct {
		lease playback.AttemptLeaseV3
		err   error
	}
	done := make(chan outcome, 1)
	go func() {
		lease, err := peer.RenewAttemptLease(ctx, reserved.Authority, time.Minute)
		done <- outcome{lease, err}
	}()
	for {
		var blocked bool
		if err := f.pool.QueryRow(ctx, `SELECT EXISTS(SELECT 1 FROM pg_stat_activity WHERE application_name=$1 AND wait_event_type='Lock')`, application).Scan(&blocked); err != nil {
			t.Fatal(err)
		}
		if blocked {
			break
		}
	}
	// Keep the row locked until the database itself observes its lease expire.
	// The waiter began with a live lease; neither a local sleep nor caller wall
	// time establishes when it becomes stale.
	if _, err := tx.Exec(ctx, `UPDATE playback_v3_attempts SET control_lease_expires_at=clock_timestamp()+interval '20 milliseconds' WHERE playback_attempt_id=$1`, request.PlaybackAttemptID); err != nil {
		t.Fatal(err)
	}
	for {
		var expired bool
		if err := tx.QueryRow(ctx, `SELECT control_lease_expires_at <= clock_timestamp() FROM playback_v3_attempts WHERE playback_attempt_id=$1`, request.PlaybackAttemptID).Scan(&expired); err != nil {
			t.Fatal(err)
		}
		if expired {
			break
		}
	}
	if err := tx.Commit(ctx); err != nil {
		t.Fatal(err)
	}
	select {
	case got := <-done:
		if !errors.Is(got.err, playback.ErrStaleAttemptAuthorityV3) || !got.lease.IssuedAt.IsZero() {
			t.Fatalf("expired waiter revived authority: %+v %v", got.lease, got.err)
		}
	case <-ctx.Done():
		t.Fatal("renewal did not resume after row unlock")
	}
	takeover, err := store.ReserveAttempt(ctx, request)
	if err != nil || !takeover.Owned || takeover.Authority.Epoch != reserved.Authority.Epoch+1 {
		t.Fatalf("expired renewal altered takeover fence: %+v %v", takeover, err)
	}
}

func TestOwnerLeaseRejectsStaleEpochAndSameOwnerABA(t *testing.T) {
	f := newPlanstoreFixture(t)
	store := NewPostgres(f.pool)
	request := authorityRequest(f)
	first, err := store.ReserveAttempt(t.Context(), request)
	if err != nil {
		t.Fatal(err)
	}
	expireAuthorityLease(t, f, request.PlaybackAttemptID)
	next, err := store.ReserveAttempt(t.Context(), request)
	if err != nil || !next.Owned || next.Authority.Epoch != first.Authority.Epoch+1 {
		t.Fatalf("same-owner takeover: %+v %v", next, err)
	}
	if _, err := store.RenewAttemptLease(t.Context(), first.Authority, time.Minute); !errors.Is(err, playback.ErrStaleAttemptAuthorityV3) {
		t.Fatalf("stale epoch renewed: %v", err)
	}
	// Simulate retention cleanup and re-creation with identical external IDs.
	if _, err := f.pool.Exec(t.Context(), `DELETE FROM playback_v3_attempts WHERE playback_attempt_id=$1`, request.PlaybackAttemptID); err != nil {
		t.Fatal(err)
	}
	recreated, err := store.ReserveAttempt(t.Context(), request)
	if err != nil || !recreated.Owned || recreated.Authority.Epoch != first.Authority.Epoch || recreated.Authority.Incarnation == first.Authority.Incarnation {
		t.Fatalf("ABA fixture did not recreate distinct incarnation: %+v %v", recreated, err)
	}
	if _, err := store.RenewAttemptLease(t.Context(), first.Authority, time.Minute); !errors.Is(err, playback.ErrStaleAttemptAuthorityV3) {
		t.Fatalf("old incarnation renewed recreated row: %v", err)
	}
	if _, err := store.RenewAttemptLease(t.Context(), recreated.Authority, time.Minute); err != nil {
		t.Fatalf("current incarnation refused: %v", err)
	}
}

func TestOwnerLeaseSupervisorCancelsAfterPostgresDrain(t *testing.T) {
	f := newGrantFixture(t)
	f.stage(t)
	if err := f.store.PublishAttempt(t.Context(), f.authority, f.record); err != nil {
		t.Fatal(err)
	}
	clock := new(runtimeTestClock)
	renewals := make(chan error, 4)
	source := func(ctx context.Context, authority playback.AttemptAuthorityV3, duration time.Duration) (playback.AttemptLeaseV3, error) {
		lease, err := f.store.RenewAttemptLease(ctx, authority, duration)
		renewals <- err
		return lease, err
	}
	policy := playback.RuntimeGrantPolicyV3{MaxDuration: 10 * time.Second, SafetyMargin: time.Second, RenewBefore: 3 * time.Second, PollInterval: time.Millisecond}
	supervisor, err := playback.AcquireRuntimeOwnerLeaseV3(t.Context(), source, clock, policy, f.authority)
	if err != nil {
		t.Fatal(err)
	}
	defer supervisor.Close()
	if err := <-renewals; err != nil {
		t.Fatal(err)
	}
	if supervisor.Authority().State != playback.AttemptActiveV3 || supervisor.Check() != nil {
		t.Fatal("supervisor did not adopt the database's preparing-to-active transition")
	}
	if _, err := f.store.BeginAttemptDrain(t.Context(), supervisor.Authority()); err != nil {
		t.Fatal(err)
	}
	// Move only the test's elapsed clock into its renewal window. The database
	// drain is already committed; no sleep schedules or guesses the transition.
	clock.tick.Store(int64(7 * time.Second))
	select {
	case err := <-renewals:
		if !errors.Is(err, playback.ErrStaleAttemptAuthorityV3) {
			t.Fatalf("draining renewal accepted: %v", err)
		}
	case <-time.After(3 * time.Second):
		t.Fatal("supervisor never attempted its renewal")
	}
	select {
	case <-supervisor.Context().Done():
	case <-time.After(3 * time.Second):
		t.Fatal("rejected database renewal did not cancel supervisor")
	}
	if !errors.Is(context.Cause(supervisor.Context()), playback.ErrStaleAttemptAuthorityV3) || !errors.Is(supervisor.Check(), playback.ErrStaleAttemptAuthorityV3) {
		t.Fatalf("supervisor lost terminal drain rejection: %v", context.Cause(supervisor.Context()))
	}
	var state playback.AttemptAuthorityStateV3
	if err := f.pool.QueryRow(t.Context(), `SELECT control_state FROM playback_v3_attempts WHERE playback_attempt_id=$1`, f.record.PlaybackAttemptID).Scan(&state); err != nil {
		t.Fatal(err)
	}
	if state != playback.AttemptDrainingV3 {
		t.Fatalf("supervisor changed draining state: %s", state)
	}
}

func TestOwnerLeaseSupervisorRejectsLostSuccessfulRenewalReply(t *testing.T) {
	f := newPlanstoreFixture(t)
	store := NewPostgres(f.pool)
	request := authorityRequest(f)
	request.LeaseDuration = time.Second
	reserved, err := store.ReserveAttempt(t.Context(), request)
	if err != nil {
		t.Fatal(err)
	}
	clock := new(runtimeTestClock)
	replies := make(chan playback.AttemptLeaseV3, 4)
	lostReply := errors.New("renewal response lost after commit")
	calls := 0
	source := func(ctx context.Context, authority playback.AttemptAuthorityV3, duration time.Duration) (playback.AttemptLeaseV3, error) {
		lease, err := store.RenewAttemptLease(ctx, authority, duration)
		if err != nil {
			return lease, err
		}
		calls++
		replies <- lease
		if calls > 1 {
			return playback.AttemptLeaseV3{}, lostReply
		}
		return lease, nil
	}
	policy := playback.RuntimeGrantPolicyV3{MaxDuration: 10 * time.Second, SafetyMargin: time.Second, RenewBefore: 3 * time.Second, PollInterval: time.Millisecond}
	supervisor, err := playback.AcquireRuntimeOwnerLeaseV3(t.Context(), source, clock, policy, reserved.Authority)
	if err != nil {
		t.Fatal(err)
	}
	defer supervisor.Close()
	first := <-replies
	clock.tick.Store(int64(7 * time.Second))
	var committed playback.AttemptLeaseV3
	select {
	case committed = <-replies:
	case <-time.After(3 * time.Second):
		t.Fatal("second database renewal did not commit")
	}
	select {
	case <-supervisor.Context().Done():
	case <-time.After(3 * time.Second):
		t.Fatal("lost renewal reply did not terminate supervisor")
	}
	if !errors.Is(supervisor.Check(), lostReply) {
		t.Fatalf("unknown outcome retained local permission: %v", supervisor.Check())
	}
	var persisted time.Time
	if err := f.pool.QueryRow(t.Context(), `SELECT control_lease_expires_at FROM playback_v3_attempts WHERE playback_attempt_id=$1`, request.PlaybackAttemptID).Scan(&persisted); err != nil {
		t.Fatal(err)
	}
	if !persisted.Equal(committed.Authority.LeaseExpiresAt) || !persisted.After(first.Authority.LeaseExpiresAt) {
		t.Fatalf("lost reply fixture did not extend persisted lease: before=%v committed=%v persisted=%v", first.Authority.LeaseExpiresAt, committed.Authority.LeaseExpiresAt, persisted)
	}
	if !supervisor.Authority().LeaseExpiresAt.Equal(first.Authority.LeaseExpiresAt) {
		t.Fatal("supervisor adopted a renewal whose response was lost")
	}
}
