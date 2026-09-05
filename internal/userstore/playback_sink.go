package userstore

import (
	"context"
	"crypto/sha256"
	"encoding/hex"
	"encoding/json"
	"errors"
	"math"
)

var (
	ErrPlaybackSinkUnsupported = errors.New("selected store does not support atomic playback writes")
	ErrPlaybackSinkNotFound    = errors.New("playback sink state not found")
	ErrPlaybackSinkStale       = errors.New("stale playback sink authority")
	ErrPlaybackSinkConflict    = errors.New("conflicting playback sink replay")
	ErrPlaybackSinkStopped     = errors.New("playback sink is stopped")
	ErrPlaybackSinkInvalid     = errors.New("invalid playback sink request")
)

// PlaybackProgressSink is an optional selected-store capability. Implementations
// commit the fence, receipts and personal projections in one local transaction.
// They do not coordinate control-plane ownership or select another database.
type PlaybackProgressSink interface {
	ReadPlaybackProgress(context.Context, PlaybackProgressScope) (PlaybackProgressState, error)
	InstallPlaybackAuthority(context.Context, InstallPlaybackAuthorityRequest) (PlaybackProgressResult, error)
	ApplyPlaybackProgress(context.Context, ApplyPlaybackProgressRequest) (PlaybackProgressResult, error)
	StopPlaybackProgress(context.Context, StopPlaybackProgressRequest) (PlaybackProgressResult, error)
}

type PlaybackProgressScope struct {
	ProfileID   string
	SessionID   string
	MediaItemID string
}

// PlaybackProgressFence belongs to one control attempt incarnation. Epochs
// from different incarnations are never comparable.
type PlaybackProgressFence struct {
	AttemptID   string
	Incarnation string
	OwnerID     string
	Epoch       int64
}

// PlaybackProgressSample is the admitted immutable envelope for a sequence.
// Policy and hints are resolved by trusted admission, not selected by clients.
// Retries retain the envelope instead of re-resolving mutable policy.
type PlaybackProgressSample struct {
	Sequence            int64
	PositionSeconds     float64
	DurationSeconds     float64
	Paused              bool
	PersistenceDisabled bool
	Thresholds          ProgressThresholds
	Hints               VersionHints
}

type PlaybackProgressReceipt struct {
	Fence  PlaybackProgressFence
	Sample PlaybackProgressSample
	Digest string
}

type PlaybackStopReceipt struct {
	Fence    PlaybackProgressFence
	StopID   string
	Digest   string
	Accepted *PlaybackProgressReceipt
	History  *WatchHistoryEntry
}

// PlaybackProgressState is persisted as versioned bounded JSON. Receipt fences
// retain their original owner when the current authority advances.
type PlaybackProgressState struct {
	Version int
	Scope   PlaybackProgressScope
	Fence   PlaybackProgressFence
	Last    *PlaybackProgressReceipt
	Stop    *PlaybackStopReceipt
}

type InstallPlaybackAuthorityRequest struct {
	Scope    PlaybackProgressScope
	Expected *PlaybackProgressFence
	Next     PlaybackProgressFence
}

type ApplyPlaybackProgressRequest struct {
	Scope  PlaybackProgressScope
	Fence  PlaybackProgressFence
	Sample PlaybackProgressSample
}

type StopPlaybackProgressRequest struct {
	Scope       PlaybackProgressScope
	Fence       PlaybackProgressFence
	StopID      string
	FinalSample *PlaybackProgressSample
	Identity    WatchIdentity
}

type PlaybackProjectionState struct {
	Exists          bool
	PositionSeconds float64
	Completed       bool
}

const (
	PlaybackProgressInstalled   = "installed"
	PlaybackProgressApplied     = "applied"
	PlaybackProgressStopped     = "stopped"
	PlaybackProgressReplayed    = "replayed"
	PlaybackProgressStaleSample = "stale_sample"
)

type PlaybackProgressResult struct {
	State PlaybackProgressState
	// Outcome is installed, applied, stopped, replayed or stale_sample.
	Outcome         string
	ProgressChanged bool
	HintsChanged    bool
	HistoryCreated  bool
	Before          PlaybackProjectionState
	After           PlaybackProjectionState
}

