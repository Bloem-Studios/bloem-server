package scanner

import (
	"bytes"
	"context"
	"encoding/binary"
	"encoding/json"
	"errors"
	"fmt"
	"io"
	"net/http"
	"net/http/httptest"
	"os"
	"os/exec"
	"path/filepath"
	"reflect"
	"strings"
	"sync"
	"sync/atomic"
	"testing"
	"time"

	"github.com/Silo-Server/silo-server/internal/mediasource"
	"github.com/Silo-Server/silo-server/internal/nativestorage"
	"github.com/Silo-Server/silo-server/internal/storagesource"
)

type nativeProbeRange struct{ offset, length int64 }
type nativeProbeFixtureSource struct {
	data      []byte
	mu        sync.Mutex
	ranges    []nativeProbeRange
	fail      error
	afterRead func()
}

func (s *nativeProbeFixtureSource) Stat(_ context.Context, _ mediasource.Ref) (mediasource.Info, error) {
	return mediasource.Info{Name: "fixture.mp4", LogicalPath: "opaque/private/key", Revision: "pinned", Size: int64(len(s.data))}, nil
}
func (s *nativeProbeFixtureSource) ReadRange(ctx context.Context, _ mediasource.Ref, offset, length int64, dst io.Writer) error {
	if err := ctx.Err(); err != nil {
		return err
	}
	s.mu.Lock()
	s.ranges = append(s.ranges, nativeProbeRange{offset, length})
	fail, after := s.fail, s.afterRead
	s.mu.Unlock()
	_, err := dst.Write(s.data[offset : offset+length])
	if after != nil {
		after()
	}
	return errors.Join(err, fail)
}
func (s *nativeProbeFixtureSource) fetched() (int64, []nativeProbeRange) {
	s.mu.Lock()
	defer s.mu.Unlock()
	var total int64
	for _, r := range s.ranges {
		total += r.length
	}
	return total, append([]nativeProbeRange(nil), s.ranges...)
}

type nativeProbeOwnedFile struct {
	mediasource.File
	closed   atomic.Int32
	closeErr error
}

type nativeProbeJoinedEOFFile struct {
	mediasource.File
	terminal error
}

func (f nativeProbeJoinedEOFFile) ReadAt(p []byte, off int64) (int, error) {
	n, err := f.File.ReadAt(p, off)
	if errors.Is(err, io.EOF) {
		return n, errors.Join(err, f.terminal)
	}
	return n, err
}

func TestNativeProbeJoinedEOFIsTerminalAtKnownSize(t *testing.T) {
	_, file, ctx, cfg := nativeProbeFixture(t, []byte("short"))
	sentinel := storagesource.ErrReferenceConflict
	got, err := ProbeNative(ctx, nativeProbeTestTool(t, "success"), nativeProbeJoinedEOFFile{file, sentinel}, cfg)
	if got != nil || !errors.Is(err, sentinel) {
		t.Fatalf("joined EOF accepted: %+v, %v", got, err)
	}
}

func (f *nativeProbeOwnedFile) Close() error {
	f.closed.Add(1)
	return errors.Join(f.File.Close(), f.closeErr)
}

func nativeProbeFixture(t *testing.T, data []byte) (*nativeProbeFixtureSource, *nativeProbeOwnedFile, context.Context, NativeProbeConfig) {
	t.Helper()
	ctx, cancel := context.WithCancel(t.Context())
	source := &nativeProbeFixtureSource{data: data}
	file, err := mediasource.Open(ctx, source, mediasource.Ref{SourceID: "source", EntryID: "entry", Revision: "pinned"})
	if err != nil {
		t.Fatal(err)
	}
	owned := &nativeProbeOwnedFile{File: file}
	t.Cleanup(func() {
		cancel()
		if owned.closed.Load() == 0 {
			_ = owned.Close()
		}
	})
	cfg := NativeProbeConfig{Demuxer: NativeProbeMOV, Input: nativestorage.ToolInputConfig{CancelSource: cancel, Reauthorize: func(context.Context) error { return nil }, MaxBytes: 2 << 20, ChunkBytes: 4096, MaxConcurrentReads: 2, Timeout: 5 * time.Second}, MaxJSONBytes: 128 << 10, MaxStderrBytes: 4096}
	return source, owned, ctx, cfg
}

