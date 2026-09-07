package pgstore

import (
	"errors"
	"testing"

	"github.com/Silo-Server/silo-server/internal/playback"
	"github.com/Silo-Server/silo-server/internal/userstore"
	"github.com/google/uuid"
)

func TestClientTimelinePostgresReceiptOrdering(t *testing.T) {
	f := newPlaybackSinkFixture(t)
	f.scope.SessionID = uuid.NewString()
	f.scope.MediaItemID = "book"
	f.fence.Incarnation = uuid.NewString()
	f.fence.OwnerID = uuid.NewString()
	f.install(t)
	timeline, err := playback.NewClientPlaybackTimelineV3("book", "edition", []playback.ClientPlaybackPartV3{{FileID: 1, DurationSeconds: 600}, {FileID: 2, DurationSeconds: 400}}, 2)
	if err != nil {
		t.Fatal(err)
	}
	binding := playback.InitialActivationBindingV3{ClientTimeline: timeline, Progress: userstore.PlaybackProgressSample{DurationSeconds: 1000, Hints: userstore.VersionHints{FileID: 2}}, Source: userstore.PlaybackSourceRef{Backend: "postgres", AccountID: f.store.userID, SourceID: uuid.NewString(), SelectionGeneration: 1}, Scope: f.scope, Fence: f.fence, IntentID: uuid.NewString(), AdmissionID: uuid.NewString()}
	sample, err := binding.ClientTimelineSample(timeline.TimelineID, 2, 30, false)
	if err != nil {
		t.Fatal(err)
	}
	request := userstore.ApplyPlaybackProgressRequest{Scope: f.scope, Fence: f.fence, Sample: sample}
	if _, err := f.store.ApplyPlaybackProgress(t.Context(), request); err != nil {
		t.Fatal(err)
	} // lost response
	replay, err := f.store.ApplyPlaybackProgress(t.Context(), request)
	if err != nil || replay.Outcome != userstore.PlaybackProgressReplayed || replay.State.Last.Sample.PositionSeconds != 630 {
		t.Fatalf("replay %+v %v", replay, err)
	}
	progress, err := f.store.GetProgress(t.Context(), f.scope.ProfileID, "book")
	if err != nil || progress.PositionSeconds != 630 || progress.DurationSeconds != 1000 {
		t.Fatalf("global progress %+v %v", progress, err)
	}
	changed := request
	changed.Sample.PositionSeconds = 631
	if _, err := f.store.ApplyPlaybackProgress(t.Context(), changed); !errors.Is(err, userstore.ErrPlaybackSinkConflict) {
		t.Fatalf("changed same sequence %v", err)
	}
	stale := request
	stale.Sample.Sequence = 1
	stale.Sample.PositionSeconds = 610
	if got, err := f.store.ApplyPlaybackProgress(t.Context(), stale); err != nil || got.Outcome != userstore.PlaybackProgressStaleSample {
		t.Fatalf("stale %+v %v", got, err)
	}
	final, err := binding.ClientTimelineSample(timeline.TimelineID, 3, 40, true)
	if err != nil {
		t.Fatal(err)
	}
	stop := userstore.StopPlaybackProgressRequest{Scope: f.scope, Fence: f.fence, StopID: uuid.NewString(), FinalSample: &final}
	terminal, err := f.store.StopPlaybackProgress(t.Context(), stop)
	if err != nil || terminal.State.Last.Sample.PositionSeconds != 640 {
		t.Fatalf("terminal %+v %v", terminal, err)
	}
	if _, err := f.store.StopPlaybackProgress(t.Context(), stop); err != nil {
		t.Fatal(err)
	}
	if _, err := f.store.ApplyPlaybackProgress(t.Context(), request); !errors.Is(err, userstore.ErrPlaybackSinkStopped) {
		t.Fatalf("late old part wrote: %v", err)
	}
	var histories int
	if err := f.pool.QueryRow(t.Context(), `SELECT count(*) FROM user_watch_history WHERE user_id=$1 AND profile_id=$2 AND media_item_id='book'`, f.store.userID, f.scope.ProfileID).Scan(&histories); err != nil || histories != 1 {
		t.Fatalf("terminal history count %d: %v", histories, err)
	}
	state, err := f.store.ReadPlaybackProgress(t.Context(), f.scope)
	if err != nil || state.Fence != binding.Fence || state.Stop.StopID != stop.StopID {
		t.Fatalf("fence/receipt changed %+v %v", state, err)
	}
}
