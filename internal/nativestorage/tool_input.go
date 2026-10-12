package nativestorage

import (
	"context"
	"errors"
	"io"
	"log"
	"net"
	"net/http"
	"os"
	"sync"
	"time"

	"github.com/Silo-Server/silo-server/internal/mediasource"
)

var (
	ErrToolInputBudget  = errors.New("native tool input byte budget exhausted")
	ErrToolInputClosed  = errors.New("native tool input is closed")
	ErrToolInputBusy    = errors.New("native tool input concurrent read limit exceeded")
	ErrToolInputInvalid = errors.New("invalid native tool input")
)

// ToolInputConfig is supplied by the trusted consumer, never an API request.
// CancelSource must cancel the lifetime used to open File. It must not call
// File.Close or release host admission: Close does that only after all reads
// and the tool have joined. An opened adapter may independently close its own
// resources on cancellation, as OpenLocal does. ReleaseHost may be nil when
// File.Close owns that admission.
// Numerical budgets are explicit so each tool integration chooses them from
// its own fixtures rather than inheriting a whole-object download allowance.
type ToolInputConfig struct {
	CancelSource       context.CancelFunc
	Reauthorize        func(context.Context) error
	ReleaseHost        func()
	MaxBytes           int64
	ChunkBytes         int
	MaxConcurrentReads int
	Timeout            time.Duration
}

// ToolInput owns one already-authorized file and a private, ephemeral tool
// argument for its immutable Info. It grants no new catalog/source authority.
// Never persist the argument or include it in child stderr, logs or responses.
type ToolInput struct {
	file                       mediasource.File
	info                       mediasource.Info
	config                     ToolInputConfig
	ctx                        context.Context
	cancel                     context.CancelFunc
	server                     *http.Server
	argument, host, capability string
	slots                      chan struct{}

	mu                sync.Mutex
	closing, used     bool
	joined            bool
	spent             int64
	sticky            error
	toolJoined        chan struct{}
	stopLifetime      func() bool
	handlers, serving sync.WaitGroup
	closeOnce         sync.Once
	done              chan struct{}
}

// NewToolInput transfers file/admission ownership only on success. The caller
// must already have admitted reads under the retained source policy and bind
// CancelSource to the same source/runtime lifetime as File. Reauthorize completes
// a fresh authority check before and after each read; it must retain no
// transaction across IO.
func NewToolInput(ctx context.Context, file mediasource.File, config ToolInputConfig) (*ToolInput, error) {
	if ctx == nil || file == nil || config.CancelSource == nil || config.Reauthorize == nil || config.MaxBytes <= 0 ||
		config.ChunkBytes <= 0 || config.ChunkBytes > 8<<20 || config.MaxConcurrentReads <= 0 || config.MaxConcurrentReads > 8 || config.Timeout <= 0 {
		return nil, ErrToolInputInvalid
	}
	if err := context.Cause(ctx); err != nil {
		return nil, &toolInputFailure{err}
	}
	info := file.Info()
	if info.Size < 0 {
		return nil, ErrToolInputInvalid
	}
	listener, err := net.Listen("tcp4", "127.0.0.1:0")
	if err != nil {
		return nil, &toolInputFailure{err}
	}
	capability, err := newToolCapability()
	if err != nil {
		_ = listener.Close()
		return nil, &toolInputFailure{err}
	}
	lifetime, cancel := context.WithTimeout(ctx, config.Timeout)
	input := &ToolInput{file: file, info: info, config: config, ctx: lifetime, cancel: cancel,
		host: listener.Addr().String(), capability: capability, slots: make(chan struct{}, config.MaxConcurrentReads), done: make(chan struct{})}
	input.argument = "http://" + input.host + "/" + capability
	input.server = &http.Server{Handler: http.HandlerFunc(input.serveToolRange), ReadHeaderTimeout: 2 * time.Second,
		IdleTimeout: 2 * time.Second, MaxHeaderBytes: 4 << 10, ErrorLog: log.New(io.Discard, "", 0)}
	input.serving.Add(1)
	go func() {
		defer input.serving.Done()
		if err := input.server.Serve(listener); err != nil && !errors.Is(err, http.ErrServerClosed) {
			input.fail(err)
		}
	}()
	input.mu.Lock()
	input.stopLifetime = context.AfterFunc(lifetime, func() { _ = input.Close() })
	input.mu.Unlock()
	return input, nil
}

