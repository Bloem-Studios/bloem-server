package livetv

import (
	"context"
	"errors"
	"io"
	"sync"
	"time"

	"github.com/google/uuid"
)

// xtreamLeaseBody owns one physical provider request, including DVR input.
// Its credential-bearing HTTP request never becomes an FFmpeg argument, a
// persisted stream URL, or a client-facing response.
type xtreamLeaseBody struct {
	io.ReadCloser
	cancel     context.CancelFunc
	ctx        context.Context
	done       <-chan struct{}
	deadlineMu sync.Mutex
	deadline   time.Time
	once       sync.Once
}

func (b *xtreamLeaseBody) Read(p []byte) (int, error) {
	if !b.valid() {
		return 0, errors.New("Xtream connection lease ended")
	}
	n, err := b.ReadCloser.Read(p)
	if !b.valid() {
		return 0, errors.New("Xtream connection lease ended")
	}
	if err != nil && !errors.Is(err, io.EOF) {
		return n, errors.New("Xtream stream ended unexpectedly")
	}
	return n, err
}

func (b *xtreamLeaseBody) valid() bool {
	b.deadlineMu.Lock()
	valid := time.Now().Before(b.deadline)
	b.deadlineMu.Unlock()
	if !valid {
		b.cancel()
	}
	return valid && b.ctx.Err() == nil
}

func (b *xtreamLeaseBody) Close() error {
	b.once.Do(func() {
		b.cancel()
		_ = b.ReadCloser.Close()
		<-b.done
	})
	return nil
}

func (s *Service) openXtreamChannel(ctx context.Context, channelID string) (io.ReadCloser, error) {
	store, err := s.xtreamStore()
	if err != nil {
		return nil, err
	}
	lookup, stopLookup := context.WithTimeout(ctx, 5*time.Second)
	defer stopLookup()
	channel, err := store.GetChannel(lookup, channelID)
	if err != nil {
		return nil, err
	}
	if channel == nil || !channel.Enabled {
		return nil, ErrNotFound
	}
	streamID, err := xtreamStreamID(channel)
	if err != nil {
		return nil, err
	}
	tuner, err := store.GetTuner(lookup, channel.TunerID)
	if err != nil {
		return nil, err
	}
	if tuner == nil || tuner.Type != TunerTypeXtream {
		return nil, ErrNotFound
	}
	client, err := s.xtreamClientForTuner(lookup, tuner)
	if err != nil {
		return nil, err
	}
	stopLookup()
	leaseID := uuid.NewString()
	started := time.Now()
	claim, cancelClaim := context.WithTimeout(ctx, 5*time.Second)
	err = store.claimXtreamLease(claim, tuner.ID, leaseID)
	cancelClaim()
	if err != nil {
		// Commit acknowledgement may have been lost. This unpredictable ID
		// belongs only to this attempt, so cleanup cannot free another owner.
		cleanup, stop := context.WithTimeout(context.WithoutCancel(ctx), 5*time.Second)
		_ = store.releaseXtreamLease(cleanup, tuner.ID, leaseID)
		stop()
		return nil, err
	}
	streamCtx, cancel := context.WithCancel(ctx)
	done := make(chan struct{})
	body := &xtreamLeaseBody{ctx: streamCtx, cancel: cancel, done: done, deadline: started.Add(xtreamLeaseTTL - 5*time.Second)}
	go func() {
		defer close(done)
		defer func() {
			cleanup, stop := context.WithTimeout(context.WithoutCancel(ctx), 5*time.Second)
			defer stop()
			_ = store.releaseXtreamLease(cleanup, tuner.ID, leaseID)
		}()
		ticker := time.NewTicker(15 * time.Second)
		defer ticker.Stop()
		for {
			select {
			case <-streamCtx.Done():
				return
			case <-ticker.C:
				if !body.valid() {
					cancel()
					return
				}
				started := time.Now()
				renew, stop := context.WithTimeout(streamCtx, 5*time.Second)
				err := store.renewXtreamLease(renew, tuner.ID, leaseID)
				stop()
				if err != nil {
					cancel()
					return
				}
				body.deadlineMu.Lock()
				body.deadline = started.Add(xtreamLeaseTTL - 5*time.Second)
				body.deadlineMu.Unlock()
			}
		}
	}()
	upstream, err := client.openLive(streamCtx, streamID)
	if err != nil {
		cancel()
		<-done
		return nil, err
	}
	body.ReadCloser = upstream
	return body, nil
}

// A nil opener means the existing HDHomeRun URL path. An Xtream opener carries
// only the channel identity; credentials and enabled state are reread at the
// actual open, under the same server-owned provider and origin binding.
func (s *Service) xtreamChannelOpener(ch *Channel) func(context.Context) (io.ReadCloser, error) {
	if ch == nil || len(ch.StreamURL) < 7 || ch.StreamURL[:7] != "xtream:" {
		return nil
	}
	id := ch.ID
	return func(ctx context.Context) (io.ReadCloser, error) { return s.openXtreamChannel(ctx, id) }
}

// OpenXtreamSessionSource handles only the opaque, server-owned source kind.
// Callers retain their existing session/profile checks and lease cancellation.
// A nil body and nil error mean the ordinary HDHomeRun proxy path applies.
func (s *Service) OpenXtreamSessionSource(ctx context.Context, sessionID string) (io.ReadCloser, error) {
	if err := s.requireStore(); err != nil {
		return nil, err
	}
	session, err := s.store.GetSession(ctx, sessionID)
	if err != nil {
		return nil, err
	}
	if session == nil || session.Status != "active" {
		return nil, ErrNotFound
	}
	channel, err := s.store.GetChannel(ctx, session.ChannelID)
	if err != nil {
		return nil, err
	}
	if channel == nil || !channel.Enabled {
		return nil, ErrNotFound
	}
	open := s.xtreamChannelOpener(channel)
	if open == nil {
		return nil, nil
	}
	return open(ctx)
}
