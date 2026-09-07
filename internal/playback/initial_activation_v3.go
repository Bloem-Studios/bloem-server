package playback

import (
	"context"
	"encoding/json"
	"errors"
	"fmt"
	"time"

	"github.com/google/uuid"

	"github.com/Silo-Server/silo-server/internal/userstore"
)

var (
	ErrInitialActivationInvalidV3     = errors.New("invalid initial playback activation")
	ErrInitialActivationConflictV3    = errors.New("initial playback activation conflict")
	ErrInitialActivationUnavailableV3 = errors.New("initial playback activation unavailable")
)

type InitialActivationPhaseV3 string

const (
	InitialActivationPendingV3   InitialActivationPhaseV3 = "pending"
	InitialActivationInstalledV3 InitialActivationPhaseV3 = "installed"
	InitialActivationActivatedV3 InitialActivationPhaseV3 = "activated"
	InitialActivationAbortingV3  InitialActivationPhaseV3 = "aborting"
	InitialActivationAbortedV3   InitialActivationPhaseV3 = "aborted"
	InitialActivationStoppingV3  InitialActivationPhaseV3 = "stopping"
	InitialActivationStoppedV3   InitialActivationPhaseV3 = "stopped"
)

// InitialActivationBindingV3 is persisted before any selected-source call.
// AdmissionID identifies the registration's admission decision; IntentID
// identifies this attempt's initial activation. Neither follows later state.
type InitialActivationBindingV3 struct {
	Progress            userstore.PlaybackProgressSample `json:"progress"`
	HistoryIdentityJSON string                           `json:"history_identity_json,omitempty"`
	Source              userstore.PlaybackSourceRef      `json:"source"`
	Scope               userstore.PlaybackProgressScope  `json:"scope"`
	Fence               userstore.PlaybackProgressFence  `json:"fence"`
	IntentID            string                           `json:"intent_id"`
	AdmissionID         string                           `json:"admission_id"`
}

func (b InitialActivationBindingV3) Validate() error {
	if err := b.Source.Validate(); err != nil {
		return err
	}
	if err := b.Scope.Validate(); err != nil {
		return err
	}
	if err := b.Fence.Validate(); err != nil {
		return err
	}
	if b.Progress.Sequence != 0 || b.Progress.PositionSeconds != 0 || b.Progress.Paused {
		return ErrInitialActivationInvalidV3
	}
	sample := b.Progress
	sample.Sequence = 1
	state := userstore.PlaybackProgressState{Version: 1, Scope: b.Scope, Fence: b.Fence}
	if _, err := userstore.PreparePlaybackProgress(&state, userstore.ApplyPlaybackProgressRequest{Scope: b.Scope, Fence: b.Fence, Sample: sample}); err != nil {
		return err
	}
	if b.HistoryIdentityJSON != "" {
		var identity userstore.WatchIdentity
		if err := json.Unmarshal([]byte(b.HistoryIdentityJSON), &identity); err != nil {
			return fmt.Errorf("invalid frozen history identity: %w", ErrInitialActivationInvalidV3)
		}
	}
	for _, value := range []string{b.IntentID, b.AdmissionID, b.Scope.SessionID, b.Fence.Incarnation, b.Fence.OwnerID} {
		id, err := uuid.Parse(value)
		if err != nil || id == uuid.Nil || id.String() != value {
			return ErrInitialActivationInvalidV3
		}
	}
	return nil
}

func (b InitialActivationBindingV3) Authority() AttemptAuthorityV3 {
	return AttemptAuthorityV3{PlaybackAttemptID: b.Fence.AttemptID, Incarnation: b.Fence.Incarnation, OwnerID: b.Fence.OwnerID, Epoch: b.Fence.Epoch}
}

type InitialActivationV3 struct {
	Binding        InitialActivationBindingV3       `json:"binding"`
	Phase          InitialActivationPhaseV3         `json:"phase"`
	Install        *userstore.PlaybackProgressState `json:"install,omitempty"`
	StopID         string                           `json:"stop_id,omitempty"`
	AbortID        string                           `json:"abort_id,omitempty"`
	DrainNotBefore time.Time                        `json:"drain_not_before,omitzero"`
	Terminal       *userstore.PlaybackProgressState `json:"terminal,omitempty"`
}

// InitialActivationStoreV3 persists only the initial control protocol. It
// performs no source calls and grants no reconciliation execution authority.
// Read remains available after lease and retention expiry for exact bindings.
type InitialActivationStoreV3 interface {
	BeginInitialActivation(context.Context, InitialActivationBindingV3) (InitialActivationV3, error)
	ReadInitialActivation(context.Context, InitialActivationBindingV3) (InitialActivationV3, error)
	AcknowledgeInitialInstallation(context.Context, InitialActivationBindingV3, InitialActivationReceiptV3) (InitialActivationV3, error)
	PublishInitialActivation(context.Context, InitialActivationBindingV3, AttemptRecordV3) (InitialActivationV3, error)
	AbortInitialActivation(context.Context, InitialActivationBindingV3, string) (InitialActivationV3, error)
	CancelInitialActivation(context.Context, InitialActivationBindingV3, string) (InitialActivationV3, error)
	CompleteInitialAbort(context.Context, InitialActivationBindingV3, string, InitialActivationReceiptV3) (InitialActivationV3, error)
}

