package planstore

import (
	"context"
	"errors"
	"sync"
	"testing"
	"time"

	"github.com/Silo-Server/silo-server/internal/playback"
	"github.com/google/uuid"
)

type grantFixture struct {
	*planstoreFixture
	store       *Postgres
	authority   playback.AttemptAuthorityV3
	record      playback.AttemptRecordV3
	route       playback.AttemptGrantRouteV3
	request     playback.AttemptGrantRequestV3
	reservation playback.AttemptReservationRequestV3
}

func newGrantFixture(t *testing.T) *grantFixture {
	t.Helper()
	f := newPlanstoreFixture(t)
	store, err := NewPostgresWithGrantPolicy(f.pool, playback.AttemptGrantPolicyV3{MaxDuration: 5 * time.Second})
	if err != nil {
		t.Fatal(err)
	}
	req := authorityRequest(f)
	reserved, err := store.ReserveAttempt(t.Context(), req)
	if err != nil {
		t.Fatal(err)
	}
	record := f.attemptRecord(uuid.NewString(), req.PlaybackAttemptID, req.RequestDigest)
	route := playback.AttemptGrantRouteV3{TransportID: uuid.NewString(), ExecutionNodeID: 1, EgressNodeID: 2}
	return &grantFixture{planstoreFixture: f, store: store, authority: reserved.Authority, record: record, route: route, reservation: req,
		request: playback.AttemptGrantRequestV3{SessionID: record.SessionID, PlanID: record.CurrentPlanID, TransportID: route.TransportID, NodeID: 1, Purpose: playback.AttemptGrantExecuteV3, Duration: 3 * time.Second}}
}

func (f *grantFixture) stage(t *testing.T) {
	t.Helper()
	if err := f.store.StageAttemptRoute(t.Context(), f.authority, f.record, f.route); err != nil {
		t.Fatal(err)
	}
}

func requireStaleGrant(t *testing.T, err error) {
	t.Helper()
	if !errors.Is(err, playback.ErrStaleAttemptAuthorityV3) {
		t.Fatalf("want stale authority, got %v", err)
	}
}

func TestAttemptGrantStagingAndBinding(t *testing.T) {
	f := newGrantFixture(t)
	_, err := f.store.IssueAttemptGrant(t.Context(), f.authority, f.request)
	requireStaleGrant(t, err)
	f.stage(t)
	f.stage(t)
	changed := f.record
	changed.CurrentPlan.DecisionReason = "changed-plan"
	requireStaleGrant(t, f.store.StageAttemptRoute(t.Context(), f.authority, changed, f.route))
	changed = f.record
	changed.FrozenRecipe.SubtitleTrackIndex = 0
	requireStaleGrant(t, f.store.StageAttemptRoute(t.Context(), f.authority, changed, f.route))
	route := f.route
	route.TransportID = uuid.NewString()
	requireStaleGrant(t, f.store.StageAttemptRoute(t.Context(), f.authority, f.record, route))
	changed = f.record
	changed.FrozenRecipe.SubtitleTrackIndex = 0
	requireStaleGrant(t, f.store.PublishAttempt(t.Context(), f.authority, changed))
	for name, change := range map[string]func(*playback.AttemptGrantRequestV3){
		"session":             func(r *playback.AttemptGrantRequestV3) { r.SessionID = uuid.NewString() },
		"plan":                func(r *playback.AttemptGrantRequestV3) { r.PlanID = "another-plan" },
		"transport nonce":     func(r *playback.AttemptGrantRequestV3) { r.TransportID = uuid.NewString() },
		"execution node":      func(r *playback.AttemptGrantRequestV3) { r.NodeID = 2 },
		"serve before active": func(r *playback.AttemptGrantRequestV3) { r.Purpose = playback.AttemptGrantServeV3; r.NodeID = 2 },
	} {
		t.Run(name, func(t *testing.T) {
			r := f.request
			change(&r)
			_, err := f.store.IssueAttemptGrant(t.Context(), f.authority, r)
			requireStaleGrant(t, err)
		})
	}
	for _, mutate := range []func(*playback.AttemptAuthorityV3){func(a *playback.AttemptAuthorityV3) { a.OwnerID = uuid.NewString() }, func(a *playback.AttemptAuthorityV3) { a.Epoch++ }} {
		a := f.authority
		mutate(&a)
		_, err := f.store.IssueAttemptGrant(t.Context(), a, f.request)
		requireStaleGrant(t, err)
	}
	if _, err := NewPostgres(f.pool).IssueAttemptGrant(t.Context(), f.authority, f.request); err == nil {
		t.Fatal("unconfigured store issued grant")
	}
	for _, duration := range []time.Duration{0, -time.Second} {
		r := f.request
		r.Duration = duration
		if _, err := f.store.IssueAttemptGrant(t.Context(), f.authority, r); err == nil {
			t.Fatal("invalid duration issued grant")
		}
	}
	r := f.request
	r.Purpose = "unknown"
	if _, err := f.store.IssueAttemptGrant(t.Context(), f.authority, r); err == nil {
		t.Fatal("invalid purpose issued grant")
	}
	if _, err := f.store.IssueAttemptGrant(t.Context(), f.authority, f.request); err != nil {
		t.Fatalf("preparing execute: %v", err)
	}
	if err := f.store.PublishAttempt(t.Context(), f.authority, f.record); err != nil {
		t.Fatal(err)
	}
	r = f.request
	r.NodeID = 2
	r.Purpose = playback.AttemptGrantServeV3
	if _, err := f.store.IssueAttemptGrant(t.Context(), f.authority, r); err != nil {
		t.Fatalf("active serve: %v", err)
	}
	r.NodeID = 1
	_, err = f.store.IssueAttemptGrant(t.Context(), f.authority, r)
	requireStaleGrant(t, err)
}

