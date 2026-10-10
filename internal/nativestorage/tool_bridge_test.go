package nativestorage

import (
	"context"
	"errors"
	"io"
	"net/http"
	"net/url"
	"os"
	"path/filepath"
	"strconv"
	"strings"
	"sync/atomic"
	"testing"
	"time"

	"github.com/Silo-Server/silo-server/internal/mediasource"
)

type toolFixtureSource struct {
	data  string
	reads atomic.Int64
	read  func(context.Context, int64, int64, io.Writer) error
}

func (s *toolFixtureSource) Stat(context.Context, mediasource.Ref) (mediasource.Info, error) {
	return mediasource.Info{Name: "private name", LogicalPath: "opaque/../private path", Revision: "immutable-fixture", Size: int64(len(s.data))}, nil
}

func (s *toolFixtureSource) ReadRange(ctx context.Context, ref mediasource.Ref, offset, length int64, w io.Writer) error {
	s.reads.Add(1)
	if ref != (mediasource.Ref{SourceID: "source", EntryID: " opaque/../entry ", Revision: "immutable-fixture"}) {
		return errors.New("incorrect immutable reference")
	}
	if s.read != nil {
		return s.read(ctx, offset, length, w)
	}
	_, err := io.WriteString(w, s.data[offset:offset+length])
	return err
}

func toolFixtureInput(t *testing.T, source *toolFixtureSource, configure func(*ToolInputConfig)) *ToolInput {
	t.Helper()
	ctx, cancel := context.WithCancel(t.Context())
	file, err := mediasource.Open(ctx, source, mediasource.Ref{SourceID: "source", EntryID: " opaque/../entry ", Revision: "immutable-fixture"})
	if err != nil {
		cancel()
		t.Fatal(err)
	}
	config := ToolInputConfig{CancelSource: cancel, Reauthorize: func(context.Context) error { return nil }, MaxBytes: 1 << 20, ChunkBytes: 4, MaxConcurrentReads: 2, Timeout: 5 * time.Second}
	if configure != nil {
		configure(&config)
	}
	input, err := NewToolInput(ctx, file, config)
	if err != nil {
		cancel()
		_ = file.Close()
		t.Fatal(err)
	}
	t.Cleanup(func() { _ = input.Close() })
	return input
}

func toolHTTP(t *testing.T) *http.Client {
	t.Helper()
	transport := &http.Transport{Proxy: nil}
	t.Cleanup(transport.CloseIdleConnections)
	return &http.Client{Transport: transport, Timeout: 5 * time.Second, CheckRedirect: func(*http.Request, []*http.Request) error { return errors.New("redirect refused") }}
}

type toolHTTPResponse struct {
	StatusCode    int
	ContentLength int64
	Header        http.Header
}

func toolResponse(t *testing.T, client *http.Client, method, argument, byteRange string) (*toolHTTPResponse, string, error) {
	t.Helper()
	r, err := http.NewRequest(method, argument, nil)
	if err != nil {
		t.Fatal(err)
	}
	if byteRange != "" {
		r.Header.Set("Range", byteRange)
	}
	response, err := client.Do(r)
	if err != nil {
		return nil, "", err
	}
	body, readErr := io.ReadAll(response.Body)
	_ = response.Body.Close()
	return &toolHTTPResponse{StatusCode: response.StatusCode, ContentLength: response.ContentLength, Header: response.Header.Clone()}, string(body), readErr
}

// A wrong range offset/length or a capability derived from provider metadata
// must fail against these independently checked responses.
func TestToolInputSeekRangesAndPrivateCapability(t *testing.T) {
	source := &toolFixtureSource{data: "0123456789abcdef"}
	input := toolFixtureInput(t, source, nil)
	client := toolHTTP(t)
	err := input.Run(func(_ context.Context, argument string) error {
		u, err := url.Parse(argument)
		if err != nil || u.Scheme != "http" || u.Hostname() != "127.0.0.1" || u.RawQuery != "" || len(u.Path) < 40 || strings.Contains(argument, "entry") {
			t.Fatalf("input is not a private opaque literal-loopback capability")
		}
		for _, tc := range []struct {
			method, byteRange, body, contentRange string
			status                                int
			length                                int64
		}{
			{"HEAD", "", "", "", 200, 16},
			{"GET", "bytes=12-15", "cdef", "bytes 12-15/16", 206, 4},
			{"GET", "bytes=3-5", "345", "bytes 3-5/16", 206, 3},
			{"GET", "bytes=-2", "ef", "bytes 14-15/16", 206, 2},
			{"GET", "bytes=14-", "ef", "bytes 14-15/16", 206, 2},
			{"GET", "", "0123456789abcdef", "", 200, 16},
		} {
			response, body, err := toolResponse(t, client, tc.method, argument, tc.byteRange)
			if err != nil || response.StatusCode != tc.status || body != tc.body || response.ContentLength != tc.length || response.Header.Get("Content-Range") != tc.contentRange || response.Header.Get("Accept-Ranges") != "bytes" {
				t.Fatalf("%s %s: response=%v body=%q error=%v", tc.method, tc.byteRange, response, body, err)
			}
		}
		return nil
	})
	if err != nil || input.Err() != nil {
		t.Fatalf("successful seekable input failed: %v", err)
	}
}

