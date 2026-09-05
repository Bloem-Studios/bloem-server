package notifications

import (
	"context"
	"database/sql"
	"errors"
	"path/filepath"
	"reflect"
	"testing"

	"github.com/Silo-Server/silo-server/internal/userdb"
	"github.com/Silo-Server/silo-server/internal/userstore/storetest"

	"github.com/Silo-Server/silo-server/internal/userstore"
)

type playbackSinkStub struct {
	userstore.UserStore // Any accidental legacy fallback panics.
	read                func(context.Context, userstore.PlaybackProgressScope) (userstore.PlaybackProgressState, error)
	install             func(context.Context, userstore.InstallPlaybackAuthorityRequest) (userstore.PlaybackProgressResult, error)
	apply               func(context.Context, userstore.ApplyPlaybackProgressRequest) (userstore.PlaybackProgressResult, error)
	stop                func(context.Context, userstore.StopPlaybackProgressRequest) (userstore.PlaybackProgressResult, error)
}

func (s playbackSinkStub) ReadPlaybackProgress(c context.Context, r userstore.PlaybackProgressScope) (userstore.PlaybackProgressState, error) {
	return s.read(c, r)
}
func (s playbackSinkStub) InstallPlaybackAuthority(c context.Context, r userstore.InstallPlaybackAuthorityRequest) (userstore.PlaybackProgressResult, error) {
	return s.install(c, r)
}
func (s playbackSinkStub) ApplyPlaybackProgress(c context.Context, r userstore.ApplyPlaybackProgressRequest) (userstore.PlaybackProgressResult, error) {
	return s.apply(c, r)
}
func (s playbackSinkStub) StopPlaybackProgress(c context.Context, r userstore.StopPlaybackProgressRequest) (userstore.PlaybackProgressResult, error) {
	return s.stop(c, r)
}

func wrappedPlaybackSink(t *testing.T, inner userstore.UserStore) (userstore.PlaybackProgressSink, *InterestUpdater) {
	t.Helper()
	updater := &InterestUpdater{pending: map[interestMutation]int{}}
	provider := WrapUserStoreProvider(preferenceTransactionTestProvider{store: inner}, &System{Interest: updater})
	store, err := provider.ForUser(t.Context(), 7)
	if err != nil {
		t.Fatal(err)
	}
	sink, ok := store.(userstore.PlaybackProgressSink)
	if !ok {
		t.Fatal("wrapper lost sink capability")
	}
	return sink, updater
}

func TestPlaybackSinkWrapperExactDelegation(t *testing.T) {
	scope := userstore.PlaybackProgressScope{ProfileID: "profile", SessionID: "session", MediaItemID: "movie"}
	fence := userstore.PlaybackProgressFence{AttemptID: "attempt", Incarnation: "incarnation", OwnerID: "owner", Epoch: 2}
	result := userstore.PlaybackProgressResult{Outcome: "installed", State: userstore.PlaybackProgressState{Version: 1, Scope: scope, Fence: fence}}
	install := userstore.InstallPlaybackAuthorityRequest{Scope: scope, Next: fence}
	apply := userstore.ApplyPlaybackProgressRequest{Scope: scope, Fence: fence, Sample: userstore.PlaybackProgressSample{Sequence: 42, PositionSeconds: 120, Paused: true, PersistenceDisabled: true}}
	stop := userstore.StopPlaybackProgressRequest{Scope: scope, Fence: fence, StopID: "stop", FinalSample: &apply.Sample}
	calls := 0
	inner := playbackSinkStub{
		read: func(ctx context.Context, r userstore.PlaybackProgressScope) (userstore.PlaybackProgressState, error) {
			calls++
			if ctx != t.Context() || r != scope {
				t.Fatal("read arguments changed")
			}
			return result.State, nil
		},
		install: func(ctx context.Context, r userstore.InstallPlaybackAuthorityRequest) (userstore.PlaybackProgressResult, error) {
			calls++
			if ctx != t.Context() || !reflect.DeepEqual(r, install) {
				t.Fatal("install arguments changed")
			}
			return result, nil
		},
		apply: func(ctx context.Context, r userstore.ApplyPlaybackProgressRequest) (userstore.PlaybackProgressResult, error) {
			calls++
			if ctx != t.Context() || !reflect.DeepEqual(r, apply) {
				t.Fatal("apply arguments changed")
			}
			return result, nil
		},
		stop: func(ctx context.Context, r userstore.StopPlaybackProgressRequest) (userstore.PlaybackProgressResult, error) {
			calls++
			if ctx != t.Context() || !reflect.DeepEqual(r, stop) {
				t.Fatal("stop arguments changed")
			}
			return result, nil
		},
	}
	sink, updater := wrappedPlaybackSink(t, inner)
	state, err := sink.ReadPlaybackProgress(t.Context(), scope)
	if err != nil || !reflect.DeepEqual(state, result.State) {
		t.Fatal("read result changed")
	}
	for _, call := range []func() (userstore.PlaybackProgressResult, error){func() (userstore.PlaybackProgressResult, error) {
		return sink.InstallPlaybackAuthority(t.Context(), install)
	}, func() (userstore.PlaybackProgressResult, error) {
		return sink.ApplyPlaybackProgress(t.Context(), apply)
	}, func() (userstore.PlaybackProgressResult, error) { return sink.StopPlaybackProgress(t.Context(), stop) }} {
		got, err := call()
		if err != nil || !reflect.DeepEqual(got, result) {
			t.Fatal("result changed")
		}
	}
	if calls != 4 || len(updater.pending) != 0 {
		t.Fatal("delegation side effects")
	}
}

