package handlers

import (
	"context"
	"errors"
	"sync/atomic"
	"testing"
	"time"

	"github.com/Silo-Server/silo-server/internal/models"
	"github.com/Silo-Server/silo-server/internal/playback"
	"github.com/Silo-Server/silo-server/internal/transcodenode"
	"github.com/redis/go-redis/v9"
)

type nativeDeliveryRedisFailure struct{ failed atomic.Bool }

func (*nativeDeliveryRedisFailure) DialHook(next redis.DialHook) redis.DialHook { return next }
func (f *nativeDeliveryRedisFailure) ProcessHook(next redis.ProcessHook) redis.ProcessHook {
	return func(ctx context.Context, cmd redis.Cmder) error {
		if f.failed.Load() {
			return errors.New("delivery Redis unavailable")
		}
		return next(ctx, cmd)
	}
}

type nativeDeliveryJoiningSource struct {
	NativeVideoFile
	active                     *atomic.Bool
	first                      *atomic.Bool
	started, release, finished chan struct{}
}

func (f *nativeDeliveryJoiningSource) Cancel() {
	if f.active.Load() && f.first.CompareAndSwap(false, true) {
		close(f.started)
		<-f.release
		close(f.finished)
	}
	f.NativeVideoFile.Cancel()
}
func (f *nativeDeliveryJoiningSource) Close() error {
	if f.active.Load() && f.first.Load() {
		select {
		case <-f.finished:
		default:
			return errors.New("source closed before its cancellation callback joined")
		}
	}
	return f.NativeVideoFile.Close()
}

func TestNativeVideoWorkerDeliveryJoinsCancellationCallback(t *testing.T) {
	client := nativeVideoGrantRedis(t)
	service, session, start, card := nativeVideoGrantFixture(t, client)
	store := NewNativeVideoWorkerGrantStore(client)
	if err := store.Prepare(t.Context(), 7, service, session, start); err != nil {
		t.Fatal(err)
	}
	if err := store.Accept(t.Context(), 7, start.SessionID, card); err != nil {
		t.Fatal(err)
	}
	var active, first atomic.Bool
	started, release, finished := make(chan struct{}), make(chan struct{}), make(chan struct{})
	defer func() {
		select {
		case <-release:
		default:
			close(release)
		}
	}()
	open := service.OpenDelegated
	service.OpenDelegated = func(ctx context.Context, file *models.MediaFile, binding NativeVideoBinding) (NativeVideoFile, error) {
		opened, err := open(ctx, file, binding)
		if err != nil {
			return nil, err
		}
		return &nativeDeliveryJoiningSource{NativeVideoFile: opened, active: &active, first: &first, started: started, release: release, finished: finished}, nil
	}
	grant, err := store.Resolve(t.Context(), transcodenode.NativeExecutionRequest{WorkerID: 7, TransportID: start.SessionID, InputPath: start.InputPath, Recipe: &card}, service)
	if err != nil {
		t.Fatal(err)
	}
	ctx, cancel := context.WithCancel(t.Context())
	defer cancel()
	owner, err := grant.OpenDelivery(ctx)
	if err != nil {
		t.Fatal(err)
	}
	active.Store(true)
	cancel()
	waitCtx, stop := context.WithTimeout(t.Context(), 3*time.Second)
	defer stop()
	select {
	case <-started:
	case <-waitCtx.Done():
		t.Fatal("source cancellation callback never started")
	}
	closed := make(chan error, 1)
	go func() { closed <- owner.Close() }()
	// Close must remain blocked while the observable cancellation callback
	// is retained. The timer bounds this negative ownership assertion.
	timer := time.NewTimer(100 * time.Millisecond)
	defer timer.Stop()
	select {
	case err := <-closed:
		t.Fatalf("owner released during active cancellation: %v", err)
	case <-timer.C:
	}
	close(release)
	select {
	case err := <-closed:
		if err != nil {
			t.Fatal(err)
		}
	case <-waitCtx.Done():
		t.Fatal("source close did not finish after cancellation joined")
	}
}

func TestNativeVideoWorkerDeliveryCloseIsIdempotent(t *testing.T) {
	client := nativeVideoGrantRedis(t)
	service, session, start, card := nativeVideoGrantFixture(t, client)
	store := NewNativeVideoWorkerGrantStore(client)
	if err := store.Prepare(t.Context(), 7, service, session, start); err != nil {
		t.Fatal(err)
	}
	if err := store.Accept(t.Context(), 7, start.SessionID, card); err != nil {
		t.Fatal(err)
	}
	var revoked atomic.Bool
	var reads, closes atomic.Int32
	wantErr := errors.New("source close failed")
	open := service.OpenDelegated
	service.OpenDelegated = func(ctx context.Context, file *models.MediaFile, binding NativeVideoBinding) (NativeVideoFile, error) {
		opened, err := open(ctx, file, binding)
		if err != nil {
			return nil, err
		}
		return &nativeDeliverySource{NativeVideoFile: opened, revoked: &revoked, reads: &reads, closes: &closes}, nil
	}
	grant, err := store.Resolve(t.Context(), transcodenode.NativeExecutionRequest{WorkerID: 7, TransportID: start.SessionID, InputPath: start.InputPath, Recipe: &card}, service)
	if err != nil {
		t.Fatal(err)
	}
	owner, err := grant.OpenDelivery(t.Context())
	if err != nil {
		t.Fatal(err)
	}
	before := closes.Load()
	owner.(*nativeVideoWorkerFile).NativeVideoFile = &nativeDeliveryErrorClose{NativeVideoFile: owner.(*nativeVideoWorkerFile).NativeVideoFile, failure: wantErr}
	// The initial Close prevents the still-live context callback. Later
	// concurrent calls must retain that result without waiting for it to run.
	if err := owner.Close(); !errors.Is(err, wantErr) {
		t.Fatalf("initial close lost source failure: %v", err)
	}
	const callers = 8
	results := make(chan error, callers)
	for range callers {
		go func() { results <- owner.Close() }()
	}
	waitCtx, cancel := context.WithTimeout(t.Context(), time.Second)
	defer cancel()
	for range callers {
		select {
		case err := <-results:
			if !errors.Is(err, wantErr) {
				t.Fatalf("repeated close lost source failure: %v", err)
			}
		case <-waitCtx.Done():
			// First Close proved the callback was prevented, so this can
			// safely release callers left by a regressed wrapper.
			close(owner.(*nativeVideoWorkerFile).joined)
			t.Fatal("repeated Close waited for a prevented callback")
		}
	}
	if closes.Load() != before+1 {
		t.Fatalf("Close released its source more than once: %d", closes.Load()-before)
	}
}

