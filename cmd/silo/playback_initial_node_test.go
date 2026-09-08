package main

import (
	"context"
	"errors"
	"testing"
	"time"

	"github.com/Silo-Server/silo-server/internal/config"
	"github.com/Silo-Server/silo-server/internal/playback"
	"github.com/google/uuid"
)

func TestInitialNodePlaybackRequiresConfiguredDependencies(t *testing.T) {
	if runtime, err := configureInitialNodePlayback(t.Context(), &config.BootstrapConfig{}, nil, nil, 0, ""); err != nil || runtime != nil {
		t.Fatal("default-off runtime constructed", err)
	}
	for _, mode := range []string{"proxy", "transcode", "api"} {
		if runtime, err := configureInitialNodePlayback(t.Context(), &config.BootstrapConfig{Mode: mode, InitialPlaybackEnabled: true}, nil, nil, 0, ""); err == nil || runtime != nil {
			t.Fatal("missing node dependencies accepted")
		}
	}
}

func TestInitialNodePlaybackShutdownJoinsAcquisitionAndRefusesNewWork(t *testing.T) {
	n := newInitialNodeRuntime(t.Context(), nil)
	entered := make(chan struct{})
	done := make(chan error, 1)
	go func() {
		_, err := n.acquire(t.Context(), func(ctx context.Context) (*playback.RuntimeGrantV3, error) {
			close(entered)
			<-ctx.Done()
			return nil, ctx.Err()
		})
		done <- err
	}()
	<-entered
	ctx, cancel := context.WithTimeout(t.Context(), time.Second)
	defer cancel()
	if err := n.Shutdown(ctx); err != nil {
		t.Fatal(err)
	}
	if err := <-done; !errors.Is(err, context.Canceled) {
		t.Fatal("acquisition survived shutdown", err)
	}
	if _, err := n.acquire(t.Context(), func(context.Context) (*playback.RuntimeGrantV3, error) {
		t.Fatal("new acquisition reached authority after shutdown")
		return nil, nil
	}); err == nil {
		t.Fatal("new acquisition accepted")
	}
}

func TestInitialNodePlaybackShutdownJoinsIssuedGrant(t *testing.T) {
	n := newInitialNodeRuntime(t.Context(), nil)
	clock, err := playback.NewRuntimeGrantClockV3()
	if err != nil {
		t.Fatal(err)
	}
	_, policy := initialPlaybackTestingPolicies()
	executor := playback.ExecutorNamespaceV3{Incarnation: uuid.NewString(), Epoch: 1, ExecutorID: uuid.NewString()}
	authority := playback.AttemptAuthorityV3{PlaybackAttemptID: "attempt", Incarnation: executor.Incarnation, Epoch: 1, OwnerID: uuid.NewString(), State: playback.AttemptActiveV3, LeaseExpiresAt: time.Now().Add(time.Minute)}
	request := playback.AttemptGrantRequestV3{Executor: executor, SessionID: "session", PlanID: "plan", TransportID: "transport", NodeID: 1, Purpose: playback.AttemptGrantExecuteV3, Duration: policy.MaxDuration}
	grant, err := n.acquire(t.Context(), func(ctx context.Context) (*playback.RuntimeGrantV3, error) {
		return playback.AcquireRuntimeGrantV3(ctx, func(_ context.Context, a playback.AttemptAuthorityV3, r playback.AttemptGrantRequestV3) (playback.AttemptGrantV3, error) {
			now := time.Now()
			return playback.AttemptGrantV3{Authority: a, Request: r, IssuedAt: now, NotAfter: now.Add(r.Duration)}, nil
		}, clock, policy, authority, request)
	})
	if err != nil {
		t.Fatal(err)
	}
	ctx, cancel := context.WithTimeout(t.Context(), time.Second)
	defer cancel()
	if err := n.Shutdown(ctx); err != nil {
		t.Fatal(err)
	}
	select {
	case <-grant.Done():
	default:
		t.Fatal("shutdown returned before grant supervisor")
	}
	if err := grant.Check(); err == nil {
		t.Fatal("shutdown retained authority")
	}
}

func TestInitialNodePlaybackShutdownJoinsAuxiliaryPermitCleanup(t *testing.T) {
	n := newInitialNodeRuntime(t.Context(), nil)
	entered, release := make(chan struct{}), make(chan struct{})
	calls := 0
	_, cleanup, err := n.openPermit(t.Context(), func(context.Context) (string, func(), error) {
		return "auxiliary-permit", func() { calls++; close(entered); <-release }, nil
	})
	if err != nil {
		t.Fatal(err)
	}
	n.cancel()
	go cleanup()
	<-entered
	select {
	case <-n.done:
		t.Fatal("shutdown escaped auxiliary permit cleanup")
	default:
	}
	close(release)
	select {
	case <-n.done:
	case <-time.After(time.Second):
		t.Fatal("shutdown failed to join auxiliary permit cleanup")
	}
	cleanup()
	if calls != 1 {
		t.Fatal("permit cleanup repeated")
	}
	if _, _, err := n.OpenAuxiliary(t.Context(), "transport", playback.ExecutorNamespaceV3{}); err == nil {
		t.Fatal("shutdown accepted auxiliary permit")
	}
}

func TestInitialNodePlaybackShutdownCancelsAuxiliaryPermitAcquisition(t *testing.T) {
	n := newInitialNodeRuntime(t.Context(), nil)
	entered, result := make(chan struct{}), make(chan error, 1)
	go func() {
		_, _, err := n.openPermit(t.Context(), func(ctx context.Context) (string, func(), error) {
			close(entered)
			<-ctx.Done()
			return "", nil, ctx.Err()
		})
		result <- err
	}()
	<-entered
	ctx, cancel := context.WithTimeout(t.Context(), time.Second)
	defer cancel()
	if err := n.Shutdown(ctx); err != nil {
		t.Fatal(err)
	}
	if !errors.Is(<-result, context.Canceled) {
		t.Fatal("permit acquisition retained application lifetime")
	}
}