func nativeProbeBinary(t *testing.T) string {
	t.Helper()
	path, err := exec.LookPath("ffprobe")
	if err != nil {
		t.Skip("ffprobe required")
	}
	return path
}

func TestNativeProbeRealFixturesMatchLocal(t *testing.T) {
	for _, path := range []string{"testdata/test.mp4", "testdata/subtitles.mkv"} {
		t.Run(filepath.Base(path), func(t *testing.T) {
			binary := nativeProbeBinary(t)
			data, err := os.ReadFile(path)
			if err != nil {
				t.Fatal(err)
			}
			local, err := ProbeFile(t.Context(), binary, path)
			if err != nil {
				t.Fatal(err)
			}
			source, file, ctx, cfg := nativeProbeFixture(t, data)
			if filepath.Ext(path) == ".mkv" {
				cfg.Demuxer = NativeProbeMatroska
			}
			got, err := ProbeNative(ctx, binary, file, cfg)
			if err != nil {
				t.Fatal(err)
			}
			if !reflect.DeepEqual(got, local) {
				t.Fatalf("native differs from local:\n%+v\n%+v", got, local)
			}
			fetched, _ := source.fetched()
			if fetched == 0 || fetched > cfg.Input.MaxBytes {
				t.Fatalf("fetched %d", fetched)
			}
			if file.closed.Load() != 1 {
				t.Fatalf("close count %d", file.closed.Load())
			}
			encoded, _ := json.Marshal(got)
			if bytes.Contains(encoded, []byte("http://")) || bytes.Contains(encoded, []byte("opaque/private/key")) {
				t.Fatalf("private input in converted probe")
			}
		})
	}
}

// Move only the moov atom behind a large free atom. Media offsets stay intact,
// so this is the same real MP4 with a distant index, not an invented container.
func nativeProbeSeekMP4(t *testing.T) []byte {
	t.Helper()
	data, err := os.ReadFile("testdata/test.mp4")
	if err != nil {
		t.Fatal(err)
	}
	var before, index []byte
	for off := 0; off < len(data); {
		if len(data)-off < 8 {
			t.Fatal("short MP4 atom")
		}
		size := int(binary.BigEndian.Uint32(data[off : off+4]))
		if size < 8 || size > len(data)-off {
			t.Fatal("invalid MP4 atom")
		}
		atom := data[off : off+size]
		if string(atom[4:8]) == "moov" {
			index = append(index, atom...)
		} else {
			before = append(before, atom...)
		}
		off += size
	}
	if len(index) == 0 {
		t.Fatal("missing moov")
	}
	free := make([]byte, 16<<20)
	binary.BigEndian.PutUint32(free, uint32(len(free)))
	copy(free[4:8], "free")
	return append(append(before, free...), index...)
}

