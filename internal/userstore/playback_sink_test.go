package userstore

import (
	"errors"
	"math"
	"reflect"
	"testing"
)

func playbackSinkState(t *testing.T) PlaybackProgressState {
	t.Helper()
	change, err := PreparePlaybackAuthority(nil, InstallPlaybackAuthorityRequest{
		Scope: PlaybackProgressScope{ProfileID: "profile", SessionID: "session", MediaItemID: "item"},
		Next:  PlaybackProgressFence{AttemptID: "attempt", Incarnation: "incarnation", OwnerID: "boot", Epoch: 1},
	})
	if err != nil {
		t.Fatal(err)
	}
	return change.Result.State
}

func TestPlaybackSinkPreservesReceiptAcrossAuthorityAdvance(t *testing.T) {
	state := playbackSinkState(t)
	first, err := PreparePlaybackProgress(&state, ApplyPlaybackProgressRequest{Scope: state.Scope, Fence: state.Fence, Sample: PlaybackProgressSample{Sequence: 41, PositionSeconds: 600, DurationSeconds: 1000}})
	if err != nil {
		t.Fatal(err)
	}
	state = first.Result.State
	original := *state.Last
	nextFence := state.Fence
	nextFence.Epoch++
	nextFence.OwnerID = "new-boot"
	advanced, err := PreparePlaybackAuthority(&state, InstallPlaybackAuthorityRequest{Scope: state.Scope, Expected: &state.Fence, Next: nextFence})
	if err != nil {
		t.Fatal(err)
	}
	if !reflect.DeepEqual(*advanced.Result.State.Last, original) {
		t.Fatal("advance rewrote original receipt")
	}
	state = advanced.Result.State
	if _, err := PreparePlaybackProgress(&state, ApplyPlaybackProgressRequest{Scope: state.Scope, Fence: original.Fence, Sample: original.Sample}); !errors.Is(err, ErrPlaybackSinkStale) {
		t.Fatalf("replay bypassed authority check: %v", err)
	}
	replay, err := PreparePlaybackProgress(&state, ApplyPlaybackProgressRequest{Scope: state.Scope, Fence: state.Fence, Sample: original.Sample})
	if err != nil || replay.Changed || replay.Result.Outcome != "replayed" || replay.Result.State.Last.Fence != original.Fence {
		t.Fatalf("successor retry lost original receipt: %+v %v", replay, err)
	}
	backward := original.Sample
	backward.Sequence++
	backward.PositionSeconds = 120
	accepted, err := PreparePlaybackProgress(&state, ApplyPlaybackProgressRequest{Scope: state.Scope, Fence: state.Fence, Sample: backward})
	if err != nil || accepted.Result.State.Last.Sample.PositionSeconds != 120 {
		t.Fatalf("backward seek: %+v %v", accepted, err)
	}
	if state.Last.Sample.PositionSeconds != 600 {
		t.Fatal("preparing transition mutated input state")
	}
}

func TestPlaybackSinkStopMergesLatestRawSampleAndCannotReopen(t *testing.T) {
	state := playbackSinkState(t)
	change, err := PreparePlaybackProgress(&state, ApplyPlaybackProgressRequest{Scope: state.Scope, Fence: state.Fence, Sample: PlaybackProgressSample{Sequence: 5, PositionSeconds: 980, DurationSeconds: 1000}})
	if err != nil {
		t.Fatal(err)
	}
	state = change.Result.State
	older := PlaybackProgressSample{Sequence: 4, PositionSeconds: 20, DurationSeconds: 1000}
	request := StopPlaybackProgressRequest{Scope: state.Scope, Fence: state.Fence, StopID: "stop", FinalSample: &older}
	stopped, err := PreparePlaybackStop(&state, request)
	if err != nil {
		t.Fatal(err)
	}
	if stopped.Sample.PositionSeconds != 980 || stopped.Result.State.Stop.Accepted.Sample.Sequence != 5 {
		t.Fatal("stop used stale or normalized sample")
	}
	if state.Stop != nil {
		t.Fatal("stop preparation mutated input state")
	}
	state = stopped.Result.State
	state.Stop.History = &WatchHistoryEntry{ID: "committed-history", Completed: true}
	replay, err := PreparePlaybackStop(&state, request)
	if err != nil || replay.Changed || replay.Sample != nil || replay.Result.State.Stop.History.ID != "committed-history" {
		t.Fatalf("stop replay: %+v %v", replay, err)
	}
	next := state.Fence
	next.Epoch++
	if _, err := PreparePlaybackAuthority(&state, InstallPlaybackAuthorityRequest{Scope: state.Scope, Expected: &state.Fence, Next: next}); !errors.Is(err, ErrPlaybackSinkStopped) {
		t.Fatalf("reopened stopped state: %v", err)
	}
	request.StopID = "another-stop"
	if _, err := PreparePlaybackStop(&state, request); !errors.Is(err, ErrPlaybackSinkConflict) {
		t.Fatalf("changed stop ID: %v", err)
	}
}

func TestPlaybackSinkCanonicalValidation(t *testing.T) {
	state := playbackSinkState(t)
	for _, value := range []float64{math.NaN(), math.Inf(1), math.Inf(-1), -1} {
		_, err := PreparePlaybackProgress(&state, ApplyPlaybackProgressRequest{Scope: state.Scope, Fence: state.Fence, Sample: PlaybackProgressSample{Sequence: 1, PositionSeconds: value}})
		if !errors.Is(err, ErrPlaybackSinkInvalid) {
			t.Fatalf("accepted non-finite/negative position: %v", err)
		}
	}
	sample := PlaybackProgressSample{Sequence: 1, DurationSeconds: 1000}
	first, err := playbackSampleDigest(sample)
	if err != nil {
		t.Fatal(err)
	}
	sample.PositionSeconds = math.Copysign(0, -1)
	second, err := playbackSampleDigest(sample)
	if err != nil || first != second {
		t.Fatal("negative zero caused conflicting replay")
	}
	for _, final := range []bool{false, true} {
		for _, sample := range []PlaybackProgressSample{{Sequence: 1}, {Sequence: 1, PositionSeconds: 900, DurationSeconds: 1000, PersistenceDisabled: true}} {
			if progress, hints, history := PlaybackProjectionPolicy(sample, final); progress || hints || history {
				t.Fatal("disabled/zero sample projected")
			}
		}
	}
}