// PlaybackProgressChange is a prepared transition, not a committed result.
// Providers call Prepare* only after reading state under their writer lock,
// apply its projection and save Result.State in the same transaction, and
// return a zero result on any error (including uncertain commit).
type PlaybackProgressChange struct {
	Result   PlaybackProgressResult
	Changed  bool
	Sample   *PlaybackProgressSample
	Final    bool
	Identity WatchIdentity
}

func (s PlaybackProgressScope) Validate() error {
	for _, value := range []string{s.ProfileID, s.SessionID, s.MediaItemID} {
		if value == "" || len(value) > 512 {
			return ErrPlaybackSinkInvalid
		}
	}
	return nil
}

func (f PlaybackProgressFence) Validate() error {
	if f.Epoch <= 0 {
		return ErrPlaybackSinkInvalid
	}
	for _, value := range []string{f.AttemptID, f.Incarnation, f.OwnerID} {
		if value == "" || len(value) > 512 {
			return ErrPlaybackSinkInvalid
		}
	}
	return nil
}

func playbackSampleDigest(sample PlaybackProgressSample) (string, error) {
	if sample.Sequence <= 0 || math.IsNaN(sample.PositionSeconds) || math.IsInf(sample.PositionSeconds, 0) ||
		math.IsNaN(sample.DurationSeconds) || math.IsInf(sample.DurationSeconds, 0) || sample.PositionSeconds < 0 || sample.DurationSeconds < 0 ||
		sample.Hints.FileID < 0 || sample.Thresholds.MinResumePct < 0 || sample.Thresholds.MinResumePct > 100 || sample.Thresholds.WatchedPct < 0 || sample.Thresholds.WatchedPct > 100 {
		return "", ErrPlaybackSinkInvalid
	}
	// JSON distinguishes negative zero even though playback does not.
	if sample.PositionSeconds == 0 {
		sample.PositionSeconds = 0
	}
	if sample.DurationSeconds == 0 {
		sample.DurationSeconds = 0
	}
	return playbackDigest(sample)
}

func playbackDigest(value any) (string, error) {
	data, err := json.Marshal(value)
	if err != nil || len(data) > 64*1024 {
		return "", ErrPlaybackSinkInvalid
	}
	digest := sha256.Sum256(data)
	return hex.EncodeToString(digest[:]), nil
}

func checkPlaybackState(state *PlaybackProgressState, scope PlaybackProgressScope) error {
	if err := scope.Validate(); err != nil {
		return err
	}
	if state == nil {
		return ErrPlaybackSinkNotFound
	}
	if state.Version != 1 {
		return ErrPlaybackSinkInvalid
	}
	if state.Scope != scope {
		return ErrPlaybackSinkStale
	}
	return nil
}

func PreparePlaybackAuthority(state *PlaybackProgressState, request InstallPlaybackAuthorityRequest) (PlaybackProgressChange, error) {
	if err := request.Scope.Validate(); err != nil {
		return PlaybackProgressChange{}, err
	}
	if err := request.Next.Validate(); err != nil {
		return PlaybackProgressChange{}, err
	}
	if state == nil {
		if request.Expected != nil {
			return PlaybackProgressChange{}, ErrPlaybackSinkStale
		}
		return PlaybackProgressChange{Changed: true, Result: PlaybackProgressResult{Outcome: PlaybackProgressInstalled, State: PlaybackProgressState{Version: 1, Scope: request.Scope, Fence: request.Next}}}, nil
	}
	if err := checkPlaybackState(state, request.Scope); err != nil {
		return PlaybackProgressChange{}, err
	}
	if state.Fence == request.Next {
		return PlaybackProgressChange{Result: PlaybackProgressResult{Outcome: PlaybackProgressReplayed, State: *state}}, nil
	}
	if state.Stop != nil {
		return PlaybackProgressChange{}, ErrPlaybackSinkStopped
	}
	if request.Expected == nil || *request.Expected != state.Fence || request.Next.AttemptID != state.Fence.AttemptID || request.Next.Incarnation != state.Fence.Incarnation || request.Next.Epoch <= state.Fence.Epoch {
		return PlaybackProgressChange{}, ErrPlaybackSinkStale
	}
	next := *state
	next.Fence = request.Next
	return PlaybackProgressChange{Changed: true, Result: PlaybackProgressResult{Outcome: PlaybackProgressInstalled, State: next}}, nil
}

