package notifications

import (
	"context"
	"errors"
	"path/filepath"
	"testing"

	"github.com/Silo-Server/silo-server/internal/userdb"

	"github.com/Silo-Server/silo-server/internal/userstore"
	"github.com/google/uuid"
)

type notificationSourceProvider struct {
	userstore.UserStoreProvider // ForUser and provider Close must never be called.
	open                        func(context.Context, userstore.PlaybackSourceRef) (userstore.PlaybackSinkHandle, error)
}

func (p notificationSourceProvider) OpenPlaybackSink(ctx context.Context, ref userstore.PlaybackSourceRef) (userstore.PlaybackSinkHandle, error) {
	return p.open(ctx, ref)
}

type notificationSourceHandle struct {
	playbackSinkStub
	ref    userstore.PlaybackSourceRef
	closes int
}

func (h *notificationSourceHandle) Source() userstore.PlaybackSourceRef { return h.ref }
func (h *notificationSourceHandle) Close() error                        { h.closes++; return nil }

func TestNotificationExactSourceHandle(t *testing.T) {
	ref := userstore.PlaybackSourceRef{Backend: "sqlite", AccountID: 7, SourceID: uuid.NewString(), SelectionGeneration: 3}
	scope := userstore.PlaybackProgressScope{ProfileID: "profile", SessionID: "session", MediaItemID: "movie"}
	handle := &notificationSourceHandle{ref: ref}
	calls := 0
	handle.apply = func(ctx context.Context, request userstore.ApplyPlaybackProgressRequest) (userstore.PlaybackProgressResult, error) {
		calls++
		if ctx != t.Context() || request.Scope != scope {
			t.Fatal("captured request changed")
		}
		if handle.closes > 0 {
			return userstore.PlaybackProgressResult{}, userstore.ErrPlaybackSourceClosed
		}
		return userstore.PlaybackProgressResult{Outcome: "applied", ProgressChanged: true, After: userstore.PlaybackProjectionState{Exists: true, PositionSeconds: 12}}, nil
	}
	handle.stop = func(context.Context, userstore.StopPlaybackProgressRequest) (userstore.PlaybackProgressResult, error) {
		return userstore.PlaybackProgressResult{Outcome: "stopped", HistoryCreated: true}, nil
	}
	opened := 0
	provider := notificationSourceProvider{open: func(ctx context.Context, got userstore.PlaybackSourceRef) (userstore.PlaybackSinkHandle, error) {
		opened++
		if ctx != t.Context() || got != ref {
			t.Fatal("source selection changed")
		}
		return handle, nil
	}}
	updater := &InterestUpdater{pending: map[interestMutation]int{}}
	wrapped := WrapUserStoreProvider(provider, &System{Interest: updater}).(userstore.PlaybackSourceProvider)
	got, err := wrapped.OpenPlaybackSink(t.Context(), ref)
	if err != nil || got.Source() != ref {
		t.Fatalf("open: %v", err)
	}
	if _, err := got.ApplyPlaybackProgress(t.Context(), userstore.ApplyPlaybackProgressRequest{Scope: scope}); err != nil {
		t.Fatal(err)
	}
	if len(updater.pending) != 1 {
		t.Fatal("bound progress lost interest notification")
	}
	clear(updater.pending)
	if _, err := got.StopPlaybackProgress(t.Context(), userstore.StopPlaybackProgressRequest{Scope: scope}); err != nil {
		t.Fatal(err)
	}
	if len(updater.pending) != 1 {
		t.Fatal("bound stop lost interest notification")
	}
	clear(updater.pending)
	if err := got.Close(); err != nil {
		t.Fatal(err)
	}
	if _, err := got.ApplyPlaybackProgress(t.Context(), userstore.ApplyPlaybackProgressRequest{Scope: scope}); !errors.Is(err, userstore.ErrPlaybackSourceClosed) {
		t.Fatalf("closed result changed: %v", err)
	}
	if opened != 1 || handle.closes != 1 || calls != 1 || len(updater.pending) != 0 {
		t.Fatal("wrapper reopened selection, lost release or queued failed write")
	}
}

func TestNotificationSourceOpenFailures(t *testing.T) {
	ref := userstore.PlaybackSourceRef{Backend: "postgres", AccountID: 7, SourceID: uuid.NewString(), SelectionGeneration: 1}
	failure := errors.New("selected source unavailable")
	for _, scenario := range []string{"unsupported", "failure", "nil", "mismatch"} {
		t.Run(scenario, func(t *testing.T) {
			var inner userstore.UserStoreProvider = struct{ userstore.UserStoreProvider }{}
			handle := &notificationSourceHandle{ref: ref}
			handle.ref.SelectionGeneration++
			if scenario != "unsupported" {
				inner = notificationSourceProvider{open: func(context.Context, userstore.PlaybackSourceRef) (userstore.PlaybackSinkHandle, error) {
					switch scenario {
					case "failure":
						return nil, failure
					case "nil":
						return nil, nil
					default:
						return handle, nil
					}
				}}
			}
			provider := WrapUserStoreProvider(inner, &System{}).(userstore.PlaybackSourceProvider)
			got, err := provider.OpenPlaybackSink(t.Context(), ref)
			if got != nil || err == nil {
				t.Fatal("invalid open succeeded")
			}
			want := userstore.ErrPlaybackSinkUnsupported
			switch scenario {
			case "failure":
				want = failure
			case "nil":
				want = userstore.ErrPlaybackSourceUnavailable
			case "mismatch":
				want = userstore.ErrPlaybackSourceMismatch
			}
			if !errors.Is(err, want) {
				t.Fatalf("error changed: %v", err)
			}
			if scenario == "mismatch" && handle.closes != 1 {
				t.Fatal("mismatched handle leaked")
			}
		})
	}
}

