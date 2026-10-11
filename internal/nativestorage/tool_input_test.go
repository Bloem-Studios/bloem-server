package nativestorage

import (
	"context"
	"errors"
	"io"
	"net/http"
	"os"
	"os/exec"
	"strings"
	"sync/atomic"
	"testing"
	"time"

	"github.com/Silo-Server/silo-server/internal/mediasource"
	"github.com/Silo-Server/silo-server/internal/storageplugin"
)

type toolCloseObserver struct {
	mediasource.File
	closed chan struct{}
}

func (f *toolCloseObserver) Close() error {
	err := f.File.Close()
	close(f.closed)
	return err
}

func toolWait(t *testing.T, signal <-chan struct{}) {
	t.Helper()
	select {
	case <-signal:
	case <-t.Context().Done():
		t.Fatal("lifetime signal did not arrive")
	}
}

// Closing the file or releasing host admission before joined reads and the
// trusted tool would let runtime shutdown race a still-running consumer.
func TestToolInputCancellationJoinsReadsAndToolBeforeHostRelease(t *testing.T) {
	ctx, cancel := context.WithCancel(t.Context())
	readCtx, cancelSource := context.WithCancel(ctx)
	entered, readJoined := make(chan struct{}), make(chan struct{})
	source := &toolFixtureSource{data: "0123456789abcdef"}
	source.read = func(ctx context.Context, _, _ int64, _ io.Writer) error {
		close(entered)
		<-ctx.Done()
		close(readJoined)
		return ctx.Err()
	}
	file, err := mediasource.Open(readCtx, source, mediasource.Ref{SourceID: "source", EntryID: " opaque/../entry ", Revision: "immutable-fixture"})
	if err != nil {
		t.Fatal(err)
	}
	observed := &toolCloseObserver{File: file, closed: make(chan struct{})}
	host := &Host{Manager: storageplugin.NewManager(storageplugin.Config{})}
	releaseHost, err := host.AcquireOpen()
	if err != nil {
		t.Fatal(err)
	}
	released := make(chan struct{})
	input, err := NewToolInput(ctx, observed, ToolInputConfig{
		CancelSource: cancelSource, Reauthorize: func(context.Context) error { return nil },
		ReleaseHost: func() { releaseHost(); close(released) },
		MaxBytes:    64, ChunkBytes: 4, MaxConcurrentReads: 1, Timeout: 5 * time.Second,
	})
	if err != nil {
		t.Fatal(err)
	}
	client := toolHTTP(t)
	toolJoin := make(chan struct{})
	result := make(chan error, 1)
	go func() {
		result <- input.Run(func(ctx context.Context, argument string) error {
			httpJoined := make(chan struct{})
			go func() {
				_, _, _ = toolResponse(t, client, "GET", argument, "bytes=0-3")
				close(httpJoined)
			}()
			<-ctx.Done()
			<-httpJoined
			<-toolJoin // Represents termination/Wait that has not completed yet.
			return nil
		})
	}()
	toolWait(t, entered)
	cancel()
	toolWait(t, readJoined)
	select {
	case <-observed.closed:
		t.Fatal("file closed before trusted tool joined")
	default:
	}
	select {
	case <-released:
		t.Fatal("host released before trusted tool joined")
	default:
	}
	drainCtx, stopDrain := context.WithCancel(t.Context())
	stopDrain()
	if err := host.Shutdown(drainCtx); !errors.Is(err, context.Canceled) {
		t.Fatalf("host did not retain admitted consumer: %v", err)
	}
	close(toolJoin)
	select {
	case err := <-result:
		if !errors.Is(err, context.Canceled) {
			t.Fatalf("parent cancellation did not invalidate result: %v", err)
		}
	case <-t.Context().Done():
		t.Fatal("tool input did not join cleanup")
	}
	toolWait(t, observed.closed)
	toolWait(t, released)
	if err := input.Close(); !errors.Is(err, context.Canceled) {
		t.Fatalf("repeat close lost sticky cancellation: %v", err)
	}
	if err := host.Shutdown(t.Context()); err != nil {
		t.Fatalf("host did not drain after input join: %v", err)
	}
}

