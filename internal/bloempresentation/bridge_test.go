package bloempresentation

import (
	"bytes"
	"context"
	"encoding/json"
	"errors"
	"fmt"
	"io"
	"os"
	"os/exec"
	"path/filepath"
	"strings"
	"sync"
	"sync/atomic"
	"testing"
	"time"
)

func TestServeRoundTrip(t *testing.T) {
	var out bytes.Buffer
	err := Serve(strings.NewReader("{\"version\":1,\"operation\":\"echo\",\"payload\":{\"value\":7}}\n"), &out, func(operation string, payload json.RawMessage) (any, error) {
		if operation != "echo" {
			t.Fatalf("operation: %q", operation)
		}
		return payload, nil
	})
	if err != nil {
		t.Fatal(err)
	}
	var result struct {
		Version int `json:"version"`
		Payload struct {
			Value int `json:"value"`
		} `json:"payload"`
	}
	if err := json.Unmarshal(out.Bytes(), &result); err != nil {
		t.Fatal(err)
	}
	if result.Version != 1 || result.Payload.Value != 7 {
		t.Fatalf("response: %s", out.Bytes())
	}
}

func TestServeRejectsInvalidEnvelope(t *testing.T) {
	for _, input := range []string{"garbage\n", "{\"version\":2,\"operation\":\"echo\",\"payload\":null}\n", "{\"version\":1,\"operation\":\"echo\"}\n", "{\"version\":1,\"operation\":\"echo\",\"payload\":null,\"extra\":1}\n", "{\"version\":1,\"operation\":\"echo\",\"payload\":null}", strings.Repeat("x", MaxMessageBytes+1) + "\n"} {
		if err := Serve(strings.NewReader(input), io.Discard, func(string, json.RawMessage) (any, error) { t.Fatal("dispatch called"); return nil, nil }); err == nil {
			t.Fatalf("accepted %.80q", input)
		}
	}
}

func TestServeErrorAndOversizedOutput(t *testing.T) {
	request := "{\"version\":1,\"operation\":\"echo\",\"payload\":null}\n"
	var out bytes.Buffer
	if err := Serve(strings.NewReader(request), &out, func(string, json.RawMessage) (any, error) { return nil, errors.New("failure") }); err != nil {
		t.Fatal(err)
	}
	if !strings.Contains(out.String(), "\"error\":\"failure\"") {
		t.Fatal(out.String())
	}
	out.Reset()
	if err := Serve(strings.NewReader(request), &out, func(string, json.RawMessage) (any, error) { return strings.Repeat("x", MaxMessageBytes), nil }); err != nil {
		t.Fatal(err)
	}
	if !strings.Contains(out.String(), "\"error\"") || out.Len() > MaxMessageBytes {
		t.Fatal("oversized output not bounded")
	}
}

// Runs a real separate test executable through the same stdin/stdout protocol.
func TestWorkerProcess(t *testing.T) {
	marker := "bloem-presentation-test-worker"
	if len(os.Args) < 2 || os.Args[len(os.Args)-2] != marker {
		return
	}
	switch os.Args[len(os.Args)-1] {
	case "crash":
		os.Exit(7)
	case "bad-version":
		fmt.Println(`{"version":9,"payload":null}`)
	case "malformed":
		fmt.Println(`not json`)
	case "huge":
		fmt.Println(strings.Repeat("x", MaxMessageBytes+1))
	case "error":
		fmt.Println(`{"version":1,"payload":null,"error":"failure"}`)
	case "hang":
		_, _ = io.Copy(io.Discard, os.Stdin)
	case "echo":
		err := Serve(os.Stdin, os.Stdout, func(operation string, payload json.RawMessage) (any, error) {
			if operation == "env" {
				return os.Environ(), nil
			}
			return payload, nil
		})
		if err != nil {
			os.Exit(8)
		}
	}
	os.Exit(0)
}

