package planstore

import (
	"context"
	"errors"
	"fmt"
	"reflect"
	"runtime"
	"testing"
	"time"

	"github.com/Silo-Server/silo-server/internal/noderecipe"
	"github.com/Silo-Server/silo-server/internal/playback"
	"github.com/Silo-Server/silo-server/internal/userstore"
	"github.com/google/uuid"
)

func activatedOwnerLossSource(t *testing.T, backend string, setup ...func(*initialActivationFixture)) (*initialActivationFixture, userstore.PlaybackSourceProvider, userstore.PlaybackSinkHandle, func()) {
	t.Helper()
	f := newInitialActivationFixture(t)
	for _, configure := range setup {
		configure(f)
	}
	provider, sink, seal := initialExactSource(t, f, backend)
	if _, err := sink.InstallPlaybackAuthority(t.Context(), userstore.InstallPlaybackAuthorityRequest{Scope: f.binding.Scope, Next: f.binding.Fence}); err != nil {
		t.Fatal(err)
	}
	if _, err := f.store.AcknowledgeInitialInstallation(t.Context(), f.binding, initialSourceReceipt(t, f, sink)); err != nil {
		t.Fatal(err)
	}
	f.stage(t)
	if _, err := f.store.PublishInitialActivation(t.Context(), f.binding, f.record); err != nil {
		t.Fatal(err)
	}
	return f, provider, sink, seal
}

func TestOwnerLossExactSourceTerminalReplay(t *testing.T) {
	for _, backend := range []string{"postgres", "sqlite"} {
		for _, sample := range []bool{false, true} {
			t.Run(fmt.Sprintf("%s/sample=%v", backend, sample), func(t *testing.T) {
				f, provider, sink, _ := activatedOwnerLossSource(t, backend)
				ctx := t.Context()
				if _, err := f.store.BeginOwnerLossRecovery(ctx, f.binding); !errors.Is(err, playback.ErrInitialOwnerLiveV3) {
					t.Fatalf("live owner: %v", err)
				}
				if sample {
					if _, err := sink.ApplyPlaybackProgress(ctx, userstore.ApplyPlaybackProgressRequest{Scope: f.binding.Scope, Fence: f.binding.Fence, Sample: userstore.PlaybackProgressSample{Sequence: 1, PositionSeconds: 42}}); err != nil {
						t.Fatal(err)
					}
				}
				var original []byte
				if err := f.pool.QueryRow(ctx, `SELECT jsonb_build_array(start_response,normalized_request,request_digest,control_owner,control_incarnation,control_epoch) FROM playback_v3_attempts WHERE playback_attempt_id=$1`, f.record.PlaybackAttemptID).Scan(&original); err != nil {
					t.Fatal(err)
				}
				expireInitialSourceOwner(t, f)
				lookup := playback.InitialRecoveryLookupV3{AccountID: f.userID, ProfileID: f.record.ProfileID, AttemptID: f.record.PlaybackAttemptID, RequestDigest: f.record.RequestDigest}
				if _, err := f.store.LookupInitialRecovery(ctx, lookup); err != nil {
					t.Fatal(err)
				}
				lookup.RequestDigest = "wrong"
				if _, err := f.store.LookupInitialRecovery(ctx, lookup); !errors.Is(err, playback.ErrIdempotencyKeyReusedV3) {
					t.Fatalf("digest: %v", err)
				}
				first, err := playback.ReconcileOwnerLossRecoveryV3(ctx, f.store, provider, f.binding)
				if err != nil || first.Phase != playback.InitialActivationAbortedV3 || first.Terminal == nil || first.Terminal.Stop.StopID != first.AbortID {
					t.Fatalf("terminal: %+v %v", first, err)
				}
				if (first.Terminal.Last != nil) != sample {
					t.Fatal("invented or lost accepted sample")
				}
				if sample && (first.Terminal.Last.Sample.Sequence != 1 || first.Terminal.Last.Sample.PositionSeconds != 42) {
					t.Fatal("changed accepted sample")
				}
				replay, err := playback.ReconcileOwnerLossRecoveryV3(ctx, f.store, provider, f.binding)
				if err != nil || !reflect.DeepEqual(first, replay) {
					t.Fatalf("replay: %+v %v", replay, err)
				}
				var after []byte
				if err := f.pool.QueryRow(ctx, `SELECT jsonb_build_array(start_response,normalized_request,request_digest,control_owner,control_incarnation,control_epoch) FROM playback_v3_attempts WHERE playback_attempt_id=$1`, f.record.PlaybackAttemptID).Scan(&after); err != nil {
					t.Fatal(err)
				}
				if string(original) != string(after) {
					t.Fatal("historical attempt or authority changed")
				}
				if _, err := f.store.IssueAttemptGrant(ctx, f.authority, f.request); err == nil {
					t.Fatal("grant after recovery")
				}
				if _, err := f.store.CleanupExpired(ctx, first.DrainNotBefore); err != nil {
					t.Fatal(err)
				}
				if _, err := f.store.LookupInitialRecovery(ctx, playback.InitialRecoveryLookupV3{AccountID: f.userID, ProfileID: f.record.ProfileID, SessionID: f.binding.Scope.SessionID}); err != nil {
					t.Fatalf("tombstone lost: %v", err)
				}
			})
		}
	}
}