func TestToolInputConcurrentReadLimitInvalidatesResult(t *testing.T) {
	entered := make(chan struct{})
	source := &toolFixtureSource{data: "0123456789abcdef"}
	source.read = func(ctx context.Context, _, _ int64, _ io.Writer) error {
		close(entered)
		<-ctx.Done()
		return ctx.Err()
	}
	input := toolFixtureInput(t, source, func(config *ToolInputConfig) { config.MaxConcurrentReads = 1 })
	client := toolHTTP(t)
	err := input.Run(func(_ context.Context, argument string) error {
		joined := make(chan struct{})
		go func() {
			_, _, _ = toolResponse(t, client, "GET", argument, "bytes=0-3")
			close(joined)
		}()
		toolWait(t, entered)
		_, body, _ := toolResponse(t, client, "GET", argument, "bytes=4-7")
		if body == "4567" {
			t.Fatal("concurrent read limit admitted another range")
		}
		<-joined
		return nil
	})
	if !errors.Is(err, ErrToolInputBusy) || source.reads.Load() != 1 {
		t.Fatalf("concurrent IO limit lost: %v", err)
	}
}

func TestToolInputDeadlineInvalidatesSuccessfulToolExit(t *testing.T) {
	source := &toolFixtureSource{data: "0123456789abcdef"}
	input := toolFixtureInput(t, source, func(config *ToolInputConfig) { config.Timeout = 50 * time.Millisecond })
	err := input.Run(func(ctx context.Context, _ string) error { <-ctx.Done(); return nil })
	if !errors.Is(err, context.DeadlineExceeded) {
		t.Fatalf("deadline hidden by exit zero: %v", err)
	}
}

func TestToolInputCleanupCancelsAndJoinsUnreturnedRange(t *testing.T) {
	entered, readCanReturn := make(chan struct{}), make(chan struct{})
	source := &toolFixtureSource{data: "0123456789abcdef"}
	source.read = func(ctx context.Context, _, _ int64, _ io.Writer) error {
		close(entered)
		<-ctx.Done()
		<-readCanReturn
		return ctx.Err()
	}
	var released atomic.Bool
	input := toolFixtureInput(t, source, func(config *ToolInputConfig) { config.ReleaseHost = func() { released.Store(true) } })
	client := toolHTTP(t)
	toolExited := make(chan struct{})
	result := make(chan error, 1)
	go func() {
		result <- input.Run(func(_ context.Context, argument string) error {
			go func() { _, _, _ = toolResponse(t, client, "GET", argument, "bytes=0-3") }()
			<-entered
			close(toolExited)
			return nil
		})
	}()
	toolWait(t, toolExited)
	// The callback has completed, but an active pinned read still owns admission.
	if released.Load() {
		t.Fatal("host was released before pinned range returned")
	}
	close(readCanReturn)
	select {
	case err := <-result:
		// The callback succeeded before cleanup canceled its unreturned range;
		// that teardown cancellation is joined but does not fail the result.
		if err != nil || !released.Load() {
			t.Fatalf("active canceled read was not joined, or its teardown failed the result: %v", err)
		}
	case <-t.Context().Done():
		t.Fatal("active read cleanup did not finish")
	}
}

// toolAbandonRange reads the first chunk of an open-ended range, waits until
// the bridge is serving the next chunk from a slow source, then abandons the
// response without reading it, as ffprobe does when it exits after reading
// what it needs.
func toolAbandonRange(t *testing.T, client *http.Client, argument string, pending <-chan struct{}) error {
	t.Helper()
	request, err := http.NewRequest("GET", argument, nil)
	if err != nil {
		return err
	}
	request.Header.Set("Range", "bytes=0-")
	response, err := client.Do(request)
	if err != nil {
		return err
	}
	first := make([]byte, 4)
	if _, err := io.ReadFull(response.Body, first); err != nil || string(first) != "0123" {
		_ = response.Body.Close()
		return errors.Join(err, errors.New("first chunk was not served"))
	}
	toolWait(t, pending)
	return response.Body.Close()
}