func TestAttemptGrantDurableMaximumAndDrain(t *testing.T) {
	f := newGrantFixture(t)
	f.stage(t)
	grant, err := f.store.IssueAttemptGrant(t.Context(), f.authority, f.request)
	if err != nil {
		t.Fatal(err)
	}
	if !grant.NotAfter.After(grant.IssuedAt) || grant.NotAfter.After(grant.IssuedAt.Add(f.request.Duration)) || grant.NotAfter.After(f.authority.LeaseExpiresAt) {
		t.Fatalf("unbounded grant: %+v", grant)
	}
	// Treat the first reply as lost. A second, shorter grant must not erase its
	// persisted maximum, which the other process uses to fence drain completion.
	shorter := f.request
	shorter.Duration = time.Millisecond
	if _, err := f.store.IssueAttemptGrant(t.Context(), f.authority, shorter); err != nil {
		t.Fatal(err)
	}
	peer, _ := authorityPeer(t, f.planstoreFixture)
	drain, err := peer.BeginAttemptDrain(t.Context(), f.authority)
	if err != nil {
		t.Fatal(err)
	}
	if drain.NotBefore.Before(grant.NotAfter) {
		t.Fatalf("lost reply omitted from drain: %+v grant=%+v", drain, grant)
	}
	replay, err := peer.BeginAttemptDrain(t.Context(), f.authority)
	if err != nil || !replay.NotBefore.Equal(drain.NotBefore) {
		t.Fatalf("drain deadline moved: %+v %v", replay, err)
	}
	_, err = f.store.IssueAttemptGrant(t.Context(), f.authority, f.request)
	requireStaleGrant(t, err)
	requireStaleGrant(t, f.store.CompleteAttemptDrain(t.Context(), f.authority))
	requireStaleGrant(t, f.store.PublishAttempt(t.Context(), f.authority, f.record))
	if _, err := f.pool.Exec(t.Context(), `UPDATE playback_v3_attempts SET control_grant_not_after=clock_timestamp()-interval '2 seconds',control_drain_not_before=clock_timestamp()-interval '1 second' WHERE playback_attempt_id=$1`, f.record.PlaybackAttemptID); err != nil {
		t.Fatal(err)
	}
	if err := peer.CompleteAttemptDrain(t.Context(), f.authority); err != nil {
		t.Fatal(err)
	}
	result, err := f.store.ReserveAttempt(t.Context(), f.reservation)
	if err != nil || result.Owned || result.Authority.State != playback.AttemptStoppedV3 {
		t.Fatalf("completed drain: %+v %v", result, err)
	}
}

