package playback

import (
	"context"
	"encoding/binary"
	"errors"
	"io"
	"os"
	"os/exec"
	"path/filepath"
	"sync"
	"time"
)

var ErrBoundProgressiveMissingV3 = errors.New("retained progressive runtime unavailable")

// BoundProgressiveRegistryV3 owns admitted processes, including starts whose
// ready response was lost. It never restores a process from a GET or recipe.
type BoundProgressiveRegistryV3 struct {
	mu       sync.Mutex
	lifetime context.Context
	acquire  ExecutorGrantProviderV3
	entries  map[ExecutorNamespaceV3]*boundProgressiveRuntimeV3
	closed   bool
}

type boundProgressiveRuntimeV3 struct {
	prepared          *PreparedBoundProgressiveV3
	ctx               context.Context
	cancel            context.CancelFunc
	ready, done       chan struct{}
	mu                sync.Mutex
	changed           chan struct{}
	size              int64
	readyOK, finished bool
	err               error
	cleanupErr        error
}

func NewBoundProgressiveRegistryV3(lifetime context.Context, acquire ExecutorGrantProviderV3) *BoundProgressiveRegistryV3 {
	return &BoundProgressiveRegistryV3{lifetime: lifetime, acquire: acquire, entries: make(map[ExecutorNamespaceV3]*boundProgressiveRuntimeV3)}
}

// Start consumes the namespace once. Cancellation of ctx ends the readiness
// exchange only; the exact retained producer remains owned by the runtime.
func (r *BoundProgressiveRegistryV3) Start(ctx context.Context, p *PreparedBoundProgressiveV3) error {
	if p == nil || p.card.Executor == nil || len(p.args) == 0 || r.acquire == nil || r.lifetime == nil {
		return errors.New("progressive runtime is not configured")
	}
	if err := ctx.Err(); err != nil {
		return err
	}
	r.mu.Lock()
	ns := *p.card.Executor
	if r.closed || r.lifetime.Err() != nil {
		r.mu.Unlock()
		return ErrBoundProgressiveMissingV3
	}
	if _, exists := r.entries[ns]; exists {
		r.mu.Unlock()
		return ErrExecutorReplacementRequired
	}
	lifetime, cancel := context.WithCancel(r.lifetime)
	e := &boundProgressiveRuntimeV3{prepared: p, ctx: lifetime, cancel: cancel, ready: make(chan struct{}), done: make(chan struct{}), changed: make(chan struct{})}
	r.entries[ns] = e
	r.mu.Unlock()
	go e.run(r.acquire)
	select {
	case <-ctx.Done():
		return ctx.Err()
	case <-e.done:
		e.mu.Lock()
		defer e.mu.Unlock()
		return e.err
	case <-e.ready:
		if err := e.ctx.Err(); err != nil {
			return err
		}
		return nil
	}
}

func (e *boundProgressiveRuntimeV3) signal() { close(e.changed); e.changed = make(chan struct{}) }

func (e *boundProgressiveRuntimeV3) run(acquire ExecutorGrantProviderV3) {
	var runErr error
	defer func() {
		e.cancel()
		e.mu.Lock()
		e.err = runErr
		if e.err == nil {
			e.err = ErrBoundProgressiveMissingV3
		}
		e.signal()
		e.mu.Unlock()
		close(e.done)
	}()
	p := e.prepared
	grant, err := acquire(e.ctx, p.card.TranscodeTransportID, *p.card.Executor, AttemptGrantExecuteV3)
	if grant != nil {
		defer func() { grant.Close(); <-grant.Done() }()
	}
	if err != nil {
		runErr = err
		return
	}
	if grant == nil {
		runErr = errors.New("progressive execution grant missing")
		return
	}
	if err = grant.CheckBinding(*p.card.Executor, AttemptGrantExecuteV3, p.card.TranscodeTransportID); err != nil {
		runErr = err
		return
	}
	stopGrant := context.AfterFunc(grant.Context(), e.cancel)
	defer stopGrant()
	if err = claimExecutorOutput(p.output, *p.card.Executor); err != nil {
		runErr = err
		return
	}
	defer func() {
		cleanupErr := removeExecutorOutput(p.output, *p.card.Executor)
		e.mu.Lock()
		e.cleanupErr = cleanupErr
		e.mu.Unlock()
		runErr = errors.Join(runErr, cleanupErr)
	}()
	file, err := os.OpenFile(filepath.Join(p.output, "output.mp4"), os.O_CREATE|os.O_EXCL|os.O_RDWR, 0o600)
	if err != nil {
		runErr = err
		return
	}
	defer file.Close()
	if err = grant.Check(); err != nil {
		runErr = err
		return
	}
	cmd := exec.CommandContext(e.ctx, p.binary, p.args...)
	cmd.WaitDelay = 2 * time.Second
	pipe, err := cmd.StdoutPipe()
	if err != nil {
		runErr = err
		return
	}
	defer pipe.Close()
	if err = cmd.Start(); err != nil {
		runErr = errors.New("progressive process start failed")
		return
	}
	stopPipe := context.AfterFunc(e.ctx, func() { _ = pipe.Close() })
	defer stopPipe()
	buffer := make([]byte, 32<<10)
	var scanOffset int64
	var moov, moof bool
	for {
		n, readErr := pipe.Read(buffer)
		if n > 0 {
			if _, err = file.Write(buffer[:n]); err != nil {
				runErr = err
				break
			}
			e.mu.Lock()
			e.size += int64(n)
			if !e.readyOK {
				ready, parseErr := progressiveReadyV3(file, e.size, &scanOffset, &moov, &moof)
				if parseErr != nil {
					e.mu.Unlock()
					runErr = parseErr
					break
				}
				if ready {
					e.readyOK = true
					close(e.ready)
				}
			}
			e.signal()
			e.mu.Unlock()
		}
		if readErr != nil {
			if !errors.Is(readErr, io.EOF) {
				runErr = errors.New("progressive output interrupted")
			}
			break
		}
	}
	if runErr != nil {
		e.cancel()
	}
	if err = cmd.Wait(); err != nil {
		runErr = errors.New("progressive process failed")
		return
	}
	if runErr != nil {
		return
	}
	e.mu.Lock()
	e.finished = true
	ready := e.readyOK
	e.signal()
	e.mu.Unlock()
	if !ready {
		runErr = errors.New("progressive output did not become ready")
		return
	}
	// A finished producer's actual bytes remain available under its live grant.
	<-e.ctx.Done()
}