func TestToolInputRejectsUnknownUnsupportedAndInvalidRangesWithoutRead(t *testing.T) {
	source := &toolFixtureSource{data: "0123456789abcdef"}
	input := toolFixtureInput(t, source, nil)
	client := toolHTTP(t)
	if err := input.Run(func(_ context.Context, argument string) error {
		for _, tc := range []struct {
			method, suffix, byteRange string
			status                    int
		}{
			{"GET", "/unknown", "", 404},
			{"GET", "?key=other", "", 404},
			{"POST", "", "", 405},
			{"GET", "", "bytes=0-1,3-4", 416},
			{"GET", "", "bytes=16-20", 416},
			{"GET", "", "bytes=-0", 416},
			{"GET", "", "bytes=4-2", 416},
			{"GET", "", "items=0-1", 416},
		} {
			response, _, err := toolResponse(t, client, tc.method, argument+tc.suffix, tc.byteRange)
			if err != nil || response.StatusCode != tc.status {
				t.Fatalf("unsupported input: status=%v error=%v", response, err)
			}
		}
		r, _ := http.NewRequest("GET", argument, nil)
		r.Host = "external.example.invalid"
		response, err := client.Do(r)
		if err != nil {
			t.Fatal(err)
		}
		_ = response.Body.Close()
		if response.StatusCode != 404 || source.reads.Load() != 0 {
			t.Fatal("unknown host/capability or unsupported range reached media IO")
		}
		return nil
	}); err != nil {
		t.Fatal(err)
	}
}

func TestToolInputFailedPinnedChunkCommitsNoBytesAndInvalidatesExitZero(t *testing.T) {
	conflict := errors.New("private provider revision conflict")
	for _, failOffset := range []int64{0, 4} {
		t.Run(string(rune('0'+failOffset)), func(t *testing.T) {
			source := &toolFixtureSource{data: "0123456789abcdef"}
			source.read = func(_ context.Context, offset, length int64, w io.Writer) error {
				_, err := io.WriteString(w, source.data[offset:offset+length])
				if offset == failOffset {
					return conflict // Error accompanying every requested byte must win.
				}
				return err
			}
			input := toolFixtureInput(t, source, nil)
			client := toolHTTP(t)
			err := input.Run(func(_ context.Context, argument string) error {
				response, body, readErr := toolResponse(t, client, "GET", argument, "")
				if failOffset == 0 {
					if response != nil && (response.StatusCode != 503 || strings.Contains(body, "0123")) {
						t.Fatalf("failed first range committed media bytes: %q", body)
					}
				} else if response == nil || response.StatusCode != 200 || body != "0123" || readErr == nil {
					t.Fatalf("failed later range committed bytes or completed response: body=%q error=%v", body, readErr)
				}
				return nil // Simulate a tool accepting partial output and exiting zero.
			})
			if !errors.Is(err, conflict) || !errors.Is(input.Err(), conflict) || strings.Contains(err.Error(), "private provider") {
				t.Fatalf("terminal source error was lost or exposed: %v", err)
			}
		})
	}
}

func TestToolInputReauthorizationBeforeAndAfterRead(t *testing.T) {
	denied := errors.New("private authority denial")
	for _, afterRead := range []bool{false, true} {
		t.Run(map[bool]string{false: "before", true: "after"}[afterRead], func(t *testing.T) {
			var revoked atomic.Bool
			revoked.Store(!afterRead)
			source := &toolFixtureSource{data: "0123456789abcdef"}
			source.read = func(_ context.Context, offset, length int64, w io.Writer) error {
				_, err := io.WriteString(w, source.data[offset:offset+length])
				revoked.Store(true)
				return err
			}
			input := toolFixtureInput(t, source, func(config *ToolInputConfig) {
				config.Reauthorize = func(context.Context) error {
					if revoked.Load() {
						return denied
					}
					return nil
				}
			})
			client := toolHTTP(t)
			err := input.Run(func(_ context.Context, argument string) error {
				_, body, _ := toolResponse(t, client, "GET", argument, "bytes=0-3")
				if strings.Contains(body, "0123") {
					t.Fatal("revoked range committed media bytes")
				}
				return nil
			})
			if !errors.Is(err, denied) || (!afterRead && source.reads.Load() != 0) {
				t.Fatalf("current authority was bypassed: %v", err)
			}
		})
	}
}

func TestToolInputByteBudgetCannotBeBypassedByMultipleRanges(t *testing.T) {
	source := &toolFixtureSource{data: "0123456789abcdef"}
	input := toolFixtureInput(t, source, func(config *ToolInputConfig) { config.MaxBytes = 4 })
	client := toolHTTP(t)
	err := input.Run(func(_ context.Context, argument string) error {
		_, body, err := toolResponse(t, client, "GET", argument, "bytes=0-3")
		if err != nil || body != "0123" {
			t.Fatalf("within-budget range failed: %q %v", body, err)
		}
		_, body, _ = toolResponse(t, client, "GET", argument, "bytes=4-7")
		if strings.Contains(body, "4567") {
			t.Fatal("exhausted budget committed media bytes")
		}
		return nil
	})
	if !errors.Is(err, ErrToolInputBudget) || source.reads.Load() != 1 {
		t.Fatalf("per-job budget was not retained across requests: %v", err)
	}
}