func TestNativeProbeRealSeekUsesBoundedRanges(t *testing.T) {
	binary := nativeProbeBinary(t)
	data := nativeProbeSeekMP4(t)
	path := filepath.Join(t.TempDir(), "seek.mp4")
	if err := os.WriteFile(path, data, 0600); err != nil {
		t.Fatal(err)
	}
	local, err := ProbeFile(t.Context(), binary, path)
	if err != nil {
		t.Fatal(err)
	}
	source, file, ctx, cfg := nativeProbeFixture(t, data)
	// This fixture's distant index requires HTTP prefetch as well as the
	// useful demuxer bytes. This is a test allowance, not a product default.
	cfg.Input.MaxBytes = 8 << 20
	got, err := ProbeNative(ctx, binary, file, cfg)
	if err != nil {
		fetched, ranges := source.fetched()
		var exit *exec.ExitError
		code := -1
		if errors.As(err, &exit) {
			code = exit.ExitCode()
		}
		t.Fatalf("seek failure: budget=%t deadline=%t EOF=%t exit=%d fetched=%d ranges=%d",
			errors.Is(err, nativestorage.ErrToolInputBudget), errors.Is(err, context.DeadlineExceeded), errors.Is(err, io.ErrUnexpectedEOF), code, fetched, len(ranges))
	}
	if !reflect.DeepEqual(got, local) {
		t.Fatalf("seek probe differs: %+v / %+v", got, local)
	}
	fetched, ranges := source.fetched()
	if fetched > cfg.Input.MaxBytes || fetched >= int64(len(data)) {
		t.Fatalf("unbounded fetched %d of %d", fetched, len(data))
	}
	var distant bool
	for _, r := range ranges {
		if r.offset >= 16<<20 {
			distant = true
		}
	}
	if !distant {
		t.Fatal("ffprobe never sought to distant index")
	}
}

func nativeProbeTestTool(t *testing.T, mode string) string {
	t.Helper()
	executable, err := os.Executable()
	if err != nil {
		t.Fatal(err)
	}
	path := filepath.Join(t.TempDir(), "ffprobe")
	quote := func(s string) string { return "'" + strings.ReplaceAll(s, "'", "'\"'\"'") + "'" }
	script := "#!/bin/sh\nGORACE=atexit_sleep_ms=0 NATIVE_PROBE_TEST_TOOL=" + quote(mode) + " exec " + quote(executable) + " -test.run=^TestNativeProbeToolProcess$ -- \"$@\"\n"
	if err := os.WriteFile(path, []byte(script), 0700); err != nil {
		t.Fatal(err)
	}
	return path
}

// A real child process stands in for ffprobe, including its HTTP client.
func TestNativeProbeToolProcess(t *testing.T) {
	mode := os.Getenv("NATIVE_PROBE_TEST_TOOL")
	if mode == "" {
		return
	}
	args := os.Args
	argument := args[len(args)-1]
	var whitelist bool
	for i, arg := range args {
		if arg == "-protocol_whitelist" && i+1 < len(args) && args[i+1] == "http,tcp" {
			whitelist = true
		}
	}
	if !whitelist {
		os.Exit(93)
	}
	valid := fmt.Sprintf(`{"format":{"filename":%q,"format_name":"mov,mp4,m4a,3gp,3g2,mj2","duration":"1"},"streams":[{"index":0,"codec_type":"video","codec_name":"h264","width":320,"height":240}]}`, argument)
	switch mode {
	case "descendant":
		child := exec.Command("sleep", "30")
		child.Stdout, child.Stderr = os.Stdout, os.Stderr
		if child.Start() != nil {
			os.Exit(94)
		}
		if os.WriteFile(os.Getenv("NATIVE_PROBE_TEST_CHILD_PID"), []byte(fmt.Sprintf("%d %d", os.Getpid(), child.Process.Pid)), 0600) != nil {
			os.Exit(95)
		}
		fmt.Print(valid)
	case "abandon":
		// Like ffprobe at exit: it read what it needed from an open-ended range
		// and exits while the bridge is still serving the rest of that range.
		request, err := http.NewRequest("GET", argument, nil)
		if err != nil {
			os.Exit(96)
		}
		request.Header.Set("Range", "bytes=0-")
		response, err := (&http.Client{Transport: &http.Transport{Proxy: nil}}).Do(request) //nolint:bodyclose // Exits with this range in flight, as ffprobe does.
		if err != nil {
			os.Exit(97)
		}
		if _, err := io.ReadFull(response.Body, make([]byte, 16)); err != nil {
			os.Exit(98)
		}
		gate := os.Getenv("NATIVE_PROBE_TEST_GATE")
		for deadline := time.Now().Add(5 * time.Second); ; time.Sleep(time.Millisecond) {
			if _, err := os.Stat(gate); err == nil {
				break
			}
			if time.Now().After(deadline) {
				os.Exit(99)
			}
		}
		fmt.Print(valid)
	case "read":
		response, err := http.Get(argument)
		if err == nil {
			_, _ = io.Copy(io.Discard, response.Body)
			_ = response.Body.Close()
		}
		fmt.Print(valid)
	case "large-json":
		fmt.Print(strings.Repeat("x", 1<<20))
	case "large-stderr":
		fmt.Fprint(os.Stderr, strings.Repeat("x", 1<<20))
		fmt.Print(valid)
	case "invalid":
		fmt.Print("{bad json")
	case "trailing":
		fmt.Print(valid + `{}`)
	case "empty":
		fmt.Print(`{}`)
	case "private-error":
		fmt.Fprintln(os.Stderr, argument, "private-provider-detail")
		os.Exit(1)
	case "grant-tag":
		fmt.Print(strings.Replace(valid, `"duration":"1"`, `"duration":"1","tags":{"title":`+fmt.Sprintf("%q", argument)+`}`, 1))
	default:
		fmt.Print(valid)
	}
	os.Exit(0)
}