func PreparePlaybackProgress(state *PlaybackProgressState, request ApplyPlaybackProgressRequest) (PlaybackProgressChange, error) {
	if err := checkPlaybackState(state, request.Scope); err != nil {
		return PlaybackProgressChange{}, err
	}
	if request.Fence != state.Fence {
		return PlaybackProgressChange{}, ErrPlaybackSinkStale
	}
	if state.Stop != nil {
		return PlaybackProgressChange{}, ErrPlaybackSinkStopped
	}
	digest, err := playbackSampleDigest(request.Sample)
	if err != nil {
		return PlaybackProgressChange{}, err
	}
	if state.Last != nil {
		if request.Sample.Sequence < state.Last.Sample.Sequence {
			return PlaybackProgressChange{Result: PlaybackProgressResult{Outcome: PlaybackProgressStaleSample, State: *state}}, nil
		}
		if request.Sample.Sequence == state.Last.Sample.Sequence {
			if digest != state.Last.Digest {
				return PlaybackProgressChange{}, ErrPlaybackSinkConflict
			}
			return PlaybackProgressChange{Result: PlaybackProgressResult{Outcome: PlaybackProgressReplayed, State: *state}}, nil
		}
	}
	next := *state
	next.Last = &PlaybackProgressReceipt{Fence: request.Fence, Sample: request.Sample, Digest: digest}
	return PlaybackProgressChange{Changed: true, Sample: &next.Last.Sample, Result: PlaybackProgressResult{Outcome: PlaybackProgressApplied, State: next}}, nil
}

func PreparePlaybackStop(state *PlaybackProgressState, request StopPlaybackProgressRequest) (PlaybackProgressChange, error) {
	if err := checkPlaybackState(state, request.Scope); err != nil {
		return PlaybackProgressChange{}, err
	}
	if request.Fence != state.Fence {
		return PlaybackProgressChange{}, ErrPlaybackSinkStale
	}
	if request.StopID == "" || len(request.StopID) > 512 {
		return PlaybackProgressChange{}, ErrPlaybackSinkInvalid
	}
	var sampleDigest string
	var err error
	if request.FinalSample != nil {
		sampleDigest, err = playbackSampleDigest(*request.FinalSample)
		if err != nil {
			return PlaybackProgressChange{}, err
		}
	}
	digest, err := playbackDigest(struct {
		StopID, SampleDigest string
		Identity             WatchIdentity
	}{request.StopID, sampleDigest, request.Identity})
	if err != nil {
		return PlaybackProgressChange{}, err
	}
	if state.Stop != nil {
		if request.StopID != state.Stop.StopID || digest != state.Stop.Digest {
			return PlaybackProgressChange{}, ErrPlaybackSinkConflict
		}
		return PlaybackProgressChange{Result: PlaybackProgressResult{Outcome: PlaybackProgressReplayed, State: *state}}, nil
	}
	next := *state
	if request.FinalSample != nil {
		change, err := PreparePlaybackProgress(state, ApplyPlaybackProgressRequest{Scope: request.Scope, Fence: request.Fence, Sample: *request.FinalSample})
		if err != nil {
			return PlaybackProgressChange{}, err
		}
		next = change.Result.State
	}
	next.Stop = &PlaybackStopReceipt{Fence: request.Fence, StopID: request.StopID, Digest: digest, Accepted: next.Last}
	change := PlaybackProgressChange{Changed: true, Final: true, Identity: request.Identity, Result: PlaybackProgressResult{Outcome: PlaybackProgressStopped, State: next}}
	if next.Last != nil {
		change.Sample = &next.Last.Sample
	}
	return change, nil
}

// PlaybackProjectionPolicy keeps raw sequencing separate from projection.
// Below-threshold heartbeats may update hints on an existing row, matching the
// legacy handler. A below-threshold stop does not project or create history.
func PlaybackProjectionPolicy(sample PlaybackProgressSample, final bool) (progress, hints, history bool) {
	if sample.PersistenceDisabled || sample.PositionSeconds <= 0 {
		return false, false, false
	}
	_, _, skip := ResolveProgressState(sample.PositionSeconds, sample.DurationSeconds, sample.Thresholds)
	if skip && final {
		return false, false, false
	}
	return !skip, sample.Hints.FileID > 0, final && !skip
}
