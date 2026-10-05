// Package bloempresentation runs reviewed, bundled presentation evaluators in
// local workers. This is a narrow Bloem bridge, not a catalog plugin SDK.
package bloempresentation

import (
	"bufio"
	"bytes"
	"context"
	"encoding/json"
	"errors"
	"fmt"
	"io"
	"os"
	"os/exec"
	"path/filepath"
	"sync"
	"time"
)

// MaxMessageBytes bounds each complete JSON line, including its newline.
const MaxMessageBytes = 4 << 20

const maxOperationBytes = 128
const maxErrorBytes = 1024
const maxCallDuration = 750 * time.Millisecond

// Runner is the host-side evaluator contract used by presentation adapters.
type Runner interface {
	Call(context.Context, string, any, any) error
}

// Dispatch evaluates one operation from its JSON payload, without host services.
type Dispatch func(operation string, payload json.RawMessage) (any, error)

type requestEnvelope struct {
	Version   int             `json:"version"`
	Operation string          `json:"operation"`
	Payload   json.RawMessage `json:"payload"`
}

type responseEnvelope struct {
	Version int             `json:"version"`
	Payload json.RawMessage `json:"payload"`
	Error   string          `json:"error,omitempty"`
}

func readLine(reader *bufio.Reader) ([]byte, error) {
	var line []byte
	for {
		chunk, err := reader.ReadSlice(byte(10))
		if len(line)+len(chunk) > MaxMessageBytes {
			return nil, errors.New("presentation message exceeds limit")
		}
		line = append(line, chunk...)
		if err == nil {
			return line[:len(line)-1], nil
		}
		if errors.Is(err, bufio.ErrBufferFull) {
			continue
		}
		if errors.Is(err, io.EOF) && len(line) > 0 {
			return nil, errors.New("unterminated presentation message")
		}
		return nil, err
	}
}

func decodeEnvelope(data []byte, target any) error {
	decoder := json.NewDecoder(bytes.NewReader(data))
	decoder.DisallowUnknownFields()
	if err := decoder.Decode(target); err != nil {
		return fmt.Errorf("invalid presentation envelope: %w", err)
	}
	if decoder.Decode(new(any)) != io.EOF {
		return errors.New("trailing presentation JSON")
	}
	return nil
}

func validOperation(operation string) bool {
	return operation != "" && len(operation) <= maxOperationBytes
}

// Serve processes version-one newline-delimited JSON until clean EOF. Invalid
// framing terminates the worker; operation errors are returned in an envelope.
// stdout must be reserved for this protocol; callers may use stderr for logs.
func Serve(reader io.Reader, writer io.Writer, dispatch Dispatch) error {
	if dispatch == nil {
		return errors.New("presentation dispatch is required")
	}
	input := bufio.NewReader(reader)
	for {
		line, err := readLine(input)
		if errors.Is(err, io.EOF) {
			return nil
		}
		if err != nil {
			return err
		}
		var request requestEnvelope
		if err := decodeEnvelope(line, &request); err != nil {
			return err
		}
		if request.Version != 1 || !validOperation(request.Operation) || len(request.Payload) == 0 {
			return errors.New("invalid presentation request")
		}
		result, dispatchErr := dispatch(request.Operation, request.Payload)
		response := responseEnvelope{Version: 1, Payload: json.RawMessage("null")}
		if dispatchErr != nil {
			response.Error = boundedError(dispatchErr)
		} else {
			response.Payload, err = json.Marshal(result)
			if err != nil {
				response.Payload = json.RawMessage("null")
				response.Error = "cannot encode presentation result"
			}
		}
		data, err := json.Marshal(response)
		if err != nil {
			return err
		}
		if len(data)+1 > MaxMessageBytes {
			data, _ = json.Marshal(responseEnvelope{Version: 1, Payload: json.RawMessage("null"), Error: "presentation result exceeds limit"})
		}
		data = append(data, byte(10))
		if n, err := writer.Write(data); err != nil {
			return err
		} else if n != len(data) {
			return io.ErrShortWrite
		}
	}
}