func TestOwnerLossExistingStopAndSealedSource(t *testing.T) {
	for _, backend := range []string{"postgres", "sqlite"} {
		t.Run(backend+"/stop", func(t *testing.T) {
			f, provider, _, _ := activatedOwnerLossSource(t, backend)
			stopID := uuid.NewString()
			if _, err := f.store.BeginBoundStop(t.Context(), f.binding, stopID); err != nil {
				t.Fatal(err)
			}
			expireInitialSourceOwner(t, f)
			state, err := playback.ReconcileOwnerLossRecoveryV3(t.Context(), f.store, provider, f.binding)
			if err != nil || state.StopID != stopID || state.AbortID != "" || state.Phase != playback.InitialActivationStoppingV3 {
				t.Fatalf("replaced stop: %+v %v", state, err)
			}
		})
		t.Run(backend+"/sealed", func(t *testing.T) {
			f, provider, _, seal := activatedOwnerLossSource(t, backend)
			expireInitialSourceOwner(t, f)
			seal()
			if _, err := playback.ReconcileOwnerLossRecoveryV3(t.Context(), f.store, provider, f.binding); err == nil {
				t.Fatal("sealed source accepted")
			}
		})
	}
}

func TestOwnerLossFreshAdmissionAndRetention(t *testing.T) {
	f, provider, _, _ := activatedOwnerLossSource(t, "postgres")
	ctx := t.Context()
	fresh := f.reservation
	fresh.ExpectedAdmissionID = f.binding.AdmissionID
	fresh.PlaybackAttemptID = uuid.NewString()
	fresh.NormalizedRequest.PlaybackAttemptID = fresh.PlaybackAttemptID
	// Another live attempt is legal; this fence is not a one-session policy.
	if _, err := f.store.ReserveAttempt(ctx, fresh); err != nil {
		t.Fatal("unrelated live attempt blocked", err)
	}
	if _, err := f.pool.Exec(ctx, `UPDATE playback_v3_attempts SET control_lease_expires_at=clock_timestamp()-interval '1 second' WHERE playback_attempt_id=$1`, f.binding.Fence.AttemptID); err != nil {
		t.Fatal(err)
	}
	fresh.PlaybackAttemptID = uuid.NewString()
	fresh.NormalizedRequest.PlaybackAttemptID = fresh.PlaybackAttemptID
	if _, err := f.store.ReserveAttempt(ctx, fresh); !errors.Is(err, playback.ErrPlaybackRecoveryPendingV3) {
		t.Fatalf("fresh not fenced: %v", err)
	}
	other := fresh
	other.ProfileID = "unrelated-profile"
	other.PlaybackAttemptID = uuid.NewString()
	other.NormalizedRequest.ProfileID = other.ProfileID
	other.NormalizedRequest.PlaybackAttemptID = other.PlaybackAttemptID
	if _, err := f.store.ReserveAttempt(ctx, other); err != nil {
		t.Fatal("unrelated profile blocked", err)
	}
	// The old pre-admission reservation cannot adopt the now-bound attempt.
	if _, err := f.store.ReserveAttempt(ctx, f.reservation); err == nil {
		t.Fatal("pre-admission reservation adopted bound source")
	}
	observed, err := f.store.ReadInitialActivation(ctx, f.binding)
	if err != nil || observed.Binding.Fence != f.binding.Fence {
		t.Fatal("reservation changed old authority", err)
	}
	terminal, err := playback.ReconcileOwnerLossRecoveryV3(ctx, f.store, provider, f.binding)
	if err != nil {
		t.Fatal(err)
	}
	if _, err := f.store.ReserveAttempt(ctx, fresh); err != nil {
		t.Fatal("settled scope still blocked", err)
	}
	if _, err := f.store.CleanupExpired(ctx, time.Now().Add(24*time.Hour)); err != nil {
		t.Fatal(err)
	}
	if _, err := f.store.LookupInitialRecovery(ctx, playback.InitialRecoveryLookupV3{AccountID: f.userID, ProfileID: f.binding.Scope.ProfileID, SessionID: f.binding.Scope.SessionID}); err != nil {
		t.Fatal("tombstone removed using caller clock", err)
	}
	if _, err := f.pool.Exec(ctx, `UPDATE playback_v3_attempts SET expires_at=clock_timestamp()-interval '1 second' WHERE playback_attempt_id=$1`, f.binding.Fence.AttemptID); err != nil {
		t.Fatal(err)
	}
	if _, err := f.store.CleanupExpired(ctx, terminal.DrainNotBefore); err != nil {
		t.Fatal(err)
	}
	if _, err := f.store.LookupInitialRecovery(ctx, playback.InitialRecoveryLookupV3{AccountID: f.userID, ProfileID: f.binding.Scope.ProfileID, SessionID: f.binding.Scope.SessionID}); !errors.Is(err, playback.ErrSessionNotFound) {
		t.Fatalf("expired tombstone retained: %v", err)
	}
}