// Run invokes one trusted tool consumer. The callback must use ctx for process
// termination and return only after its child has been reaped; it must not call
// Close itself. Run then joins HTTP reads and cleanup and checks sticky input
// errors even when the callback accepted partial output and returned nil.
func (t *ToolInput) Run(run func(context.Context, string) error) (result error) {
	if t == nil || run == nil {
		return ErrToolInputInvalid
	}
	t.mu.Lock()
	if t.closing || t.used {
		t.mu.Unlock()
		return ErrToolInputClosed
	}
	t.used = true
	t.toolJoined = make(chan struct{})
	t.mu.Unlock()
	defer func() {
		t.mu.Lock()
		if err := context.Cause(t.ctx); err != nil && t.sticky == nil {
			t.sticky = &toolInputFailure{err}
		}
		t.joined = true
		close(t.toolJoined)
		t.mu.Unlock()
		result = errors.Join(result, t.Close())
	}()
	if err := run(t.ctx, t.argument); err != nil {
		return &toolInputFailure{err}
	}
	return nil
}

// Info returns the opening snapshot, including the exact pinned revision. The
// ordinary local-file adapter also works and carries its existing empty revision.
func (t *ToolInput) Info() mediasource.Info { return t.info }

// String prevents accidental formatting of a handle from exposing its grant.
func (t *ToolInput) String() string { return "native tool input" }

// Err retains the first source/authority/budget failure without its private text.
// errors.Is still exposes the original typed cause for internal classification.
func (t *ToolInput) Err() error {
	t.mu.Lock()
	defer t.mu.Unlock()
	return t.sticky
}

type toolInputFailure struct{ cause error }

func (*toolInputFailure) Error() string   { return "native tool input failed" }
func (e *toolInputFailure) Unwrap() error { return e.cause }

func (t *ToolInput) fail(err error) {
	if err == nil {
		return
	}
	t.mu.Lock()
	if t.sticky == nil {
		t.sticky = &toolInputFailure{err}
	}
	t.mu.Unlock()
	t.cancel()
}

// failServing records a failure observed by an HTTP handler, except one seen
// after the tool has joined and Close has begun. Close cancels the lifetime
// and the source before it joins handlers, so a range the tool abandoned
// (ffprobe exits with an open-ended range still being served) fails as a
// consequence of that teardown. Such a failure cannot affect the tool's
// result: a chunk reaches HTTP only after its whole read and authority checks
// succeed, and a handler records any failure before it answers, so a failed
// read the tool waited on was recorded before the tool could join. Failures
// while the tool still runs are always recorded.
func (t *ToolInput) failServing(err error) {
	if err == nil {
		return
	}
	t.mu.Lock()
	if t.closing && t.joined {
		t.mu.Unlock()
		return
	}
	if t.sticky == nil {
		t.sticky = &toolInputFailure{err}
	}
	t.mu.Unlock()
	t.cancel()
}

// Close stops admission, cancels source/HTTP IO, joins the tool and active
// handlers, explicitly closes File, and releases host admission exactly once.
// A consumer that has not yet joined retains host admission; no cleanup timeout
// releases its runtime. File's own cancellation hooks retain their semantics.
// The bounded lifetime triggers this cleanup independently of a caller
// remembering to close an idle handle. Reads that fail because this cleanup
// canceled them are not recorded; see failServing.
func (t *ToolInput) Close() error {
	if t == nil {
		return nil
	}
	t.closeOnce.Do(func() {
		t.mu.Lock()
		t.closing = true
		// A source can cancel its lifetime before the HTTP handler records
		// its terminal error. Retain that cause when cleanup wins the race.
		if err := context.Cause(t.ctx); err != nil && t.sticky == nil {
			t.sticky = &toolInputFailure{err}
		}
		joined, stop := t.toolJoined, t.stopLifetime
		t.mu.Unlock()
		if stop != nil {
			stop()
		}
		t.cancel()
		t.config.CancelSource()
		_ = t.server.Close()
		if joined != nil {
			<-joined
		}
		t.serving.Wait()
		t.handlers.Wait()
		if err := t.file.Close(); err != nil && !errors.Is(err, os.ErrClosed) {
			t.fail(err)
		}
		if t.config.ReleaseHost != nil {
			t.config.ReleaseHost()
		}
		close(t.done)
	})
	<-t.done
	return t.Err()
}