func boundedError(err error) string {
	message := err.Error()
	if len(message) > maxErrorBytes {
		message = message[:maxErrorBytes]
	}
	return message
}

// Client serializes exchanges with one lazily started worker. Cancellation and
// protocol errors terminate that worker; the next call starts a fresh process.
// A client must be closed during host shutdown.
type Client struct {
	path    string
	command func(string) *exec.Cmd
	gate    chan struct{}
	closed  chan struct{}
	mu      sync.Mutex
	process *worker
}

type worker struct {
	cmd      *exec.Cmd
	input    io.WriteCloser
	output   io.ReadCloser
	reader   *bufio.Reader
	exited   chan struct{}
	stopOnce sync.Once
}

// New accepts only an absolute, reviewed executable path. It never searches
// PATH or accepts a shell command. Launch and executable checks are lazy.
func New(path string) (*Client, error) {
	if !filepath.IsAbs(path) {
		return nil, errors.New("presentation worker path must be absolute")
	}
	client := &Client{path: path, command: func(path string) *exec.Cmd { return exec.Command(path) }, gate: make(chan struct{}, 1), closed: make(chan struct{})}
	client.gate <- struct{}{}
	return client, nil
}

func (c *Client) start() (*worker, error) {
	c.mu.Lock()
	defer c.mu.Unlock()
	select {
	case <-c.closed:
		return nil, errors.New("presentation client is closed")
	default:
	}
	if c.process != nil {
		select {
		case <-c.process.exited:
			c.process.stop()
			c.process = nil
		default:
			return c.process, nil
		}
	}
	cmd := c.command(c.path)
	// Never inherit the application environment, credentials, proxies or HOME.
	cmd.Env = []string{"LANG=C.UTF-8", "LC_ALL=C.UTF-8", "TZ=UTC"}
	// Discarding stderr keeps noisy workers from blocking or growing host memory.
	cmd.Stderr = io.Discard
	input, err := cmd.StdinPipe()
	if err != nil {
		return nil, err
	}
	output, childOutput, err := os.Pipe()
	if err != nil {
		_ = input.Close()
		return nil, err
	}
	cmd.Stdout = childOutput
	if err := cmd.Start(); err != nil {
		_ = input.Close()
		_ = output.Close()
		_ = childOutput.Close()
		return nil, fmt.Errorf("start presentation worker: %w", err)
	}
	_ = childOutput.Close()
	process := &worker{cmd: cmd, input: input, output: output, reader: bufio.NewReader(output), exited: make(chan struct{})}
	go func() { _ = cmd.Wait(); close(process.exited) }()
	c.process = process
	return process, nil
}

func (w *worker) stop() {
	w.stopOnce.Do(func() {
		_ = w.input.Close()
		_ = w.output.Close()
		_ = w.cmd.Process.Kill()
		<-w.exited
	})
}

func (c *Client) reset(process *worker) {
	c.mu.Lock()
	if c.process == process {
		c.process = nil
	}
	c.mu.Unlock()
	process.stop()
}

func encodeRequest(operation string, request any) ([]byte, error) {
	payload, err := json.Marshal(request)
	if err != nil {
		return nil, fmt.Errorf("encode presentation request: %w", err)
	}
	if len(payload) > MaxMessageBytes {
		return nil, errors.New("presentation request exceeds limit")
	}
	data, err := json.Marshal(requestEnvelope{Version: 1, Operation: operation, Payload: payload})
	if err != nil {
		return nil, err
	}
	if len(data)+1 > MaxMessageBytes {
		return nil, errors.New("presentation request exceeds limit")
	}
	return append(data, byte(10)), nil
}