func TestOwnerLossCandidateAndRetainedReplacementDrain(t *testing.T) {
	f, doc := replacementFixture(t)
	ctx := t.Context()
	stageReadyReplacement(t, f, doc)
	current := f.request
	current.Duration = 100 * time.Millisecond
	granted, err := f.store.IssueAttemptGrant(ctx, f.authority, current)
	if err != nil {
		t.Fatal(err)
	}
	retiring, err := f.store.BeginBoundRouteRetirement(ctx, f.binding, doc.Key)
	if err != nil {
		t.Fatal(err)
	}
	candidate := f.request
	candidate.Executor = doc.Route.Executor
	candidate.TransportID = doc.Route.TransportID
	candidate.PlanID = doc.Next.CurrentPlanID
	candidate.Duration = 500 * time.Millisecond
	renewed, err := f.store.IssueAttemptGrant(ctx, f.authority, candidate)
	if err != nil {
		t.Fatal(err)
	}
	if _, err := f.pool.Exec(ctx, `UPDATE playback_v3_attempts SET control_lease_expires_at=clock_timestamp()-interval '1 second' WHERE playback_attempt_id=$1`, f.binding.Fence.AttemptID); err != nil {
		t.Fatal(err)
	}
	state, err := f.store.BeginOwnerLossRecovery(ctx, f.binding)
	if err != nil {
		t.Fatal(err)
	}
	if state.DrainNotBefore.Before(renewed.NotAfter) || state.DrainNotBefore.Before(granted.NotAfter) || state.DrainNotBefore.Before(retiring.DrainNotBefore) {
		t.Fatal("aggregate drain shortened")
	}
	if _, err := f.store.IssueAttemptGrant(ctx, f.authority, candidate); err == nil {
		t.Fatal("candidate renewed after recovery")
	}
	if _, err := f.store.CompleteBoundRouteReplacement(ctx, f.binding, doc.Key); err == nil {
		t.Fatal("candidate cut over after recovery")
	}
	if _, err := f.store.RenewAttemptLease(ctx, f.authority, time.Minute); err == nil {
		t.Fatal("owner renewed after recovery")
	}
	// The retained replacement deadline is still considered if the aggregate is
	// absent in a retained row; no worker/route pointer may stand in for it.
	if _, err := f.pool.Exec(ctx, `UPDATE playback_v3_attempts SET control_grant_not_after=NULL WHERE playback_attempt_id=$1`, f.binding.Fence.AttemptID); err != nil {
		t.Fatal(err)
	}
	replay, err := f.store.BeginOwnerLossRecovery(ctx, f.binding)
	if err != nil || !replay.DrainNotBefore.Equal(state.DrainNotBefore) || replay.AbortID != state.AbortID {
		t.Fatal("retry recalculated frozen barrier", err)
	}
}