// Require complete initialization and at least one complete media fragment,
// rather than treating process launch or an empty output file as readiness.
func progressiveReadyV3(file *os.File, size int64, offset *int64, moov, moof *bool) (bool, error) {
	for size-*offset >= 8 {
		var header [16]byte
		if _, err := file.ReadAt(header[:8], *offset); err != nil {
			return false, err
		}
		length := int64(binary.BigEndian.Uint32(header[:4]))
		headerSize := int64(8)
		if length == 1 {
			if size-*offset < 16 {
				return false, nil
			}
			if _, err := file.ReadAt(header[8:], *offset+8); err != nil {
				return false, err
			}
			value := binary.BigEndian.Uint64(header[8:])
			if value > uint64(^uint64(0)>>1) {
				return false, errors.New("invalid progressive box size")
			}
			length, headerSize = int64(value), 16
		}
		if length < headerSize {
			return false, errors.New("invalid progressive box")
		}
		if length > size-*offset {
			return false, nil
		}
		switch string(header[4:8]) {
		case "moov":
			*moov = true
		case "moof":
			*moof = *moov
		case "mdat":
			if *moov && *moof && length > headerSize {
				return true, nil
			}
		}
		*offset += length
	}
	return false, nil
}

func (r *BoundProgressiveRegistryV3) lookup(transport string, ns ExecutorNamespaceV3) (*boundProgressiveRuntimeV3, error) {
	r.mu.Lock()
	defer r.mu.Unlock()
	e := r.entries[ns]
	if e == nil || e.prepared.card.TranscodeTransportID != transport {
		return nil, ErrBoundProgressiveMissingV3
	}
	return e, nil
}

func (r *BoundProgressiveRegistryV3) Stop(ctx context.Context, transport string, ns ExecutorNamespaceV3) error {
	e, err := r.lookup(transport, ns)
	if err != nil {
		return err
	}
	e.cancel()
	select {
	case <-ctx.Done():
		return ctx.Err()
	case <-e.done:
		e.mu.Lock()
		defer e.mu.Unlock()
		return e.cleanupErr
	}
}

func (r *BoundProgressiveRegistryV3) Close(ctx context.Context) error {
	r.mu.Lock()
	r.closed = true
	entries := make([]*boundProgressiveRuntimeV3, 0, len(r.entries))
	for _, e := range r.entries {
		e.cancel()
		entries = append(entries, e)
	}
	r.mu.Unlock()
	var cleanupErr error
	for _, e := range entries {
		select {
		case <-ctx.Done():
			return ctx.Err()
		case <-e.done:
			e.mu.Lock()
			cleanupErr = errors.Join(cleanupErr, e.cleanupErr)
			e.mu.Unlock()
		}
	}
	return cleanupErr
}

// RetainedRecipe returns a defensive copy for an exact control/serving adapter;
// it does not load storage or reconstruct a missing runtime.
func (r *BoundProgressiveRegistryV3) RetainedRecipe(transport string, ns ExecutorNamespaceV3) (RecipeCard, error) {
	e, err := r.lookup(transport, ns)
	if err != nil {
		return RecipeCard{}, err
	}
	return e.prepared.Recipe(), nil
}