func TestToolInputOrdinaryFileCompatibilityAndClosedHandle(t *testing.T) {
	ctx, cancel := context.WithCancel(t.Context())
	path := filepath.Join(t.TempDir(), "ordinary.bin")
	if err := os.WriteFile(path, []byte("local fixture"), 0600); err != nil {
		t.Fatal(err)
	}
	file, err := mediasource.OpenLocal(ctx, path)
	if err != nil {
		t.Fatal(err)
	}
	input, err := NewToolInput(ctx, file, ToolInputConfig{CancelSource: cancel, Reauthorize: func(context.Context) error { return nil }, MaxBytes: 64, ChunkBytes: 4, MaxConcurrentReads: 1, Timeout: 5 * time.Second})
	if err != nil {
		t.Fatal(err)
	}
	client := toolHTTP(t)
	err = input.Run(func(_ context.Context, argument string) error {
		_, body, err := toolResponse(t, client, "GET", argument, "bytes=6-12")
		if err != nil || body != "fixture" {
			t.Fatalf("ordinary file range changed: %q %v", body, err)
		}
		return nil
	})
	if err != nil {
		t.Fatal(err)
	}
	if _, err := file.ReadAt(make([]byte, 1), 0); err == nil {
		t.Fatal("joined cleanup did not close owned file")
	}
	if err := input.Run(func(context.Context, string) error { t.Fatal("closed handle was reused"); return nil }); !errors.Is(err, ErrToolInputClosed) {
		t.Fatalf("closed input admission=%v", err)
	}
}

func TestToolInputSparseFileSeekDoesNotFetchWholeObject(t *testing.T) {
	ctx, cancel := context.WithCancel(t.Context())
	const size = int64(4 << 30)
	path := filepath.Join(t.TempDir(), "sparse.bin")
	fixture, err := os.Create(path)
	if err != nil {
		t.Fatal(err)
	}
	if err := fixture.Truncate(size); err != nil {
		t.Fatal(err)
	}
	if _, err := fixture.WriteAt([]byte("HEAD!!"), 0); err != nil {
		t.Fatal(err)
	}
	if _, err := fixture.WriteAt([]byte("TAIL!!"), size-6); err != nil {
		t.Fatal(err)
	}
	if err := fixture.Close(); err != nil {
		t.Fatal(err)
	}
	file, err := mediasource.OpenLocal(ctx, path)
	if err != nil {
		t.Fatal(err)
	}
	input, err := NewToolInput(ctx, file, ToolInputConfig{CancelSource: cancel, Reauthorize: func(context.Context) error { return nil }, MaxBytes: 12, ChunkBytes: 6, MaxConcurrentReads: 1, Timeout: 5 * time.Second})
	if err != nil {
		t.Fatal(err)
	}
	client := toolHTTP(t)
	if err := input.Run(func(_ context.Context, argument string) error {
		response, _, err := toolResponse(t, client, "HEAD", argument, "")
		if err != nil || response.ContentLength != size {
			t.Fatalf("large fixed-length input changed: %v", err)
		}
		for _, tc := range []struct{ byteRange, want string }{{"bytes=0-5", "HEAD!!"}, {"bytes=" + strconv.FormatInt(size-6, 10) + "-", "TAIL!!"}} {
			_, body, err := toolResponse(t, client, "GET", argument, tc.byteRange)
			if err != nil || body != tc.want {
				t.Fatalf("sparse seek failed within 12-byte budget: %q %v", body, err)
			}
		}
		return nil
	}); err != nil {
		t.Fatal(err)
	}
}

func TestToolInputUnexpectedEOFInvalidatesResult(t *testing.T) {
	for _, terminal := range []bool{false, true} {
		t.Run(map[bool]string{false: "short", true: "terminal_eof"}[terminal], func(t *testing.T) {
			source := &toolFixtureSource{data: "0123456789abcdef"}
			source.read = func(_ context.Context, _, _ int64, w io.Writer) error {
				if terminal {
					_, _ = io.WriteString(w, "0123")
					return io.EOF
				}
				_, _ = io.WriteString(w, "01")
				return nil
			}
			input := toolFixtureInput(t, source, nil)
			client := toolHTTP(t)
			err := input.Run(func(_ context.Context, argument string) error {
				_, body, _ := toolResponse(t, client, "GET", argument, "bytes=0-3")
				if strings.Contains(body, "01") {
					t.Fatal("failed pinned chunk committed partial bytes")
				}
				return nil
			})
			want := io.ErrUnexpectedEOF
			if terminal {
				want = io.EOF
			}
			if !errors.Is(err, want) {
				t.Fatalf("range completeness failure hidden: %v", err)
			}
		})
	}
}
