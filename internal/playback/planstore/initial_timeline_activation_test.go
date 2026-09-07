package planstore

import (
	"errors"
	"sync"
	"testing"

	"github.com/Silo-Server/silo-server/internal/playback"
	"github.com/google/uuid"
)

func initialTimelineFixture(t *testing.T) *initialActivationFixture {
	t.Helper()
	f := newInitialActivationFixture(t)
	timeline, err := playback.NewClientPlaybackTimelineV3(f.binding.Scope.MediaItemID, "edition", []playback.ClientPlaybackPartV3{{FileID: f.mediaFileID, DurationSeconds: 600}, {FileID: f.altFileID, DurationSeconds: 400}}, f.mediaFileID)
	if err != nil {
		t.Fatal(err)
	}
	f.binding.ClientTimeline = timeline
	f.binding.Progress.DurationSeconds = timeline.DurationSeconds
	f.binding.Progress.Hints.FileID = timeline.FileID
	return f
}

func nextTimelineBinding(t *testing.T, f *initialActivationFixture, profile string, bound bool) playback.InitialActivationBindingV3 {
	t.Helper()
	request := f.reservation
	request.PlaybackAttemptID = uuid.NewString()
	request.RequestedMediaFileID = f.altFileID
	request.NormalizedRequest.FileID = f.altFileID
	request.ProfileID = profile
	request.NormalizedRequest.ProfileID = profile
	request.ExpectedAdmissionID = f.binding.AdmissionID
	reserved, err := f.store.ReserveAttempt(t.Context(), request)
	if err != nil || !reserved.Owned {
		t.Fatalf("reserve next part: %+v %v", reserved, err)
	}
	binding := f.binding
	binding.IntentID, binding.Scope.SessionID, binding.Scope.ProfileID = uuid.NewString(), uuid.NewString(), profile
	binding.Fence.AttemptID, binding.Fence.Incarnation, binding.Fence.OwnerID, binding.Fence.Epoch = request.PlaybackAttemptID, reserved.Authority.Incarnation, reserved.Authority.OwnerID, reserved.Authority.Epoch
	binding.ClientTimeline.FileID = f.altFileID
	binding.ClientTimeline.PartOffsetSeconds = 600
	binding.ClientTimeline.PartDurationSeconds = 400
	binding.Progress.Hints.FileID = f.altFileID
	if !bound {
		binding.ClientTimeline = playback.ClientPlaybackTimelineV3{}
	}
	return binding
}

func TestInitialTimelinePartRequiresTerminalReceipt(t *testing.T) {
	f := initialTimelineFixture(t)
	f.begin(t)
	next := nextTimelineBinding(t, f, f.binding.Scope.ProfileID, true)
	requireBusy := func() {
		t.Helper()
		if _, err := f.store.BeginInitialActivation(t.Context(), next); !errors.Is(err, playback.ErrClientPlaybackTimelineBusyV3) {
			t.Fatalf("nonterminal part admitted: %v", err)
		}
	}
	requireBusy()
	if _, err := f.store.BeginInitialActivation(t.Context(), nextTimelineBinding(t, f, "another-profile", true)); err != nil {
		t.Fatalf("cross-profile blocked: %v", err)
	}
	if _, err := f.store.BeginInitialActivation(t.Context(), nextTimelineBinding(t, f, f.binding.Scope.ProfileID, false)); err != nil {
		t.Fatalf("unbound session blocked: %v", err)
	}
	abortID := uuid.NewString()
	if _, err := f.store.CancelInitialActivation(t.Context(), f.binding, abortID); err != nil {
		t.Fatal(err)
	}
	requireBusy() // A pending or lost terminal receipt is still nonterminal.
	terminal := terminalInitialReceipt(t, f, abortID)
	if _, err := f.store.CompleteInitialAbort(t.Context(), f.binding, abortID, f.receipt(t, terminal)); err != nil {
		t.Fatal(err)
	}
	if _, err := f.store.BeginInitialActivation(t.Context(), next); err != nil {
		t.Fatalf("terminal part blocked successor: %v", err)
	}
}

func TestInitialTimelineConcurrentPartsHaveOneWinner(t *testing.T) {
	f := initialTimelineFixture(t)
	first := f.binding
	second := nextTimelineBinding(t, f, first.Scope.ProfileID, true)
	peer, _ := authorityPeer(t, f.planstoreFixture)
	start := make(chan struct{})
	results := make(chan error, 2)
	var work sync.WaitGroup
	work.Go(func() { <-start; _, err := f.store.BeginInitialActivation(t.Context(), first); results <- err })
	work.Go(func() { <-start; _, err := peer.BeginInitialActivation(t.Context(), second); results <- err })
	close(start)
	work.Wait()
	close(results)
	won, refused := 0, 0
	for err := range results {
		if err == nil {
			won++
		} else if errors.Is(err, playback.ErrClientPlaybackTimelineBusyV3) {
			refused++
		} else {
			t.Fatal(err)
		}
	}
	if won != 1 || refused != 1 {
		t.Fatalf("winners=%d refused=%d", won, refused)
	}
}