func TestOwnerLossGrantAndRenewalRaceExpiry(t *testing.T) {
	f := activatedLifecycleFixture(t)
	ctx, cancel := context.WithTimeout(t.Context(), 5*time.Second)
	defer cancel()
	blocker, err := f.pool.Begin(ctx)
	if err != nil {
		t.Fatal(err)
	}
	defer rollbackAuthority(blocker)
	if _, err := blocker.Exec(ctx, `UPDATE playback_v3_attempts SET control_lease_expires_at=clock_timestamp()-interval '1 second' WHERE playback_attempt_id=$1`, f.binding.Fence.AttemptID); err != nil {
		t.Fatal(err)
	}
	grantDone, renewDone, recoveryDone := make(chan error, 1), make(chan error, 1), make(chan error, 1)
	go func() { _, err := f.store.IssueAttemptGrant(ctx, f.authority, f.request); grantDone <- err }()
	go func() { _, err := f.store.RenewAttemptLease(ctx, f.authority, time.Minute); renewDone <- err }()
	go func() { _, err := f.store.BeginOwnerLossRecovery(ctx, f.binding); recoveryDone <- err }()
	// Observe the actual lock wait before publishing expiry; no timing sleep.
	for {
		var waiting bool
		if err := f.pool.QueryRow(ctx, `SELECT EXISTS(SELECT 1 FROM pg_stat_activity WHERE datname=current_database() AND pid<>pg_backend_pid() AND wait_event_type='Lock')`).Scan(&waiting); err != nil {
			t.Fatal(err)
		}
		if waiting {
			break
		}
		runtime.Gosched()
	}
	if err := blocker.Commit(ctx); err != nil {
		t.Fatal(err)
	}
	if err := <-grantDone; err == nil {
		t.Fatal("grant crossed committed owner expiry")
	}
	if err := <-renewDone; err == nil {
		t.Fatal("renewal crossed committed owner expiry")
	}
	if err := <-recoveryDone; err != nil {
		t.Fatal("recovery did not settle expiry", err)
	}
}