// nativeProbeHeldFile models a slow remote range: reads from offset onward
// stay pending until the source is canceled, as an S3 GET does mid-transfer.
type nativeProbeHeldFile struct {
	mediasource.File
	ctx      context.Context
	from     int64
	gate     string
	once     sync.Once
	canceled atomic.Int32
}

func (f *nativeProbeHeldFile) ReadAt(p []byte, off int64) (int, error) {
	if off < f.from {
		return f.File.ReadAt(p, off)
	}
	f.once.Do(func() { _ = os.WriteFile(f.gate, []byte("pending"), 0600) })
	<-f.ctx.Done()
	f.canceled.Add(1)
	clear(p)
	return 0, errors.Join(f.ctx.Err(), io.ErrUnexpectedEOF)
}

// Cleanup after a successful child cancels the range it abandoned. That
// teardown delivered no byte to the child and must not fail the probe; this was
// how every S3 movie scan ended "cancelled" after ffprobe succeeded.
func TestNativeProbeAbandonedRangeTeardownKeepsResult(t *testing.T) {
	_, file, ctx, cfg := nativeProbeFixture(t, bytes.Repeat([]byte("x"), 64<<10))
	gate := filepath.Join(t.TempDir(), "pending")
	t.Setenv("NATIVE_PROBE_TEST_GATE", gate)
	held := &nativeProbeHeldFile{File: file, ctx: ctx, from: int64(cfg.Input.ChunkBytes), gate: gate}
	got, err := ProbeNative(ctx, nativeProbeTestTool(t, "abandon"), held, cfg)
	if err != nil || got == nil || got.CodecVideo != "h264" {
		t.Fatalf("teardown of an abandoned range failed a successful probe: %+v, %v", got, err)
	}
	if held.canceled.Load() != 1 {
		t.Fatal("the abandoned range was not in flight when cleanup canceled the source")
	}
	if file.closed.Load() != 1 {
		t.Fatalf("close count %d", file.closed.Load())
	}
}

func TestNativeProbeRejectsOutputAndInfrastructure(t *testing.T) {
	for _, mode := range []string{"large-json", "large-stderr", "invalid", "trailing", "empty", "private-error", "grant-tag", "missing"} {
		t.Run(mode, func(t *testing.T) {
			_, file, ctx, cfg := nativeProbeFixture(t, []byte("ordinary fixture bytes"))
			tool := nativeProbeTestTool(t, mode)
			if mode == "missing" {
				tool = filepath.Join(t.TempDir(), "missing")
			}
			got, err := ProbeNative(ctx, tool, file, cfg)
			if err == nil || got != nil {
				t.Fatalf("accepted %s: %+v, %v", mode, got, err)
			}
			if strings.Contains(err.Error(), "http://") || strings.Contains(err.Error(), "private-provider-detail") || strings.Contains(err.Error(), tool) {
				t.Fatalf("private error exposed: %v", err)
			}
			if file.closed.Load() != 1 {
				t.Fatalf("close count %d", file.closed.Load())
			}
			if (mode == "large-json" || mode == "large-stderr") && !errors.Is(err, ErrNativeProbeOutputBudget) {
				t.Fatalf("output budget cause lost: %v", err)
			}
		})
	}
}

