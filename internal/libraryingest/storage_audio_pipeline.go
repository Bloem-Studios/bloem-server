package libraryingest

import (
	"context"
	"errors"
	"sync"

	"github.com/Silo-Server/silo-server/internal/scanner"
	storagev1 "github.com/Silo-Server/silo-server/internal/storageproto/bloem/plugin/v1"
	"github.com/Silo-Server/silo-server/internal/storagesource"
)

// nativeAudioScanWorkers is how many audiobook directories one scan lists and
// probes at once. A book's time is spent waiting on its provider and ffprobe,
// so workers overlap that waiting; the scan still applies one page at a time.
const nativeAudioScanWorkers = 8

// preparedAudioPage is a directory page a worker listed and probed, ready for
// the scan to apply.
type preparedAudioPage struct {
	checkpoint storagesource.Checkpoint
	page       *storagev1.ListResponse
	tracks     []scanner.StorageAudioTrack
	skipped    int
	err        error
}

// audioPipeline lists and probes an audiobook scan's pending directories with
// several workers. Each directory is claimed until the scan applies its page
// and calls release, so a directory's pages are fetched one after another and
// never by two workers. Applying pages stays sequential.
type audioPipeline struct {
	cancel context.CancelFunc
	// stopped and cause are the workers' context's Done and Err.
	stopped <-chan struct{}
	cause   func() error
	ready   chan preparedAudioPage
	done    chan struct{}
	wg      sync.WaitGroup

	mu       sync.Mutex
	changed  *sync.Cond
	inFlight map[string]bool
	// after is the furthest directory claimed; the next claim looks after it.
	after string
	// version counts releases. A worker that found nothing pending decides
	// the run is finished only if no release happened since it looked, since
	// a released page may have queued new directories.
	version  uint64
	finished bool
}

type audioPageSource interface {
	NextDirectoryAfter(ctx context.Context, lease storagesource.Lease, after string, inFlight []string) (storagesource.Checkpoint, bool, error)
}

func startAudioPipeline(ctx context.Context, workers int, sources audioPageSource, lease storagesource.Lease, fetch func(context.Context, storagesource.Checkpoint) (*storagev1.ListResponse, error), prepare func(context.Context, storagesource.Checkpoint, *storagev1.ListResponse) ([]scanner.StorageAudioTrack, int, error)) *audioPipeline {
	ctx, cancel := context.WithCancel(ctx)
	p := &audioPipeline{cancel: cancel, stopped: ctx.Done(), cause: ctx.Err, ready: make(chan preparedAudioPage, workers), done: make(chan struct{}), inFlight: map[string]bool{}}
	p.changed = sync.NewCond(&p.mu)
	stop := context.AfterFunc(ctx, func() {
		p.mu.Lock()
		p.changed.Broadcast()
		p.mu.Unlock()
	})
	for range workers {
		p.wg.Add(1)
		go func() {
			defer p.wg.Done()
			p.work(ctx, sources, lease, fetch, prepare)
		}()
	}
	go func() {
		p.wg.Wait()
		stop()
		close(p.done)
	}()
	return p
}

func (p *audioPipeline) work(ctx context.Context, sources audioPageSource, lease storagesource.Lease, fetch func(context.Context, storagesource.Checkpoint) (*storagev1.ListResponse, error), prepare func(context.Context, storagesource.Checkpoint, *storagev1.ListResponse) ([]scanner.StorageAudioTrack, int, error)) {
	for ctx.Err() == nil {
		p.mu.Lock()
		if p.finished {
			p.mu.Unlock()
			return
		}
		claimed := make([]string, 0, len(p.inFlight))
		for id := range p.inFlight {
			claimed = append(claimed, id)
		}
		version, after := p.version, p.after
		p.mu.Unlock()

		checkpoint, more, err := sources.NextDirectoryAfter(ctx, lease, after, claimed)
		if err != nil {
			p.fail(err)
			return
		}
		p.mu.Lock()
		if !more {
			if len(p.inFlight) == 0 && p.version == version {
				p.finished = true
				p.changed.Broadcast()
				p.mu.Unlock()
				return
			}
			// Wait for a release: it may queue directories or end the run.
			for p.version == version && !p.finished && ctx.Err() == nil {
				p.changed.Wait()
			}
			p.mu.Unlock()
			continue
		}
		if p.version != version || p.inFlight[checkpoint.DirectoryID] {
			// Another worker claimed it after this one looked, or a page was
			// applied since, so the checkpoint may be stale.
			p.mu.Unlock()
			continue
		}
		p.inFlight[checkpoint.DirectoryID] = true
		p.after = max(p.after, checkpoint.DirectoryID)
		p.mu.Unlock()

		page, err := fetch(ctx, checkpoint)
		if errors.Is(err, storagesource.ErrCheckpointRestart) {
			p.release(checkpoint.DirectoryID)
			continue
		}
		var tracks []scanner.StorageAudioTrack
		var skipped int
		if err == nil {
			tracks, skipped, err = prepare(ctx, checkpoint, page)
		}
		select {
		case p.ready <- preparedAudioPage{checkpoint: checkpoint, page: page, tracks: tracks, skipped: skipped, err: err}:
		case <-ctx.Done():
			return
		}
		if err != nil {
			return
		}
	}
}

func (p *audioPipeline) fail(err error) {
	select {
	case p.ready <- preparedAudioPage{err: err}:
	case <-p.stopped:
	}
}

// next returns the next prepared page, or false once every directory of the
// run has been applied.
func (p *audioPipeline) next() (preparedAudioPage, bool, error) {
	select {
	case item := <-p.ready:
		if item.err != nil {
			return item, false, item.err
		}
		return item, true, nil
	case <-p.done:
		// A worker may have queued its last page as the others finished.
		select {
		case item := <-p.ready:
			if item.err != nil {
				return item, false, item.err
			}
			return item, true, nil
		default:
		}
		if err := p.cause(); err != nil {
			return preparedAudioPage{}, false, err
		}
		return preparedAudioPage{}, false, nil
	}
}

// release marks a directory's page applied; its next page, if any, can be
// claimed again.
func (p *audioPipeline) release(directoryID string) {
	p.mu.Lock()
	delete(p.inFlight, directoryID)
	p.version++
	p.changed.Broadcast()
	p.mu.Unlock()
}

// stop ends the workers and waits for them.
func (p *audioPipeline) stop() {
	p.cancel()
	for {
		select {
		case <-p.ready:
		case <-p.done:
			return
		}
	}
}