// Call applies a 750-millisecond budget to queue admission, request encoding,
// and worker I/O, bounded by an earlier caller deadline. Cancellation does not
// wait for arbitrary MarshalJSON callbacks: one unfinished encoder retains the
// client gate until it exits, preventing additional encoders or worker launches.
// Requests must remain immutable until their MarshalJSON callbacks finish.
// Process startup/reaping and response decoding cannot be forcibly preempted;
// reviewed workers and plain, bounded response DTOs are required. Response is
// a pointer accepted by json.Unmarshal, or nil to discard the value.
func (c *Client) Call(ctx context.Context, operation string, request, response any) error {
	if ctx == nil {
		return errors.New("presentation context is required")
	}
	if err := ctx.Err(); err != nil {
		return err
	}
	ctx, cancel := context.WithTimeout(ctx, maxCallDuration)
	defer cancel()
	if !validOperation(operation) {
		return errors.New("invalid presentation operation")
	}
	select {
	case <-ctx.Done():
		return ctx.Err()
	case <-c.closed:
		return errors.New("presentation client is closed")
	case <-c.gate:
	}
	releaseGate := true
	defer func() {
		if releaseGate {
			c.gate <- struct{}{}
		}
	}()
	if err := ctx.Err(); err != nil {
		return err
	}
	select {
	case <-c.closed:
		return errors.New("presentation client is closed")
	default:
	}

	// An unbuffered handoff transfers gate ownership to the caller only when it
	// receives the result. After cancellation the encoder releases the gate itself
	// and cannot access the worker or response destination.
	type encodingResult struct {
		data []byte
		err  error
	}
	encoded := make(chan encodingResult)
	abandoned := make(chan struct{})
	go func() {
		data, err := encodeRequest(operation, request)
		select {
		case encoded <- encodingResult{data: data, err: err}:
		case <-abandoned:
			c.gate <- struct{}{}
		}
	}()
	var data []byte
	select {
	case <-ctx.Done():
		releaseGate = false
		close(abandoned)
		return ctx.Err()
	case <-c.closed:
		releaseGate = false
		close(abandoned)
		return errors.New("presentation client is closed")
	case result := <-encoded:
		if err := ctx.Err(); err != nil {
			return err
		}
		select {
		case <-c.closed:
			return errors.New("presentation client is closed")
		default:
		}
		if result.err != nil {
			return result.err
		}
		data = result.data
	}
	process, err := c.start()
	if err != nil {
		return err
	}
	result := make(chan error, 1)
	go func() {
		if _, err := process.input.Write(data); err != nil {
			result <- fmt.Errorf("write presentation request: %w", err)
			return
		}
		line, err := readLine(process.reader)
		if err != nil {
			result <- fmt.Errorf("read presentation response: %w", err)
			return
		}
		var envelope responseEnvelope
		if err := decodeEnvelope(line, &envelope); err != nil {
			result <- err
			return
		}
		if envelope.Version != 1 || len(envelope.Payload) == 0 {
			result <- errors.New("invalid presentation response")
			return
		}
		if envelope.Error != "" {
			result <- fmt.Errorf("presentation worker: %s", boundedError(errors.New(envelope.Error)))
			return
		}
		if response != nil {
			err = json.Unmarshal(envelope.Payload, response)
		}
		result <- err
	}()
	select {
	case <-ctx.Done():
		c.reset(process)
		<-result
		return ctx.Err()
	case <-c.closed:
		c.reset(process)
		<-result
		return errors.New("presentation client is closed")
	case err := <-result:
		if err != nil {
			c.reset(process)
		}
		return err
	}
}

// Close interrupts an active exchange, releases its process, and prevents reuse.
func (c *Client) Close() error {
	c.mu.Lock()
	select {
	case <-c.closed:
		c.mu.Unlock()
		return nil
	default:
		close(c.closed)
	}
	process := c.process
	c.process = nil
	c.mu.Unlock()
	if process != nil {
		process.stop()
	}
	return nil
}