// InitialActivationReceiptV3 is a frozen observation from an exact source
// handle. Its fields are private so a bare sink state cannot acknowledge an
// installation or abort. Concrete source handles are the trust boundary.
type InitialActivationReceiptV3 struct {
	source   userstore.PlaybackSourceRef
	document string
}

// ReadInitialActivationReceiptV3 must run before entering control storage.
// It observes committed state; it performs no install, stop or reconciliation.
func ReadInitialActivationReceiptV3(ctx context.Context, binding InitialActivationBindingV3, handle userstore.PlaybackSinkHandle) (InitialActivationReceiptV3, error) {
	var zero InitialActivationReceiptV3
	if err := binding.Validate(); err != nil {
		return zero, err
	}
	if handle == nil || handle.Source() != binding.Source {
		return zero, ErrInitialActivationInvalidV3
	}
	state, err := handle.ReadPlaybackProgress(ctx, binding.Scope)
	if err != nil {
		return zero, err
	}
	if state.Version != 1 || state.Scope != binding.Scope || state.Fence != binding.Fence {
		return zero, ErrInitialActivationInvalidV3
	}
	document, err := json.Marshal(state)
	if err != nil {
		return zero, err
	}
	if len(document) > 256*1024 {
		return zero, ErrInitialActivationInvalidV3
	}
	return InitialActivationReceiptV3{source: binding.Source, document: string(document)}, nil
}

// StateFor returns a fresh copy, preserving the immutable observed receipt.
func (r InitialActivationReceiptV3) StateFor(binding InitialActivationBindingV3) (userstore.PlaybackProgressState, error) {
	var state userstore.PlaybackProgressState
	if r.source != binding.Source || r.document == "" {
		return state, ErrInitialActivationInvalidV3
	}
	if err := json.Unmarshal([]byte(r.document), &state); err != nil {
		return state, err
	}
	if state.Version != 1 || state.Scope != binding.Scope || state.Fence != binding.Fence {
		return userstore.PlaybackProgressState{}, ErrInitialActivationInvalidV3
	}
	return state, nil
}

// ValidateInitialInstallV3 checks a receipt supplied by a trusted exact-source
// handle. Control storage cannot itself prove an external database committed.
func ValidateInitialInstallV3(b InitialActivationBindingV3, receipt userstore.PlaybackProgressState) error {
	if receipt.Version != 1 || receipt.Scope != b.Scope || receipt.Fence != b.Fence || receipt.Last != nil || receipt.Stop != nil {
		return fmt.Errorf("initial installation receipt: %w", ErrInitialActivationInvalidV3)
	}
	return nil
}

// An already stopped exact fence is valid terminal evidence even when its
// stop ID differs from this abort's ID. The store retains the first receipt.
func ValidateInitialTerminalV3(b InitialActivationBindingV3, receipt userstore.PlaybackProgressState) error {
	if receipt.Version != 1 || receipt.Scope != b.Scope || receipt.Fence != b.Fence || receipt.Stop == nil || receipt.Stop.Fence != b.Fence || receipt.Stop.StopID == "" || receipt.Stop.Digest == "" {
		return fmt.Errorf("initial terminal receipt: %w", ErrInitialActivationInvalidV3)
	}
	return nil
}

// AdmittedPlaybackSourceV3 is a read-only observation, not an admission grant.
// BeginInitialActivation rechecks the captured selection under registration lock.
type AdmittedPlaybackSourceV3 struct {
	Source      userstore.PlaybackSourceRef
	AdmissionID string
}

// ActivatedPlaybackAuthorityV3 includes phase so callers distinguish a live
// progress authority from retained stopping/stopped reconciliation information.
// A control read does not authorize a later write to an unrelated source.
type ActivatedPlaybackAuthorityV3 struct {
	Binding    InitialActivationBindingV3
	Authority  AttemptAuthorityV3
	Activation InitialActivationV3
}

type BoundPlaybackControlStoreV3 interface {
	GetAdmittedPlaybackSource(context.Context, int) (AdmittedPlaybackSourceV3, error)
	GetActivatedPlaybackAuthority(context.Context, int, string, string) (ActivatedPlaybackAuthorityV3, error)
	BeginBoundStop(context.Context, InitialActivationBindingV3, string) (InitialActivationV3, error)
	CompleteBoundStop(context.Context, InitialActivationBindingV3, string, InitialActivationReceiptV3) (InitialActivationV3, error)
}

// SessionActivationPhaseV3 is the durable phase of a session's attempt row
// with the account and profile that own it, read without a binding.
type SessionActivationPhaseV3 struct {
	UserID    int
	ProfileID string
	Phase     InitialActivationPhaseV3
}