func TestNativeProbeSharesHTTPAndSupplementBudget(t *testing.T) {
	data, err := os.ReadFile("testdata/subtitles.mkv")
	if err != nil {
		t.Fatal(err)
	}
	source, file, ctx, cfg := nativeProbeFixture(t, data)
	cfg.Input.MaxBytes = int64(len(data)) // HTTP alone fits; adding supplement must fail.
	got, err := ProbeNative(ctx, nativeProbeTestTool(t, "read"), file, cfg)
	if got != nil || !errors.Is(err, nativestorage.ErrToolInputBudget) {
		t.Fatalf("accepted combined budget: %+v, %v", got, err)
	}
	fetched, _ := source.fetched()
	if fetched > cfg.Input.MaxBytes {
		t.Fatalf("fetched %d exceeds %d", fetched, cfg.Input.MaxBytes)
	}
}

func TestNativeProbeSourceAndAuthorityFailuresAreSticky(t *testing.T) {
	for _, mode := range []string{"full-byte-conflict", "revoke-after-read", "close-after-exit-zero", "final-revoke-after-exit-zero"} {
		t.Run(mode, func(t *testing.T) {
			source, file, ctx, cfg := nativeProbeFixture(t, []byte("ordinary fixture bytes"))
			var revoked atomic.Bool
			sentinel := storagesource.ErrReferenceConflict
			switch mode {
			case "full-byte-conflict":
				source.fail = sentinel
			case "revoke-after-read":
				source.afterRead = func() { revoked.Store(true) }
			case "close-after-exit-zero":
				file.closeErr = sentinel
			}
			if mode == "final-revoke-after-exit-zero" {
				var checks atomic.Int32
				// Non-EBML first byte requires one pre-read with pre/post checks.
				cfg.Input.Reauthorize = func(context.Context) error {
					if checks.Add(1) > 2 {
						return sentinel
					}
					return nil
				}
			} else {
				cfg.Input.Reauthorize = func(context.Context) error {
					if revoked.Load() {
						return sentinel
					}
					return nil
				}
			}
			got, err := ProbeNative(ctx, nativeProbeTestTool(t, "success"), file, cfg)
			if got != nil || !errors.Is(err, sentinel) {
				t.Fatalf("accepted failed authority/source: %+v, %v", got, err)
			}
			if file.closed.Load() != 1 {
				t.Fatalf("close count %d", file.closed.Load())
			}
		})
	}
}

func TestNativeProbeCanceledParentKeepsUntransferredOwnership(t *testing.T) {
	_, file, ctx, cfg := nativeProbeFixture(t, []byte("fixture"))
	canceled, cancel := context.WithCancel(ctx)
	cancel()
	got, err := ProbeNative(canceled, nativeProbeTestTool(t, "success"), file, cfg)
	if got != nil || !errors.Is(err, context.Canceled) {
		t.Fatalf("cancellation lost: %+v, %v", got, err)
	}
	if file.closed.Load() != 0 {
		t.Fatal("ownership transferred after failed admission")
	}
}

func TestNativeProbeDemuxerIsRequiredAndFixed(t *testing.T) {
	for _, demuxer := range []NativeProbeDemuxer{0, 255} {
		_, file, ctx, cfg := nativeProbeFixture(t, []byte("fixture"))
		cfg.Demuxer = demuxer
		got, err := ProbeNative(ctx, nativeProbeTestTool(t, "success"), file, cfg)
		if got != nil || !errors.Is(err, ErrNativeProbeInvalid) {
			t.Fatalf("accepted demuxer %d: %+v, %v", demuxer, got, err)
		}
		if file.closed.Load() != 0 {
			t.Fatal("invalid config transferred ownership")
		}
	}
}

