package planstore

import (
	"encoding/json"
	"errors"
	"testing"
	"time"

	"github.com/Silo-Server/silo-server/internal/playback"
	"github.com/google/uuid"
)

// TestBoundReplanFenceAndCAS proves the bound replan writer admits exactly the
// live active owner generation, that the legacy writer still refuses the row,
// and that completion is a compare-and-swap on the base revision that never
// moves the retention deadline or the staged executor route.
func TestBoundReplanFenceAndCAS(t *testing.T) {
	f := activatedLifecycleFixture(t)
	ctx := t.Context()
	session := f.record.SessionID
	authority := f.authority
	until := time.Now().Add(time.Minute)

	if _, err := f.store.BeginReplan(ctx, session, "legacy-1", "d", "", until); !errors.Is(err, playback.ErrStaleAttemptAuthorityV3) {
		t.Fatalf("legacy writer admitted an owned row: %v", err)
	}
	stale := authority
	stale.Epoch++
	if _, err := f.store.BeginBoundReplan(ctx, stale, session, "r-1", "d", "", until); !errors.Is(err, playback.ErrStaleAttemptAuthorityV3) {
		t.Fatalf("stale epoch admitted: %v", err)
	}
	foreign := authority
	foreign.OwnerID = uuid.NewString()
	if _, err := f.store.BeginBoundReplan(ctx, foreign, session, "r-1", "d", "", until); !errors.Is(err, playback.ErrStaleAttemptAuthorityV3) {
		t.Fatalf("foreign owner admitted: %v", err)
	}
	if _, err := f.store.BeginBoundReplan(ctx, authority, uuid.NewString(), "r-1", "d", "", until); !errors.Is(err, playback.ErrSessionNotFound) {
		t.Fatalf("unknown session: %v", err)
	}

	lease, err := f.store.BeginBoundReplan(ctx, authority, session, "r-1", "d", "", until)
	if err != nil || lease.State != playback.ReplanLeaseOwnedV3 || lease.LeaseToken == "" {
		t.Fatalf("owned lease: %+v %v", lease, err)
	}
	if again, err := f.store.BeginBoundReplan(ctx, authority, session, "r-1", "d", "", until); err != nil || again.State != playback.ReplanLeaseInFlightV3 {
		t.Fatalf("in-flight lease: %+v %v", again, err)
	}
	if _, err := f.store.BeginBoundReplan(ctx, authority, session, "r-1", "other", "", until); !errors.Is(err, playback.ErrIdempotencyKeyReusedV3) {
		t.Fatalf("reused id with other digest: %v", err)
	}

	var expiresBefore time.Time
	var routeBefore []byte
	if err := f.pool.QueryRow(ctx, `SELECT expires_at, control_route FROM playback_v3_attempts WHERE playback_attempt_id=$1`, authority.PlaybackAttemptID).Scan(&expiresBefore, &routeBefore); err != nil {
		t.Fatal(err)
	}
	updated := f.record
	updated.CurrentReplanRequestID = "r-1"
	updated.CurrentPlan.Timeline.SourceStartSeconds = 42
	updated.ExpiresAt = time.Now().Add(48 * time.Hour) // must be ignored by the bound CAS
	response, _ := json.Marshal(playback.DecisionResponseV3{ProtocolVersion: 3, Outcome: playback.OutcomePlayableV3, SessionID: session, PlaybackPlan: &updated.CurrentPlan})

	// A completion under a different fence, or for another attempt, is refused.
	if err := f.store.CompleteBoundReplan(ctx, stale, session, "r-1", lease.LeaseToken, "", response, updated); !errors.Is(err, playback.ErrReplanSupersededV3) {
		t.Fatalf("stale epoch completion: %v", err)
	}
	other := updated
	other.PlaybackAttemptID = uuid.NewString()
	if err := f.store.CompleteBoundReplan(ctx, authority, session, "r-1", lease.LeaseToken, "", response, other); !errors.Is(err, playback.ErrStaleAttemptAuthorityV3) {
		t.Fatalf("foreign attempt completion: %v", err)
	}
	// The legacy completion cannot land on an owned row either.
	if err := f.store.CompleteReplan(ctx, session, "r-1", lease.LeaseToken, "", response, updated); !errors.Is(err, playback.ErrReplanSupersededV3) {
		t.Fatalf("legacy completion admitted: %v", err)
	}
	if err := f.store.CompleteBoundReplan(ctx, authority, session, "r-1", lease.LeaseToken, "", response, updated); err != nil {
		t.Fatalf("bound completion: %v", err)
	}
	var expiresAfter time.Time
	var routeAfter []byte
	var current string
	var position float64
	if err := f.pool.QueryRow(ctx, `SELECT expires_at, control_route, current_replan_request_id, (current_plan->'timeline'->>'source_start_seconds')::float8 FROM playback_v3_attempts WHERE playback_attempt_id=$1`, authority.PlaybackAttemptID).Scan(&expiresAfter, &routeAfter, &current, &position); err != nil {
		t.Fatal(err)
	}
	if !expiresAfter.Equal(expiresBefore) || string(routeAfter) != string(routeBefore) || current != "r-1" || position != 42 {
		t.Fatalf("bound completion changed retention/route or missed the plan: %v/%v %q %v", expiresBefore, expiresAfter, current, position)
	}
	// The completed lease replays; a stale base is superseded.
	replay, err := f.store.BeginBoundReplan(ctx, authority, session, "r-1", "d", "", until)
	if err != nil || replay.State != playback.ReplanLeaseCompletedV3 {
		t.Fatalf("replay: %+v %v", replay.State, err)
	}
	var want, got playback.DecisionResponseV3
	if json.Unmarshal(response, &want) != nil || json.Unmarshal(replay.Response, &got) != nil || got.SessionID != want.SessionID || got.PlaybackPlan == nil || got.PlaybackPlan.Timeline.SourceStartSeconds != 42 {
		t.Fatalf("replayed decision: %s", replay.Response)
	}
	if _, err := f.store.BeginBoundReplan(ctx, authority, session, "r-2", "e", "wrong-base", until); err != nil {
		t.Fatalf("second lease: %v", err)
	}
	next := updated
	next.CurrentReplanRequestID = "r-2"
	if err := f.store.CompleteBoundReplan(ctx, authority, session, "r-2", "", "wrong-base", response, next); !errors.Is(err, playback.ErrReplanSupersededV3) {
		t.Fatalf("wrong base completion: %v", err)
	}
	// Once the owner lease has lapsed, the bound writer is fenced out too.
	if _, err := f.pool.Exec(ctx, `UPDATE playback_v3_attempts SET control_lease_expires_at = clock_timestamp() - interval '1 second' WHERE playback_attempt_id=$1`, authority.PlaybackAttemptID); err != nil {
		t.Fatal(err)
	}
	if _, err := f.store.BeginBoundReplan(ctx, authority, session, "r-3", "f", "r-1", until); !errors.Is(err, playback.ErrStaleAttemptAuthorityV3) {
		t.Fatalf("expired owner admitted: %v", err)
	}
}