func testClient(t *testing.T, modes ...string) (*Client, *atomic.Int32) {
	t.Helper()
	path, err := os.Executable()
	if err != nil {
		t.Fatal(err)
	}
	client, err := New(path)
	if err != nil {
		t.Fatal(err)
	}
	launches := new(atomic.Int32)
	client.command = func(path string) *exec.Cmd {
		index := int(launches.Add(1)) - 1
		if index >= len(modes) {
			index = len(modes) - 1
		}
		return exec.Command(path, "-test.run=^TestWorkerProcess$", "bloem-presentation-test-worker", modes[index])
	}
	t.Cleanup(func() { _ = client.Close() })
	return client, launches
}

func TestClientRoundTripReusesWorkerAndCleanEnvironment(t *testing.T) {
	t.Setenv("SECRET_KEY", "never-inherit")
	t.Setenv("DATABASE_URL", "never-inherit")
	t.Setenv("AWS_SECRET_ACCESS_KEY", "never-inherit")
	client, launches := testClient(t, "echo")
	for value := range 3 {
		var got map[string]int
		if err := client.Call(context.Background(), "echo", map[string]int{"value": value}, &got); err != nil {
			t.Fatal(err)
		}
		if got["value"] != value {
			t.Fatal(got)
		}
	}
	if launches.Load() != 1 {
		t.Fatalf("launches: %d", launches.Load())
	}
	var env []string
	if err := client.Call(context.Background(), "env", nil, &env); err != nil {
		t.Fatal(err)
	}
	for _, value := range env {
		if strings.Contains(value, "never-inherit") || strings.HasPrefix(value, "HOME=") {
			t.Fatalf("inherited environment: %s", value)
		}
	}
	if len(env) > 4 {
		t.Fatalf("unexpected environment size: %d", len(env))
	}
}

func TestClientRestartsAfterFailure(t *testing.T) {
	for _, mode := range []string{"crash", "bad-version", "malformed", "huge", "error"} {
		t.Run(mode, func(t *testing.T) {
			client, launches := testClient(t, mode, "echo")
			ctx, cancel := context.WithTimeout(context.Background(), 5*time.Second)
			defer cancel()
			var got int
			if err := client.Call(ctx, "echo", 7, &got); err == nil {
				t.Fatal("accepted failing worker")
			}
			if err := client.Call(ctx, "echo", 8, &got); err != nil {
				t.Fatal(err)
			}
			if got != 8 || launches.Load() != 2 {
				t.Fatalf("result=%d launches=%d", got, launches.Load())
			}
		})
	}
}

func TestCancellationRestartsWorker(t *testing.T) {
	client, launches := testClient(t, "hang", "echo")
	ctx, cancel := context.WithTimeout(context.Background(), 100*time.Millisecond)
	defer cancel()
	var got int
	if err := client.Call(ctx, "echo", 7, &got); !errors.Is(err, context.DeadlineExceeded) {
		t.Fatalf("cancel: %v", err)
	}
	if err := client.Call(context.Background(), "echo", 8, &got); err != nil {
		t.Fatal(err)
	}
	if got != 8 || launches.Load() != 2 {
		t.Fatalf("result=%d launches=%d", got, launches.Load())
	}
}

func TestConcurrentCallsMatchResponses(t *testing.T) {
	client, _ := testClient(t, "echo")
	var wg sync.WaitGroup
	for value := range 20 {
		wg.Add(1)
		go func() {
			defer wg.Done()
			var got int
			if err := client.Call(context.Background(), "echo", value, &got); err != nil {
				t.Error(err)
			} else if got != value {
				t.Errorf("got %d want %d", got, value)
			}
		}()
	}
	wg.Wait()
}

func TestCloseInterruptsWorkerAndWaitingCalls(t *testing.T) {
	client, _ := testClient(t, "hang")
	ctx, cancel := context.WithTimeout(context.Background(), 5*time.Second)
	defer cancel()
	first := make(chan error, 1)
	go func() { first <- client.Call(ctx, "echo", 7, nil) }()
	// Synchronize on ownership of the call gate, not a timed sleep.
	for len(client.gate) != 0 {
		select {
		case <-ctx.Done():
			t.Fatal(ctx.Err())
		default:
		}
	}
	if err := client.Close(); err != nil {
		t.Fatal(err)
	}
	select {
	case err := <-first:
		if err == nil {
			t.Fatal("closed call succeeded")
		}
	case <-ctx.Done():
		t.Fatal(ctx.Err())
	}
	if err := client.Call(ctx, "echo", 7, nil); err == nil {
		t.Fatal("call after close succeeded")
	}
}