func TestNativeProbeCannotUseInheritedProxy(t *testing.T) {
	video, err := os.ReadFile("testdata/test.mp4")
	if err != nil {
		t.Fatal(err)
	}
	var contacted atomic.Int32
	proxy := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, _ *http.Request) { contacted.Add(1); _, _ = w.Write(video) }))
	defer proxy.Close()
	for _, key := range []string{"http_proxy", "HTTP_PROXY", "https_proxy", "HTTPS_PROXY", "all_proxy", "ALL_PROXY"} {
		t.Setenv(key, proxy.URL)
	}
	t.Setenv("no_proxy", "")
	t.Setenv("NO_PROXY", "")
	source, file, ctx, cfg := nativeProbeFixture(t, []byte("not a media container"))
	got, err := ProbeNative(ctx, nativeProbeBinary(t), file, cfg)
	if got != nil || err == nil {
		t.Errorf("accepted proxy metadata instead of authorized bytes")
	}
	if contacted.Load() != 0 {
		t.Errorf("proxy received %d unauthorized requests", contacted.Load())
	}
	fetched, _ := source.fetched()
	if fetched > cfg.Input.MaxBytes {
		t.Errorf("fetched %d exceeds input bound", fetched)
	}
}

func TestNativeProbeDoesNotPersistFFReport(t *testing.T) {
	report := filepath.Join(t.TempDir(), "ffprobe-report.log")
	t.Setenv("FFREPORT", "file="+report+":level=32")
	data, err := os.ReadFile("testdata/test.mp4")
	if err != nil {
		t.Fatal(err)
	}
	_, file, ctx, cfg := nativeProbeFixture(t, data)
	if _, err := ProbeNative(ctx, nativeProbeBinary(t), file, cfg); err != nil {
		t.Fatal(err)
	}
	if _, err := os.Stat(report); !errors.Is(err, os.ErrNotExist) {
		t.Fatal("ffprobe persisted a report containing its private arguments")
	}
	if os.Getenv("FFREPORT") == "" {
		t.Fatal("consumer changed parent report environment")
	}
}

func TestNativeProbeManifestCannotContactAnotherOrigin(t *testing.T) {
	for _, kind := range []string{"hls", "dash"} {
		t.Run(kind, func(t *testing.T) {
			var contacted atomic.Int32
			video, err := os.ReadFile("testdata/test.mp4")
			if err != nil {
				t.Fatal(err)
			}
			trap := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, _ *http.Request) { contacted.Add(1); _, _ = w.Write(video) }))
			defer trap.Close()
			playlist := "#EXTM3U\n#EXT-X-VERSION:3\n#EXT-X-TARGETDURATION:1\n#EXT-X-MEDIA-SEQUENCE:0\n#EXTINF:1,\n" + trap.URL + "/segment.ts\n#EXT-X-ENDLIST\n"
			if kind == "dash" {
				playlist = `<?xml version="1.0"?><MPD xmlns="urn:mpeg:dash:schema:mpd:2011" type="static" mediaPresentationDuration="PT1S" minBufferTime="PT1S"><Period><AdaptationSet mimeType="video/mp4"><Representation id="1" bandwidth="1000"><BaseURL>` + trap.URL + `/segment.mp4</BaseURL><SegmentBase /></Representation></AdaptationSet></Period></MPD>`
			}
			source, file, ctx, cfg := nativeProbeFixture(t, []byte(playlist))
			got, err := ProbeNative(ctx, nativeProbeBinary(t), file, cfg)
			if contacted.Load() != 0 {
				t.Fatalf("ffprobe contacted unauthorized origin %d times", contacted.Load())
			}
			if err == nil || got != nil {
				t.Fatalf("accepted remote manifest: %+v, %v", got, err)
			}
			fetched, _ := source.fetched()
			if fetched > cfg.Input.MaxBytes {
				t.Fatalf("manifest fetched %d bytes", fetched)
			}
		})
	}
}