func TestNotificationSQLiteSourceHandleIntegration(t *testing.T) {
	dir := t.TempDir()
	db, err := userdb.NewUserDB(filepath.Join(dir, "7.db"), 7)
	if err != nil {
		t.Fatal(err)
	}
	ref := userstore.PlaybackSourceRef{Backend: "sqlite", AccountID: 7, SourceID: uuid.NewString(), SelectionGeneration: 1}
	if _, err := db.DB.Exec("INSERT INTO playback_source_markers(user_id,source_id,selection_generation,gate) VALUES(?,?,?,'writable')", 7, ref.SourceID, 1); err != nil {
		t.Fatal(err)
	}
	if err := db.Close(); err != nil {
		t.Fatal(err)
	}
	provider := userdb.NewSQLiteProvider(userdb.NewUserDBPool(userdb.PoolConfig{DataDir: dir}))
	t.Cleanup(func() { _ = provider.Close() })
	updater := &InterestUpdater{pending: map[interestMutation]int{}}
	wrapped := WrapUserStoreProvider(provider, &System{Interest: updater})
	handle, err := wrapped.(userstore.PlaybackSourceProvider).OpenPlaybackSink(t.Context(), ref)
	if err != nil {
		t.Fatal(err)
	}
	t.Cleanup(func() { _ = handle.Close() })
	store, err := provider.ForUser(t.Context(), 7)
	if err != nil {
		t.Fatal(err)
	}
	scope := userstore.PlaybackProgressScope{ProfileID: uuid.NewString(), SessionID: uuid.NewString(), MediaItemID: uuid.NewString()}
	if err := store.CreateProfile(t.Context(), userstore.Profile{ID: scope.ProfileID, Name: "Source fixture"}); err != nil {
		t.Fatal(err)
	}
	fence := userstore.PlaybackProgressFence{AttemptID: uuid.NewString(), Incarnation: uuid.NewString(), OwnerID: uuid.NewString(), Epoch: 1}
	if _, err := handle.InstallPlaybackAuthority(t.Context(), userstore.InstallPlaybackAuthorityRequest{Scope: scope, Next: fence}); err != nil {
		t.Fatal(err)
	}
	if _, err := handle.ApplyPlaybackProgress(t.Context(), userstore.ApplyPlaybackProgressRequest{Scope: scope, Fence: fence, Sample: userstore.PlaybackProgressSample{Sequence: 1, PositionSeconds: 120, DurationSeconds: 1000}}); err != nil {
		t.Fatal(err)
	}
	row, err := store.GetProgress(t.Context(), scope.ProfileID, scope.MediaItemID)
	if err != nil || row == nil || row.PositionSeconds != 120 || len(updater.pending) != 1 {
		t.Fatalf("committed observer state: %+v %v pending=%d", row, err, len(updater.pending))
	}
	if err := handle.Close(); err != nil {
		t.Fatal(err)
	}
	if _, err := handle.ReadPlaybackProgress(t.Context(), scope); !errors.Is(err, userstore.ErrPlaybackSourceClosed) {
		t.Fatalf("closed handle: %v", err)
	}
	if _, err := provider.ForUser(t.Context(), 7); err != nil {
		t.Fatalf("handle closed provider: %v", err)
	}
}

func TestNotificationSourceCloseWaitsForWriteAndObserver(t *testing.T) {
	ref := userstore.PlaybackSourceRef{Backend: "sqlite", AccountID: 7, SourceID: uuid.NewString(), SelectionGeneration: 1}
	entered, release, writeDone, closeStarted, closeDone := make(chan struct{}), make(chan struct{}), make(chan struct{}), make(chan struct{}), make(chan struct{})
	updater := &InterestUpdater{pending: map[interestMutation]int{}}
	inner := &notificationSourceHandle{ref: ref}
	inner.apply = func(context.Context, userstore.ApplyPlaybackProgressRequest) (userstore.PlaybackProgressResult, error) {
		close(entered)
		<-release
		return userstore.PlaybackProgressResult{Outcome: "applied", HistoryCreated: true}, nil
	}
	wrapped := &interestPlaybackSinkHandle{PlaybackSinkHandle: inner, observer: &interestTrackingStore{userID: 7, updater: updater}}
	go func() {
		defer close(writeDone)
		_, _ = wrapped.ApplyPlaybackProgress(t.Context(), userstore.ApplyPlaybackProgressRequest{Scope: userstore.PlaybackProgressScope{ProfileID: "profile", MediaItemID: "movie"}})
	}()
	<-entered
	go func() { close(closeStarted); _ = wrapped.Close(); close(closeDone) }()
	<-closeStarted
	select {
	case <-closeDone:
		t.Fatal("Close passed blocked write")
	default:
	}
	close(release)
	<-writeDone
	<-closeDone
	if len(updater.pending) != 1 || inner.closes != 1 {
		t.Fatal("Close lost committed observer or handle release")
	}
	if err := wrapped.Close(); err != nil || inner.closes != 1 {
		t.Fatal("Close released handle twice")
	}
}