func TestClientInputLimitAndAbsolutePath(t *testing.T) {
	if _, err := New("relative-worker"); err == nil {
		t.Fatal("relative worker accepted")
	}
	client, launches := testClient(t, "echo")
	if err := client.Call(context.Background(), "echo", strings.Repeat("x", MaxMessageBytes), nil); err == nil {
		t.Fatal("oversized request accepted")
	}
	if launches.Load() != 0 {
		t.Fatal("invalid request launched worker")
	}
	if !filepath.IsAbs(client.path) {
		t.Fatal("test path")
	}
}

func TestQueuedCancellationDoesNotRestartActiveWorker(t *testing.T) {
	client, launches := testClient(t, "hang", "echo")
	active := make(chan error, 1)
	ctx, cancel := context.WithCancel(context.Background())
	go func() { active <- client.Call(ctx, "echo", 7, nil) }()
	deadline := time.After(5 * time.Second)
	for launches.Load() == 0 {
		select {
		case <-deadline:
			t.Fatal("worker never launched")
		default:
		}
	}
	queuedCtx, queuedCancel := context.WithCancel(context.Background())
	queuedCancel()
	if err := client.Call(queuedCtx, "echo", 9, nil); !errors.Is(err, context.Canceled) {
		t.Fatalf("queued cancel: %v", err)
	}
	cancel()
	if err := <-active; !errors.Is(err, context.Canceled) {
		t.Fatalf("active cancel: %v", err)
	}
	var got int
	if err := client.Call(context.Background(), "echo", 11, &got); err != nil {
		t.Fatal(err)
	}
	if got != 11 || launches.Load() != 2 {
		t.Fatalf("result=%d launches=%d", got, launches.Load())
	}
}

func TestBackgroundCallHasDeadline(t *testing.T) {
	client, _ := testClient(t, "hang")
	started := time.Now()
	if err := client.Call(context.Background(), "echo", 7, nil); !errors.Is(err, context.DeadlineExceeded) {
		t.Fatalf("deadline: %v", err)
	}
	if elapsed := time.Since(started); elapsed > 2*time.Second {
		t.Fatalf("call blocked for %s", elapsed)
	}
}

func TestInvalidResponsePayloadRestartsWorker(t *testing.T) {
	client, launches := testClient(t, "echo")
	var got int
	if err := client.Call(context.Background(), "echo", "wrong type", &got); err == nil {
		t.Fatal("response decode succeeded")
	}
	if err := client.Call(context.Background(), "echo", 7, &got); err != nil {
		t.Fatal(err)
	}
	if got != 7 || launches.Load() != 2 {
		t.Fatalf("result=%d launches=%d", got, launches.Load())
	}
}

type shortWriter struct{}

func (shortWriter) Write(data []byte) (int, error) { return len(data) - 1, nil }
func TestServeRejectsShortWrite(t *testing.T) {
	input := strings.NewReader("{\"version\":1,\"operation\":\"echo\",\"payload\":null}\n")
	if err := Serve(input, shortWriter{}, func(string, json.RawMessage) (any, error) { return nil, nil }); !errors.Is(err, io.ErrShortWrite) {
		t.Fatalf("short write: %v", err)
	}
}

type marshalerFunc func() ([]byte, error)

func (marshal marshalerFunc) MarshalJSON() ([]byte, error) { return marshal() }

func TestCancelledCallDoesNotEncodeRequest(t *testing.T) {
	client, launches := testClient(t, "echo")
	var encodes atomic.Int32
	request := marshalerFunc(func() ([]byte, error) { encodes.Add(1); return []byte("7"), nil })
	ctx, cancel := context.WithCancel(context.Background())
	cancel()
	if err := client.Call(ctx, "echo", request, nil); !errors.Is(err, context.Canceled) {
		t.Fatalf("cancel: %v", err)
	}
	if encodes.Load() != 0 || launches.Load() != 0 {
		t.Fatalf("encodes=%d launches=%d", encodes.Load(), launches.Load())
	}
}

