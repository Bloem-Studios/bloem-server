package planstore

import (
	"errors"
	"fmt"
	"reflect"
	"testing"

	"github.com/Silo-Server/silo-server/internal/playback"
	"github.com/Silo-Server/silo-server/internal/userstore"
	"github.com/google/uuid"
)

func activatedOwnerLossSource(t *testing.T, backend string) (*initialActivationFixture, userstore.PlaybackSourceProvider, userstore.PlaybackSinkHandle, func()) {
	t.Helper()
	f := newInitialActivationFixture(t)
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
