package storetest

import (
	"errors"
	"math"
	"reflect"
	"testing"
	"time"

	"github.com/Silo-Server/silo-server/internal/userstore"
	"github.com/google/uuid"
)

// PlaybackSink runs the same storage contract against each selected backend.
// newStore returns an initialized, isolated store; this suite creates profiles.
func PlaybackSink(t *testing.T, newStore func(*testing.T) userstore.UserStore) {
	const testResolution = "1080p"
	for _, scenario := range []string{"sequencing", "authority", "terminal", "suppression", "completion", "below-threshold", "rewatch", "empty-stop"} {
		t.Run(scenario, func(t *testing.T) {
			ctx := t.Context()
			store := newStore(t)
			sink, ok := store.(userstore.PlaybackProgressSink)
			if !ok {
				t.Fatal("missing PlaybackProgressSink")
			}
			scope := userstore.PlaybackProgressScope{ProfileID: uuid.NewString(), SessionID: uuid.NewString(), MediaItemID: uuid.NewString()}
			if err := store.CreateProfile(ctx, userstore.Profile{ID: scope.ProfileID, Name: "Sink fixture"}); err != nil {
				t.Fatal(err)
			}
			fence := userstore.PlaybackProgressFence{AttemptID: uuid.NewString(), Incarnation: uuid.NewString(), OwnerID: uuid.NewString(), Epoch: 1}
			install := userstore.InstallPlaybackAuthorityRequest{Scope: scope, Next: fence}
			if _, err := sink.ReadPlaybackProgress(ctx, scope); !errors.Is(err, userstore.ErrPlaybackSinkNotFound) {
				t.Fatalf("absent read: %v", err)
			}
			if got, err := sink.InstallPlaybackAuthority(ctx, install); err != nil || got.Outcome != userstore.PlaybackProgressInstalled {
				t.Fatalf("install: %+v %v", got, err)
			}
			if got, err := sink.InstallPlaybackAuthority(ctx, install); err != nil || got.Outcome != userstore.PlaybackProgressReplayed {
				t.Fatalf("install replay: %+v %v", got, err)
			}
			apply := func(sequence int64, position float64) userstore.PlaybackProgressResult {
				t.Helper()
				got, err := sink.ApplyPlaybackProgress(ctx, userstore.ApplyPlaybackProgressRequest{Scope: scope, Fence: fence, Sample: userstore.PlaybackProgressSample{Sequence: sequence, PositionSeconds: position, DurationSeconds: 1000, Hints: userstore.VersionHints{FileID: 42, Resolution: testResolution}}})
				if err != nil {
					t.Fatal(err)
				}
				return got
			}
			stop := userstore.StopPlaybackProgressRequest{Scope: scope, Fence: fence, StopID: uuid.NewString()}
			switch scenario {
			case "sequencing":
				apply(41, 600)
				got := apply(42, 120)
				if got.State.Last.Sample.PositionSeconds != 120 {
					t.Fatal("backward seek lost")
				}
				row, err := store.GetProgress(ctx, scope.ProfileID, scope.MediaItemID)
				if err != nil || row == nil || row.PositionSeconds != 120 {
					t.Fatalf("projection: %+v %v", row, err)
				}
				stale := apply(41, 600)
				if stale.Outcome != userstore.PlaybackProgressStaleSample || stale.State.Last.Sample.Sequence != 42 {
					t.Fatalf("stale: %+v", stale)
				}
				replay := apply(42, 120)
				if replay.Outcome != userstore.PlaybackProgressReplayed || replay.ProgressChanged || replay.HistoryCreated {
					t.Fatalf("replay: %+v", replay)
				}
				after, _ := store.GetProgress(ctx, scope.ProfileID, scope.MediaItemID)
				if !reflect.DeepEqual(row, after) {
					t.Fatal("replay changed projection or timestamp")
				}
				req := userstore.ApplyPlaybackProgressRequest{Scope: scope, Fence: fence, Sample: got.State.Last.Sample}
				req.Sample.PositionSeconds++
				if _, err := sink.ApplyPlaybackProgress(ctx, req); !errors.Is(err, userstore.ErrPlaybackSinkConflict) {
					t.Fatalf("conflict: %v", err)
				}
				req.Sample.Sequence++
				req.Sample.PositionSeconds = math.NaN()
				if _, err := sink.ApplyPlaybackProgress(ctx, req); !errors.Is(err, userstore.ErrPlaybackSinkInvalid) {
					t.Fatalf("NaN: %v", err)
				}
			case "authority":
				apply(5, 200)
				next := fence
				next.Epoch++
				next.OwnerID = uuid.NewString()
				if _, err := sink.InstallPlaybackAuthority(ctx, userstore.InstallPlaybackAuthorityRequest{Scope: scope, Next: next}); !errors.Is(err, userstore.ErrPlaybackSinkStale) {
					t.Fatalf("missing predecessor: %v", err)
				}
				result, err := sink.InstallPlaybackAuthority(ctx, userstore.InstallPlaybackAuthorityRequest{Scope: scope, Expected: &fence, Next: next})
				if err != nil || result.State.Last.Sample.Sequence != 5 {
					t.Fatalf("advance: %+v %v", result, err)
				}
				req := userstore.ApplyPlaybackProgressRequest{Scope: scope, Fence: fence, Sample: userstore.PlaybackProgressSample{Sequence: 6, PositionSeconds: 900, DurationSeconds: 1000}}
				if _, err := sink.ApplyPlaybackProgress(ctx, req); !errors.Is(err, userstore.ErrPlaybackSinkStale) {
					t.Fatalf("stale write: %v", err)
				}
				wrong := next
				wrong.OwnerID = uuid.NewString()
				if _, err := sink.InstallPlaybackAuthority(ctx, userstore.InstallPlaybackAuthorityRequest{Scope: scope, Expected: &next, Next: wrong}); !errors.Is(err, userstore.ErrPlaybackSinkStale) {
					t.Fatalf("same epoch owner: %v", err)
				}
				wrong = next
				wrong.Epoch++
				wrong.Incarnation = uuid.NewString()
				if _, err := sink.InstallPlaybackAuthority(ctx, userstore.InstallPlaybackAuthorityRequest{Scope: scope, Expected: &next, Next: wrong}); !errors.Is(err, userstore.ErrPlaybackSinkStale) {
					t.Fatalf("incarnation: %v", err)
				}
				req.Fence = next
				req.Scope.MediaItemID = uuid.NewString()
				if _, err := sink.ApplyPlaybackProgress(ctx, req); !errors.Is(err, userstore.ErrPlaybackSinkStale) {
					t.Fatalf("wrong target: %v", err)
				}
			case "terminal":
				latest := apply(2, 600)
				bad := latest.State.Last.Sample
				bad.PositionSeconds = 601
				stop.FinalSample = &bad
				if _, err := sink.StopPlaybackProgress(ctx, stop); !errors.Is(err, userstore.ErrPlaybackSinkConflict) {
					t.Fatalf("stop conflict: %v", err)
				}
				state, err := sink.ReadPlaybackProgress(ctx, scope)
				if err != nil || state.Stop != nil {
					t.Fatal("conflicting stop committed")
				}
				older := bad
				older.Sequence = 1
				older.PositionSeconds = 100
				stop.FinalSample = &older
				result, err := sink.StopPlaybackProgress(ctx, stop)
				if err != nil || !result.HistoryCreated || result.State.Stop.Accepted.Sample.PositionSeconds != 600 {
					t.Fatalf("stop latest: %+v %v", result, err)
				}
				if _, err := sink.ApplyPlaybackProgress(ctx, userstore.ApplyPlaybackProgressRequest{Scope: scope, Fence: fence, Sample: latest.State.Last.Sample}); !errors.Is(err, userstore.ErrPlaybackSinkStopped) {
					t.Fatalf("post-stop sample: %v", err)
				}
				next := fence
				next.Epoch++
				if _, err := sink.InstallPlaybackAuthority(ctx, userstore.InstallPlaybackAuthorityRequest{Scope: scope, Expected: &fence, Next: next}); !errors.Is(err, userstore.ErrPlaybackSinkStopped) {
					t.Fatalf("terminal advance: %v", err)
				}
				if got, err := sink.InstallPlaybackAuthority(ctx, install); err != nil || got.Outcome != userstore.PlaybackProgressReplayed || got.State.Stop == nil {
					t.Fatalf("terminal install replay: %+v %v", got, err)
				}
				if err := store.RemoveHistoryItems(ctx, scope.ProfileID, []string{scope.MediaItemID}, time.Now().Add(time.Hour)); err != nil {
					t.Fatal(err)
				}
				replay, err := sink.StopPlaybackProgress(ctx, stop)
				if err != nil || replay.Outcome != userstore.PlaybackProgressReplayed || replay.HistoryCreated || !reflect.DeepEqual(replay.State.Stop, result.State.Stop) {
					t.Fatalf("stop replay: %+v %v", replay, err)
				}
				history, err := store.ListHistory(ctx, scope.ProfileID, 20, 0)
				if err != nil || len(history) != 0 {
					t.Fatalf("deleted history revived: %+v %v", history, err)
				}
				stop.StopID = uuid.NewString()
				if _, err := sink.StopPlaybackProgress(ctx, stop); !errors.Is(err, userstore.ErrPlaybackSinkConflict) {
					t.Fatalf("changed stop: %v", err)
				}
			case "suppression":
				apply(1, 500)
				for sequence, disabled := range []bool{true, false} {
					sample := userstore.PlaybackProgressSample{Sequence: int64(sequence + 2), PositionSeconds: 0, DurationSeconds: 1000, PersistenceDisabled: disabled, Hints: userstore.VersionHints{FileID: 99}}
					if disabled {
						sample.PositionSeconds = 800
					}
					result, err := sink.ApplyPlaybackProgress(ctx, userstore.ApplyPlaybackProgressRequest{Scope: scope, Fence: fence, Sample: sample})
					if err != nil || result.ProgressChanged || result.HintsChanged || result.State.Last.Sample.Sequence != sample.Sequence {
						t.Fatalf("suppressed: %+v %v", result, err)
					}
				}
				result, err := sink.StopPlaybackProgress(ctx, stop)
				if err != nil || result.HistoryCreated || result.ProgressChanged {
					t.Fatalf("zero stop: %+v %v", result, err)
				}
				row, _ := store.GetProgress(ctx, scope.ProfileID, scope.MediaItemID)
				if row == nil || row.PositionSeconds != 500 || row.LastFileID == nil || *row.LastFileID != 42 {
					t.Fatalf("suppressed projection: %+v", row)
				}
			case "completion":
				apply(1, 900)
				row, _ := store.GetProgress(ctx, scope.ProfileID, scope.MediaItemID)
				if row == nil || row.Completed {
					t.Fatal("threshold must be strict")
				}
				result := apply(2, 901)
				if result.State.Last.Sample.PositionSeconds != 901 {
					t.Fatal("raw completed position normalized")
				}
				row, _ = store.GetProgress(ctx, scope.ProfileID, scope.MediaItemID)
				if row == nil || !row.Completed || row.PositionSeconds != 0 {
					t.Fatalf("completion projection: %+v", row)
				}
				final, err := sink.StopPlaybackProgress(ctx, stop)
				if err != nil || !final.HistoryCreated || final.State.Stop.History == nil || !final.State.Stop.History.Completed {
					t.Fatalf("completed history: %+v %v", final, err)
				}
			case "below-threshold":
				apply(1, 500)
				sample := userstore.PlaybackProgressSample{Sequence: 2, PositionSeconds: 10, DurationSeconds: 1000, Hints: userstore.VersionHints{FileID: 99}}
				result, err := sink.ApplyPlaybackProgress(ctx, userstore.ApplyPlaybackProgressRequest{Scope: scope, Fence: fence, Sample: sample})
				if err != nil || result.ProgressChanged || !result.HintsChanged {
					t.Fatalf("below-threshold heartbeat: %+v %v", result, err)
				}
				row, _ := store.GetProgress(ctx, scope.ProfileID, scope.MediaItemID)
				if row == nil || row.PositionSeconds != 500 || row.LastFileID == nil || *row.LastFileID != 99 {
					t.Fatalf("below-threshold hints: %+v", row)
				}
				result, err = sink.StopPlaybackProgress(ctx, stop)
				if err != nil || result.ProgressChanged || result.HintsChanged || result.HistoryCreated {
					t.Fatalf("below-threshold stop: %+v %v", result, err)
				}
			case "rewatch":
				if err := store.MarkWatched(ctx, scope.ProfileID, scope.MediaItemID, 1000); err != nil {
					t.Fatal(err)
				}
				apply(1, 950)
				result, err := sink.StopPlaybackProgress(ctx, stop)
				if err != nil || !result.Before.Completed || !result.HistoryCreated || result.State.Stop.History == nil || !result.State.Stop.History.Completed {
					t.Fatalf("completed rewatch: %+v %v", result, err)
				}
				replay, err := sink.StopPlaybackProgress(ctx, stop)
				if err != nil || replay.HistoryCreated {
					t.Fatalf("rewatch replay: %+v %v", replay, err)
				}
			case "empty-stop":
				result, err := sink.StopPlaybackProgress(ctx, stop)
				if err != nil || result.State.Stop == nil || result.State.Stop.Accepted != nil || result.HistoryCreated {
					t.Fatalf("empty stop: %+v %v", result, err)
				}
			}
		})
	}
}