type nativeProbeBlockedFile struct {
	mediasource.File
	ctx                                context.Context
	started, canceled, release, joined chan struct{}
	once                               sync.Once
}

func (f *nativeProbeBlockedFile) ReadAt(p []byte, off int64) (int, error) {
	if len(p) <= 16 {
		return f.File.ReadAt(p, off)
	}
	f.once.Do(func() { close(f.started) })
	<-f.ctx.Done()
	close(f.canceled)
	<-f.release
	close(f.joined)
	return 0, f.ctx.Err()
}

func nativeProbeAwait(t *testing.T, done <-chan struct{}) {
	t.Helper()
	select {
	case <-done:
	case <-time.After(5 * time.Second):
		t.Fatal("observable state did not arrive")
	}
}

// Trigger deadline propagation after the child starts its read. A timer
// measured from startup can expire before a race-instrumented helper reads.
// ToolInput's own timer is covered separately by its lifetime tests.
type nativeProbeDeadlineContext struct {
	context.Context
	done <-chan struct{}
}

func (c nativeProbeDeadlineContext) Done() <-chan struct{} { return c.done }
func (c nativeProbeDeadlineContext) Err() error {
	select {
	case <-c.done:
		return context.DeadlineExceeded
	default:
		return c.Context.Err()
	}
}

func TestNativeProbeCancellationJoinsChildAndReadBeforeRelease(t *testing.T) {
	for _, mode := range []string{"parent", "deadline"} {
		t.Run(mode, func(t *testing.T) {
			_, file, ctx, cfg := nativeProbeFixture(t, bytes.Repeat([]byte("x"), 64<<10))
			parent, cancelParent := context.WithCancel(ctx)
			defer cancelParent()
			blocked := &nativeProbeBlockedFile{File: file, ctx: ctx, started: make(chan struct{}), canceled: make(chan struct{}), release: make(chan struct{}), joined: make(chan struct{})}
			var releaseOnce sync.Once
			releaseRead := func() { releaseOnce.Do(func() { close(blocked.release) }) }
			defer releaseRead()
			released := make(chan struct{})
			cfg.Input.ReleaseHost = func() {
				select {
				case <-blocked.joined:
				default:
					t.Error("released host before read joined")
				}
				if file.closed.Load() != 1 {
					t.Error("released host before file closed")
				}
				close(released)
			}
			probeContext := parent
			cancelProbe := cancelParent
			if mode == "deadline" {
				deadline := make(chan struct{})
				var once sync.Once
				cancelProbe = func() { once.Do(func() { close(deadline) }) }
				probeContext = nativeProbeDeadlineContext{Context: parent, done: deadline}
			}
			defer cancelProbe()
			tool := nativeProbeTestTool(t, "read")
			result := make(chan error, 1)
			go func() {
				got, err := ProbeNative(probeContext, tool, blocked, cfg)
				if got != nil {
					err = errors.Join(err, errors.New("accepted canceled result"))
				}
				result <- err
			}()
			nativeProbeAwait(t, blocked.started)
			cancelProbe()
			nativeProbeAwait(t, blocked.canceled)
			select {
			case <-released:
				t.Fatal("host release raced blocked read")
			default:
			}
			select {
			case err := <-result:
				t.Fatalf("probe returned before read joined: %v", err)
			default:
			}
			if file.closed.Load() != 0 {
				t.Fatal("file closed before active read joined")
			}
			releaseRead()
			select {
			case err := <-result:
				want := context.Canceled
				if mode == "deadline" {
					want = context.DeadlineExceeded
				}
				if !errors.Is(err, want) {
					t.Fatalf("cancellation cause lost: %v", err)
				}
			case <-time.After(5 * time.Second):
				t.Fatal("probe did not join after released read")
			}
			nativeProbeAwait(t, released)
		})
	}
}