func TestAttemptGrantOwnerExpiryCannotReclaim(t *testing.T) {
	f := newGrantFixture(t)
	f.stage(t)
	if _, err := f.store.IssueAttemptGrant(t.Context(), f.authority, f.request); err != nil {
		t.Fatal(err)
	}
	requireStaleGrant(t, f.store.StopAttempt(t.Context(), f.authority))
	expireAuthorityLease(t, f.planstoreFixture, f.record.PlaybackAttemptID)
	forged := f.authority
	forged.LeaseExpiresAt = time.Now().Add(100 * 365 * 24 * time.Hour)
	_, err := f.store.IssueAttemptGrant(t.Context(), forged, f.request)
	requireStaleGrant(t, err)
	if _, err := f.pool.Exec(t.Context(), `UPDATE playback_v3_attempts SET control_grant_not_after=clock_timestamp()-interval '1 second' WHERE playback_attempt_id=$1`, f.record.PlaybackAttemptID); err != nil {
		t.Fatal(err)
	}
	request := f.reservation
	request.OwnerID = uuid.NewString()
	result, err := f.store.ReserveAttempt(t.Context(), request)
	if err == nil && (result.Owned || result.Authority.Epoch != f.authority.Epoch) {
		t.Fatalf("previously granted preparing attempt reclaimed: %+v", result)
	}
	if _, err := f.store.BeginAttemptDrain(t.Context(), f.authority); err != nil {
		t.Fatalf("expired owner cannot drain its own fence: %v", err)
	}
}

func TestAttemptGrantDatabaseClockBounds(t *testing.T) {
	f := newGrantFixture(t)
	// Client-provided record retention cannot extend the reservation's deadline.
	f.record.ExpiresAt = time.Now().Add(100 * 365 * 24 * time.Hour)
	f.stage(t)
	var retainedWithinPolicy bool
	if err := f.pool.QueryRow(t.Context(), `SELECT expires_at <= clock_timestamp() + interval '1 hour' FROM playback_v3_attempts WHERE playback_attempt_id=$1`, f.record.PlaybackAttemptID).Scan(&retainedWithinPolicy); err != nil {
		t.Fatal(err)
	}
	if !retainedWithinPolicy {
		t.Fatal("staged client timestamp extended reservation retention")
	}

	if _, err := f.pool.Exec(t.Context(), `UPDATE playback_v3_attempts SET expires_at=clock_timestamp()+interval '2 seconds',control_lease_expires_at=clock_timestamp()+interval '1 second' WHERE playback_attempt_id=$1`, f.record.PlaybackAttemptID); err != nil {
		t.Fatal(err)
	}
	grant, err := f.store.IssueAttemptGrant(t.Context(), f.authority, f.request)
	if err != nil {
		t.Fatal(err)
	}
	var lease, retention, maximum time.Time
	if err := f.pool.QueryRow(t.Context(), `SELECT control_lease_expires_at,expires_at,control_grant_not_after FROM playback_v3_attempts WHERE playback_attempt_id=$1`, f.record.PlaybackAttemptID).Scan(&lease, &retention, &maximum); err != nil {
		t.Fatal(err)
	}
	if grant.NotAfter.After(lease) || grant.NotAfter.After(retention) || !maximum.Equal(grant.NotAfter) || grant.NotAfter.After(grant.IssuedAt.Add(5*time.Second)) {
		t.Fatalf("database bounds ignored: grant=%+v lease=%v retention=%v max=%v", grant, lease, retention, maximum)
	}
}

func TestAttemptGrantDrainRace(t *testing.T) {
	f := newGrantFixture(t)
	f.stage(t)
	peer, _ := authorityPeer(t, f.planstoreFixture)
	var grant playback.AttemptGrantV3
	var drain playback.AttemptDrainV3
	var grantErr, drainErr error
	gate := make(chan struct{})
	var wg sync.WaitGroup
	wg.Go(func() { <-gate; grant, grantErr = f.store.IssueAttemptGrant(t.Context(), f.authority, f.request) })
	wg.Go(func() { <-gate; drain, drainErr = peer.BeginAttemptDrain(t.Context(), f.authority) })
	close(gate)
	wg.Wait()
	if drainErr != nil {
		t.Fatal(drainErr)
	}
	if grantErr == nil {
		if drain.NotBefore.Before(grant.NotAfter) {
			t.Fatalf("drain missed concurrent grant: %+v %+v", drain, grant)
		}
	} else {
		requireStaleGrant(t, grantErr)
	}
	_, err := f.store.IssueAttemptGrant(t.Context(), f.authority, f.request)
	requireStaleGrant(t, err)
}

func TestAttemptGrantCancellation(t *testing.T) {
	f := newGrantFixture(t)
	f.stage(t)
	ctx, cancel := context.WithCancel(t.Context())
	cancel()
	if _, err := f.store.IssueAttemptGrant(ctx, f.authority, f.request); !errors.Is(err, context.Canceled) {
		t.Fatalf("canceled grant: %v", err)
	}
	var recorded bool
	if err := f.pool.QueryRow(t.Context(), `SELECT control_grant_not_after IS NOT NULL FROM playback_v3_attempts WHERE playback_attempt_id=$1`, f.record.PlaybackAttemptID).Scan(&recorded); err != nil {
		t.Fatal(err)
	}
	if recorded {
		t.Fatal("precanceled grant changed durable maximum")
	}
}