// A slow remote range can still be in flight when the tool exits successfully.
// Close cancels that abandoned read; its failure is a consequence of teardown,
// delivered no byte to the tool, and must not invalidate the tool's result.
func TestToolInputTeardownOfAbandonedRangeKeepsSuccessfulResult(t *testing.T) {
	pending := make(chan struct{})
	var canceled atomic.Bool
	source := &toolFixtureSource{data: "0123456789abcdef"}
	source.read = func(ctx context.Context, offset, length int64, w io.Writer) error {
		if offset == 0 {
			_, err := io.WriteString(w, source.data[offset:offset+length])
			return err
		}
		close(pending)
		<-ctx.Done()
		canceled.Store(true)
		// A canceled provider stream surfaces as cancellation and a short read.
		return errors.Join(ctx.Err(), io.ErrUnexpectedEOF)
	}
	released := make(chan struct{})
	input := toolFixtureInput(t, source, func(config *ToolInputConfig) { config.ReleaseHost = func() { close(released) } })
	client := toolHTTP(t)
	err := input.Run(func(_ context.Context, argument string) error {
		return toolAbandonRange(t, client, argument, pending)
	})
	if err != nil || input.Err() != nil {
		t.Fatalf("teardown of an abandoned range failed a successful tool: %v, %v", err, input.Err())
	}
	if !canceled.Load() {
		t.Fatal("the abandoned range was not in flight when cleanup canceled the source")
	}
	toolWait(t, released)
}

// Only teardown is excused. A provider failure on an abandoned range that is
// observed before Close begins remains sticky and invalidates exit zero.
func TestToolInputAbandonedRangeFailureBeforeTeardownStillFails(t *testing.T) {
	failure := errors.New("private provider failure")
	pending, fail := make(chan struct{}), make(chan struct{})
	source := &toolFixtureSource{data: "0123456789abcdef"}
	source.read = func(ctx context.Context, offset, length int64, w io.Writer) error {
		if offset == 0 {
			_, err := io.WriteString(w, source.data[offset:offset+length])
			return err
		}
		close(pending)
		select {
		case <-fail:
			return failure
		case <-ctx.Done():
			return ctx.Err()
		}
	}
	input := toolFixtureInput(t, source, nil)
	client := toolHTTP(t)
	err := input.Run(func(_ context.Context, argument string) error {
		if err := toolAbandonRange(t, client, argument, pending); err != nil {
			return err
		}
		close(fail)
		for input.Err() == nil {
			select {
			case <-t.Context().Done():
				t.Fatal("abandoned range failure was never recorded")
			case <-time.After(time.Millisecond):
			}
		}
		return nil
	})
	if !errors.Is(err, failure) || strings.Contains(err.Error(), "private provider") {
		t.Fatalf("provider failure on an abandoned range was lost or exposed: %v", err)
	}
}

func TestToolInputRejectsMissingLifetimeAuthorityAndBounds(t *testing.T) {
	for _, name := range []string{"authority", "cancellation", "bytes", "chunk", "concurrency", "deadline"} {
		t.Run(name, func(t *testing.T) {
			ctx, cancel := context.WithCancel(t.Context())
			defer cancel()
			source := &toolFixtureSource{data: "0123"}
			file, err := mediasource.Open(ctx, source, mediasource.Ref{SourceID: "source", EntryID: " opaque/../entry ", Revision: "immutable-fixture"})
			if err != nil {
				t.Fatal(err)
			}
			defer file.Close()
			config := ToolInputConfig{CancelSource: cancel, Reauthorize: func(context.Context) error { return nil }, MaxBytes: 64, ChunkBytes: 4, MaxConcurrentReads: 1, Timeout: time.Second}
			switch name {
			case "authority":
				config.Reauthorize = nil
			case "cancellation":
				config.CancelSource = nil
			case "bytes":
				config.MaxBytes = 0
			case "chunk":
				config.ChunkBytes = 0
			case "concurrency":
				config.MaxConcurrentReads = 0
			case "deadline":
				config.Timeout = 0
			}
			input, err := NewToolInput(ctx, file, config)
			if err == nil || input != nil {
				t.Fatal("unbounded or authority-free input admitted")
			}
		})
	}
}