func TestPlaybackSinkWrapperQueuesOnlyCommittedInterest(t *testing.T) {
	for _, scenario := range []string{"transition", "ordinary-tick", "hints-only", "completed-rewatch", "replay", "stale", "rollback"} {
		t.Run(scenario, func(t *testing.T) {
			scope := userstore.PlaybackProgressScope{ProfileID: "profile", SessionID: "session", MediaItemID: "movie"}
			result := userstore.PlaybackProgressResult{Outcome: "applied", ProgressChanged: true, After: userstore.PlaybackProjectionState{Exists: true, PositionSeconds: 120}}
			var failure error
			want := false
			switch scenario {
			case "transition":
				want = true
			case "ordinary-tick":
				result.Before = userstore.PlaybackProjectionState{Exists: true, PositionSeconds: 100}
			case "hints-only":
				result.ProgressChanged = false
				result.HintsChanged = true
			case "completed-rewatch":
				result.Outcome = "stopped"
				result.ProgressChanged = false
				result.HistoryCreated = true
				result.Before = userstore.PlaybackProjectionState{Exists: true, Completed: true}
				result.After = result.Before
				result.State.Stop = &userstore.PlaybackStopReceipt{History: &userstore.WatchHistoryEntry{Completed: true}}
				want = true
			case "replay":
				result.Outcome = "replayed"
				result.HistoryCreated = true
			case "stale":
				result.Outcome = "stale_sample"
			case "rollback":
				failure = errors.New("commit failed")
			}
			entered, release, done := make(chan struct{}), make(chan struct{}), make(chan struct{})
			committed := false
			call := func(context.Context, userstore.ApplyPlaybackProgressRequest) (userstore.PlaybackProgressResult, error) {
				close(entered)
				<-release
				committed = failure == nil
				return result, failure
			}
			sink, updater := wrappedPlaybackSink(t, playbackSinkStub{apply: call, stop: func(ctx context.Context, _ userstore.StopPlaybackProgressRequest) (userstore.PlaybackProgressResult, error) {
				return call(ctx, userstore.ApplyPlaybackProgressRequest{})
			}})
			go func() {
				defer close(done)
				if scenario == "completed-rewatch" {
					_, _ = sink.StopPlaybackProgress(t.Context(), userstore.StopPlaybackProgressRequest{Scope: scope})
				} else {
					_, _ = sink.ApplyPlaybackProgress(t.Context(), userstore.ApplyPlaybackProgressRequest{Scope: scope})
				}
			}()
			<-entered
			if len(updater.pending) != 0 {
				t.Fatal("queued before inner transaction returned")
			}
			close(release)
			<-done
			if got := len(updater.pending) > 0; got != want {
				t.Fatalf("queued=%v want=%v", got, want)
			}
			if want && !committed {
				t.Fatal("queued without commit")
			}
		})
	}
}

func TestPlaybackSinkWrapperUnsupportedHasNoFallback(t *testing.T) {
	// Embedding only the base interface must not invent an implementation.
	inner := struct{ userstore.UserStore }{}
	sink, updater := wrappedPlaybackSink(t, inner)
	_, readErr := sink.ReadPlaybackProgress(t.Context(), userstore.PlaybackProgressScope{})
	_, installErr := sink.InstallPlaybackAuthority(t.Context(), userstore.InstallPlaybackAuthorityRequest{})
	_, applyErr := sink.ApplyPlaybackProgress(t.Context(), userstore.ApplyPlaybackProgressRequest{})
	_, stopErr := sink.StopPlaybackProgress(t.Context(), userstore.StopPlaybackProgressRequest{})
	for _, err := range []error{readErr, installErr, applyErr, stopErr} {
		if !errors.Is(err, userstore.ErrPlaybackSinkUnsupported) {
			t.Fatalf("unsupported: %v", err)
		}
	}
	if len(updater.pending) != 0 {
		t.Fatal("unsupported mutation queued")
	}
}

// Run the concrete selected-store contract through the production decorator,
// not just the bare backend. This catches lost optional methods and fallbacks.
func TestPlaybackSinkProductionWrapperSQLite(t *testing.T) {
	storetest.PlaybackSink(t, func(t *testing.T) userstore.UserStore {
		db, err := sql.Open("sqlite3", filepath.Join(t.TempDir(), "account.db")+"?_journal_mode=WAL&_busy_timeout=5000")
		if err != nil {
			t.Fatal(err)
		}
		t.Cleanup(func() { _ = db.Close() })
		if err := userdb.InitSchema(db); err != nil {
			t.Fatal(err)
		}
		updater := &InterestUpdater{pending: map[interestMutation]int{}}
		provider := WrapUserStoreProvider(preferenceTransactionTestProvider{store: userdb.NewSQLiteUserStore(db)}, &System{Interest: updater})
		store, err := provider.ForUser(t.Context(), 7)
		if err != nil {
			t.Fatal(err)
		}
		return store
	})
}