func TestAttemptGrantLeaseCheckedAfterRowWait(t *testing.T) {
	f := newGrantFixture(t)
	f.stage(t)
	rawPeer, name := authorityPeer(t, f.planstoreFixture)
	peer, err := NewPostgresWithGrantPolicy(rawPeer.db, playback.AttemptGrantPolicyV3{MaxDuration: 5 * time.Second})
	if err != nil {
		t.Fatal(err)
	}
	tx, err := f.pool.Begin(t.Context())
	if err != nil {
		t.Fatal(err)
	}
	defer func() {
		ctx, cancel := context.WithTimeout(context.Background(), time.Second)
		defer cancel()
		_ = tx.Rollback(ctx)
	}()
	if _, err := tx.Exec(t.Context(), `SELECT 1 FROM playback_v3_attempts WHERE playback_attempt_id=$1 FOR UPDATE`, f.record.PlaybackAttemptID); err != nil {
		t.Fatal(err)
	}
	ctx, cancel := context.WithTimeout(t.Context(), 5*time.Second)
	defer cancel()
	done := make(chan error, 1)
	go func() { _, err := peer.IssueAttemptGrant(ctx, f.authority, f.request); done <- err }()
	for {
		var waiting bool
		if err := f.pool.QueryRow(ctx, `SELECT EXISTS(SELECT 1 FROM pg_stat_activity WHERE application_name=$1 AND wait_event_type='Lock')`, name).Scan(&waiting); err != nil {
			t.Fatalf("grant did not wait: %v", err)
		}
		if waiting {
			break
		}
	}
	if _, err := tx.Exec(t.Context(), `UPDATE playback_v3_attempts SET control_lease_expires_at=clock_timestamp()-interval '1 second' WHERE playback_attempt_id=$1`, f.record.PlaybackAttemptID); err != nil {
		t.Fatal(err)
	}
	if err := tx.Commit(t.Context()); err != nil {
		t.Fatal(err)
	}
	select {
	case err := <-done:
		requireStaleGrant(t, err)
	case <-ctx.Done():
		t.Fatal("grant did not resume after lock release")
	}
	var recorded bool
	if err := f.pool.QueryRow(t.Context(), `SELECT control_grant_not_after IS NOT NULL FROM playback_v3_attempts WHERE playback_attempt_id=$1`, f.record.PlaybackAttemptID).Scan(&recorded); err != nil {
		t.Fatal(err)
	}
	if recorded {
		t.Fatal("expired waiting grant changed durable maximum")
	}
}

func TestAttemptGrantWrongIncarnation(t *testing.T) {
	f := newGrantFixture(t)
	f.stage(t)
	stale := f.authority
	stale.Incarnation = uuid.NewString()
	requireStaleGrant(t, f.store.StageAttemptRoute(t.Context(), stale, f.record, f.route))
	_, err := f.store.IssueAttemptGrant(t.Context(), stale, f.request)
	requireStaleGrant(t, err)
	_, err = f.store.BeginAttemptDrain(t.Context(), stale)
	requireStaleGrant(t, err)
	requireStaleGrant(t, f.store.CompleteAttemptDrain(t.Context(), stale))
	if _, err := f.store.BeginAttemptDrain(t.Context(), f.authority); err != nil {
		t.Fatal(err)
	}
	requireStaleGrant(t, f.store.CompleteAttemptDrain(t.Context(), stale))
	if err := f.store.CompleteAttemptDrain(t.Context(), f.authority); err != nil {
		t.Fatalf("valid incarnation drain: %v", err)
	}
}

func TestAttemptGrantPolicyMaximum(t *testing.T) {
	f := newGrantFixture(t)
	f.stage(t)
	request := f.request
	request.Duration = time.Hour
	grant, err := f.store.IssueAttemptGrant(t.Context(), f.authority, request)
	if err != nil {
		t.Fatal(err)
	}
	if !grant.NotAfter.After(grant.IssuedAt) || grant.NotAfter.After(grant.IssuedAt.Add(5*time.Second)) {
		t.Fatalf("deployment duration cap ignored: %+v", grant)
	}
}