func TestToolInputClosedSourceCannotProduceAcceptedOutput(t *testing.T) {
	source := &toolFixtureSource{data: "0123456789abcdef"}
	input := toolFixtureInput(t, source, nil)
	if err := input.file.Close(); err != nil {
		t.Fatal(err)
	}
	client := toolHTTP(t)
	err := input.Run(func(_ context.Context, argument string) error {
		_, body, _ := toolResponse(t, client, "GET", argument, "bytes=0-3")
		if strings.Contains(body, "0123") {
			t.Fatal("closed source produced bytes")
		}
		return nil
	})
	if !errors.Is(err, os.ErrClosed) {
		t.Fatalf("closed source failure lost: %v", err)
	}
}

func TestToolInputIdleCancellationClosesFileAndAdmission(t *testing.T) {
	ctx, cancel := context.WithCancel(t.Context())
	source := &toolFixtureSource{data: "0123"}
	file, err := mediasource.Open(ctx, source, mediasource.Ref{SourceID: "source", EntryID: " opaque/../entry ", Revision: "immutable-fixture"})
	if err != nil {
		t.Fatal(err)
	}
	observed := &toolCloseObserver{File: file, closed: make(chan struct{})}
	released := make(chan struct{})
	input, err := NewToolInput(ctx, observed, ToolInputConfig{CancelSource: cancel, Reauthorize: func(context.Context) error { return nil }, ReleaseHost: func() { close(released) }, MaxBytes: 4, ChunkBytes: 4, MaxConcurrentReads: 1, Timeout: time.Second})
	if err != nil {
		t.Fatal(err)
	}
	cancel()
	toolWait(t, released)
	toolWait(t, observed.closed)
	if !errors.Is(input.Err(), context.Canceled) {
		t.Fatalf("idle lifetime cancellation lost: %v", input.Err())
	}
}

func TestToolInputRealChildExitZeroCannotHideReadFailure(t *testing.T) {
	failure := errors.New("private terminal failure")
	source := &toolFixtureSource{data: "0123456789abcdef"}
	source.read = func(_ context.Context, _, _ int64, w io.Writer) error {
		_, _ = io.WriteString(w, "0123")
		return failure
	}
	input := toolFixtureInput(t, source, nil)
	executable, err := os.Executable()
	if err != nil {
		t.Fatal(err)
	}
	var childJoined atomic.Bool
	err = input.Run(func(_ context.Context, argument string) error {
		// This deliberately broken trusted tool ignores the failed range and
		// exits zero. Wait must still complete before result validation.
		command := exec.Command(executable, "-test.run=^TestToolInputHelperProcess$", "--", argument)
		command.Env = append(os.Environ(), "BLOEM_TOOL_INPUT_HELPER=1")
		err := command.Run()
		childJoined.Store(true)
		return err
	})
	if !childJoined.Load() || !errors.Is(err, failure) {
		t.Fatalf("actual child success hid transport failure or child was not joined: %v", err)
	}
}

func TestToolInputHelperProcess(t *testing.T) {
	if os.Getenv("BLOEM_TOOL_INPUT_HELPER") != "1" {
		return
	}
	client := &http.Client{Transport: &http.Transport{Proxy: nil}, Timeout: 5 * time.Second}
	r, err := http.NewRequest("GET", os.Args[len(os.Args)-1], nil)
	if err == nil {
		r.Header.Set("Range", "bytes=0-3")
		if response, err := client.Do(r); err == nil {
			_, _ = io.Copy(io.Discard, response.Body)
			_ = response.Body.Close()
		}
	}
	os.Exit(0)
}