func TestOwnerLossOutputAndAuxiliaryDrain(t *testing.T) {
	f, provider, _, _ := activatedOwnerLossSource(t, "postgres", func(f *initialActivationFixture) { f.route.ExecutionNodeID = 0 })
	ctx := t.Context()
	clock, err := playback.NewRuntimeGrantClockV3()
	if err != nil {
		t.Fatal(err)
	}
	policy := playback.RuntimeGrantPolicyV3{MaxDuration: time.Second, SafetyMargin: 100 * time.Millisecond, RenewBefore: 200 * time.Millisecond, PollInterval: time.Millisecond}
	// Permit admission reads the production control binding, not recipe content.
	// No Redis or media producer is accessed by these grant-only source tests.
	egress, err := NewExecutorRuntime(f.store, noderecipe.NewStore(nil, time.Minute), f.route.EgressNodeID, clock, policy)
	if err != nil {
		t.Fatal(err)
	}
	output, closeOutput, err := egress.OpenOutputTransfer(ctx, f.route.TransportID, f.route.Executor)
	if err != nil {
		t.Fatal(err)
	}
	defer closeOutput()
	auxiliary, closeAuxiliary, err := egress.OpenAuxiliaryTransfer(ctx, f.route.TransportID, f.route.Executor)
	if err != nil {
		t.Fatal(err)
	}
	defer closeAuxiliary()
	outputRequest := f.request
	outputRequest.NodeID = 0
	outputRequest.Purpose = playback.AttemptGrantTransferV3
	outputRequest.EgressNodeID = f.route.EgressNodeID
	outputRequest.OutputTransferID = output
	outputRequest.Duration = 300 * time.Millisecond
	outputGrant, err := f.store.IssueAttemptGrant(ctx, f.authority, outputRequest)
	if err != nil {
		t.Fatal(err)
	}
	auxiliaryRequest := outputRequest
	auxiliaryRequest.Purpose = playback.AttemptGrantAuxiliaryV3
	auxiliaryRequest.OutputTransferID = ""
	auxiliaryRequest.AuxiliaryTransferID = auxiliary
	auxiliaryRequest.Duration = 500 * time.Millisecond
	auxiliaryGrant, err := f.store.IssueAttemptGrant(ctx, f.authority, auxiliaryRequest)
	if err != nil {
		t.Fatal(err)
	}
	closeOutput()
	closeAuxiliary()
	if _, err := f.pool.Exec(ctx, `UPDATE playback_v3_attempts SET control_lease_expires_at=clock_timestamp()-interval '1 second' WHERE playback_attempt_id=$1`, f.binding.Fence.AttemptID); err != nil {
		t.Fatal(err)
	}
	state, err := playback.ReconcileOwnerLossRecoveryV3(ctx, f.store, provider, f.binding)
	if err != nil {
		t.Fatal(err)
	}
	if state.DrainNotBefore.Before(outputGrant.NotAfter) || state.DrainNotBefore.Before(auxiliaryGrant.NotAfter) {
		t.Fatal("released permits shortened aggregate")
	}
	if _, err := f.store.IssueAttemptGrant(ctx, f.authority, outputRequest); err == nil {
		t.Fatal("output renewed after recovery")
	}
	if _, err := f.store.IssueAttemptGrant(ctx, f.authority, auxiliaryRequest); err == nil {
		t.Fatal("auxiliary renewed after recovery")
	}
	if _, _, err := egress.OpenOutputTransfer(ctx, f.route.TransportID, f.route.Executor); err == nil {
		t.Fatal("new output permit after recovery")
	}
	if _, _, err := egress.OpenAuxiliaryTransfer(ctx, f.route.TransportID, f.route.Executor); err == nil {
		t.Fatal("new auxiliary permit after recovery")
	}
	waitInitialDatabaseTime(t, f, state.DrainNotBefore)
	final, err := playback.ReconcileOwnerLossRecoveryV3(ctx, f.store, provider, f.binding)
	if err != nil || final.Phase != playback.InitialActivationAbortedV3 || final.AbortID != state.AbortID {
		t.Fatal("drain failed to finish", err)
	}
}

func TestOwnerLossRetainedAbortIDMustMatchSourceStop(t *testing.T) {
	f, provider, _, _ := activatedOwnerLossSource(t, "postgres")
	expireInitialSourceOwner(t, f)
	state, err := playback.ReconcileOwnerLossRecoveryV3(t.Context(), f.store, provider, f.binding)
	if err != nil {
		t.Fatal(err)
	}
	if state.Terminal == nil || state.Terminal.Stop == nil {
		t.Fatal("missing source terminal")
	}
	// Validate retained documents too; replay must not attest to another stop.
	state.AbortID = uuid.NewString()
	if err := validateStoredInitialActivation(state); err == nil {
		t.Fatal("mismatched retained receipt accepted")
	}
}