type nativeDeliveryErrorClose struct {
	NativeVideoFile
	failure error
}

func (f *nativeDeliveryErrorClose) Close() error {
	return errors.Join(f.NativeVideoFile.Close(), f.failure)
}
func (*nativeDeliveryRedisFailure) ProcessPipelineHook(next redis.ProcessPipelineHook) redis.ProcessPipelineHook {
	return next
}

type nativeDeliverySource struct {
	NativeVideoFile
	revoked *atomic.Bool
	reads   *atomic.Int32
	closes  *atomic.Int32
}

func (f *nativeDeliverySource) Reauthorize(ctx context.Context) error {
	if f.revoked.Load() {
		return errors.New("delivery source unavailable")
	}
	return f.NativeVideoFile.Reauthorize(ctx)
}
func (f *nativeDeliverySource) Read(p []byte) (int, error) {
	f.reads.Add(1)
	return f.NativeVideoFile.Read(p)
}
func (f *nativeDeliverySource) ReadAt(p []byte, offset int64) (int, error) {
	f.reads.Add(1)
	return f.NativeVideoFile.ReadAt(p, offset)
}
func (f *nativeDeliverySource) Close() error {
	f.closes.Add(1)
	return f.NativeVideoFile.Close()
}

// Production OpenDelivery keeps one source owner and checks the original
// durable grant plus source policy without byte reads, tool inputs or probes.
func TestNativeVideoWorkerDeliveryRetainsExactAuthority(t *testing.T) {
	for _, cause := range []string{"grant", "deny", "source", "redis"} {
		t.Run(cause, func(t *testing.T) {
			client := nativeVideoGrantRedis(t)
			service, session, start, card := nativeVideoGrantFixture(t, client)
			failedClient := redis.NewClient(client.Options())
			t.Cleanup(func() { _ = failedClient.Close() })
			failure := new(nativeDeliveryRedisFailure)
			failedClient.AddHook(failure)
			t.Cleanup(func() { failure.failed.Store(false) })
			store := NewNativeVideoWorkerGrantStore(failedClient)
			if err := store.Prepare(t.Context(), 7, service, session, start); err != nil {
				t.Fatal(err)
			}
			if err := store.Accept(t.Context(), 7, start.SessionID, card); err != nil {
				t.Fatal(err)
			}
			var sourceRevoked atomic.Bool
			var reads, closes atomic.Int32
			open := service.OpenDelegated
			service.OpenDelegated = func(ctx context.Context, file *models.MediaFile, binding NativeVideoBinding) (NativeVideoFile, error) {
				opened, err := open(ctx, file, binding)
				if err != nil {
					return nil, err
				}
				return &nativeDeliverySource{NativeVideoFile: opened, revoked: &sourceRevoked, reads: &reads, closes: &closes}, nil
			}
			grant, err := store.Resolve(t.Context(), transcodenode.NativeExecutionRequest{WorkerID: 7, TransportID: start.SessionID, InputPath: start.InputPath, Recipe: &card}, service)
			if err != nil || grant.OpenDelivery == nil {
				t.Fatalf("accepted grant has no delivery owner: %v", err)
			}
			owner, err := grant.OpenDelivery(t.Context())
			if err != nil {
				t.Fatal(err)
			}
			closedBefore := closes.Load()
			defer func() {
				owner.Cancel()
				if err := owner.Close(); err != nil {
					t.Error(err)
				}
				if closes.Load() != closedBefore+1 || reads.Load() != 0 {
					t.Errorf("delivery source not joined without reads: closes=%d reads=%d", closes.Load()-closedBefore, reads.Load())
				}
			}()
			if err := owner.Reauthorize(t.Context()); err != nil {
				t.Fatal(err)
			}
			switch cause {
			case "grant":
				if err := store.Revoke(t.Context(), 7, start.SessionID); err != nil {
					t.Fatal(err)
				}
			case "deny":
				if err := client.Set(t.Context(), playback.StreamDenyKey(session.ID), "1", playback.MaxTokenTTL).Err(); err != nil {
					t.Fatal(err)
				}
			case "source":
				sourceRevoked.Store(true)
			case "redis":
				failure.failed.Store(true)
			}
			if err := owner.Reauthorize(t.Context()); err == nil {
				t.Fatal("retained delivery source ignored current grant/source denial")
			}
		})
	}
}