func TestRequestEncodingCancellationRetainsGateUntilCleanup(t *testing.T) {
	client, launches := testClient(t, "echo")
	started, release := make(chan struct{}), make(chan struct{})
	var releaseOnce sync.Once
	unblock := func() { releaseOnce.Do(func() { close(release) }) }
	defer unblock()
	request := marshalerFunc(func() ([]byte, error) { close(started); <-release; return []byte("7"), nil })
	ctx, cancel := context.WithTimeout(context.Background(), 100*time.Millisecond)
	defer cancel()
	response := 99
	result := make(chan error, 1)
	go func() { result <- client.Call(ctx, "echo", request, &response) }()
	select {
	case <-started:
	case <-time.After(time.Second):
		t.Fatal("encoder did not start")
	}
	select {
	case err := <-result:
		if !errors.Is(err, context.DeadlineExceeded) {
			t.Fatalf("encoding deadline: %v", err)
		}
	case <-time.After(time.Second):
		t.Fatal("cancelled call waited for encoder")
	}
	if response != 99 || launches.Load() != 0 {
		t.Fatalf("response=%d launches=%d", response, launches.Load())
	}

	var extraEncodes atomic.Int32
	queuedRequest := marshalerFunc(func() ([]byte, error) { extraEncodes.Add(1); return []byte("8"), nil })
	queuedCtx, queuedCancel := context.WithTimeout(context.Background(), 100*time.Millisecond)
	defer queuedCancel()
	if err := client.Call(queuedCtx, "echo", queuedRequest, nil); !errors.Is(err, context.DeadlineExceeded) {
		t.Fatalf("queued encoding: %v", err)
	}
	if extraEncodes.Load() != 0 || launches.Load() != 0 {
		t.Fatalf("queued encodes=%d launches=%d", extraEncodes.Load(), launches.Load())
	}
	unblock()
	var got int
	if err := client.Call(context.Background(), "echo", 11, &got); err != nil {
		t.Fatal(err)
	}
	if got != 11 || response != 99 || launches.Load() != 1 {
		t.Fatalf("result=%d abandoned response=%d launches=%d", got, response, launches.Load())
	}
}

func TestBackgroundEncodingHasDeadline(t *testing.T) {
	client, launches := testClient(t, "echo")
	release := make(chan struct{})
	defer close(release)
	request := marshalerFunc(func() ([]byte, error) { <-release; return []byte("7"), nil })
	result := make(chan error, 1)
	go func() { result <- client.Call(context.Background(), "echo", request, nil) }()
	select {
	case err := <-result:
		if !errors.Is(err, context.DeadlineExceeded) {
			t.Fatalf("encoding deadline: %v", err)
		}
	case <-time.After(1500 * time.Millisecond):
		t.Fatal("encoding exceeded total call budget")
	}
	if launches.Load() != 0 {
		t.Fatal("cancelled encoder launched worker")
	}
}

func TestCloseInterruptsRequestEncoding(t *testing.T) {
	client, launches := testClient(t, "echo")
	started, release := make(chan struct{}), make(chan struct{})
	defer close(release)
	request := marshalerFunc(func() ([]byte, error) { close(started); <-release; return []byte("7"), nil })
	result := make(chan error, 1)
	go func() { result <- client.Call(context.Background(), "echo", request, nil) }()
	select {
	case <-started:
	case <-time.After(time.Second):
		t.Fatal("encoder did not start")
	}
	if err := client.Close(); err != nil {
		t.Fatal(err)
	}
	select {
	case err := <-result:
		if err == nil {
			t.Fatal("closed call succeeded")
		}
	case <-time.After(time.Second):
		t.Fatal("close waited for encoder")
	}
	if launches.Load() != 0 {
		t.Fatal("closed encoder launched worker")
	}
}
