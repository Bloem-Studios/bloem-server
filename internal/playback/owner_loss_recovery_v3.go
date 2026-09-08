package playback

import (
	"context"
	"encoding/json"
	"errors"

	"github.com/Silo-Server/silo-server/internal/userstore"
)

const InitialAbortOwnerLostV3 = "owner_lost"

var ErrInitialOwnerLiveV3 = errors.New("initial playback owner remains live")
var ErrPlaybackRecoveryDrainingV3 = errors.New("playback recovery grants are draining")

var ErrPlaybackRecoveryPendingV3 = errors.New("lost playback owner requires terminal recovery")

// InitialRecoveryLookupV3 resolves retained identity, never live execution authority.
// START supplies AttemptID and its exact server-computed RequestDigest. STOP
// supplies SessionID; both remain scoped to the authenticated account/profile.
type InitialRecoveryLookupV3 struct {
	AccountID     int
	ProfileID     string
	AttemptID     string
	SessionID     string
	RequestDigest string
	TimelineID    string
}

type OwnerLossRecoveryStoreV3 interface {
	LookupInitialRecovery(context.Context, InitialRecoveryLookupV3) (InitialActivationV3, error)
	BeginOwnerLossRecovery(context.Context, InitialActivationBindingV3) (InitialActivationV3, error)
	CompleteInitialAbort(context.Context, InitialActivationBindingV3, string, InitialActivationReceiptV3) (InitialActivationV3, error)
}

// ReconcileOwnerLossRecoveryV3 closes only the captured sink fence. It does not
// supply a final sample, revive a producer, or replace a client-started stop.
func ReconcileOwnerLossRecoveryV3(ctx context.Context, store OwnerLossRecoveryStoreV3, sources userstore.PlaybackSourceProvider, b InitialActivationBindingV3) (InitialActivationV3, error) {
	state, err := store.BeginOwnerLossRecovery(ctx, b)
	if err != nil {
		return state, err
	}
	if state.AbortReason != InitialAbortOwnerLostV3 || state.Phase == InitialActivationAbortedV3 {
		return state, nil
	}
	sink, err := sources.OpenPlaybackSink(ctx, b.Source)
	if err != nil {
		return state, err
	}
	defer sink.Close() //nolint:errcheck
	if sink.Source() != b.Source {
		return state, ErrInitialActivationConflictV3
	}
	if state.Install == nil {
		if _, err = sink.InstallPlaybackAuthority(ctx, userstore.InstallPlaybackAuthorityRequest{Scope: b.Scope, Next: b.Fence}); err != nil {
			return state, err
		}
	}
	observed, err := ReadInitialActivationReceiptV3(ctx, b, sink)
	if err != nil {
		return state, err
	}
	receipt, err := observed.StateFor(b)
	if err != nil {
		return state, err
	}
	if receipt.Stop == nil {
		var identity userstore.WatchIdentity
		if b.HistoryIdentityJSON != "" {
			if err = json.Unmarshal([]byte(b.HistoryIdentityJSON), &identity); err != nil {
				return state, err
			}
		}
		if _, err = sink.StopPlaybackProgress(ctx, userstore.StopPlaybackProgressRequest{Scope: b.Scope, Fence: b.Fence, StopID: state.AbortID, Identity: identity}); err != nil {
			return state, err
		}
		observed, err = ReadInitialActivationReceiptV3(ctx, b, sink)
		if err != nil {
			return state, err
		}
	}
	completed, err := store.CompleteInitialAbort(ctx, b, state.AbortID, observed)
	if errors.Is(err, ErrPlaybackRecoveryDrainingV3) {
		return state, nil
	}
	if err != nil {
		return state, err
	}
	return completed, nil
}
