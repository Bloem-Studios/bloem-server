package transcodenode

import (
	"context"
	"errors"
	"net/http"
	"sync"
	"time"

	"github.com/Silo-Server/silo-server/internal/playback"
)

// WithExecutorGrantProvider installs durable grant acquisition for bound worker
// execution and HTTP delivery. A namespace without this provider fails closed.
func (s *Server) WithExecutorGrantProvider(provider playback.ExecutorGrantProviderV3) *Server {
	s.executorGrants = provider
	return s
}

func (s *Server) grantExecutorResponse(w http.ResponseWriter, r *http.Request, transportID string, session *playback.TranscodeSession) (http.ResponseWriter, *http.Request, func(), error) {
	executor := session.ExecutorNamespace()
	if executor == nil {
		return w, r, func() {}, nil
	}
	if s.executorGrants == nil {
		return nil, r, nil, errors.New("executor serving grants are not configured")
	}
	grant, err := s.executorGrants(r.Context(), transportID, *executor, playback.AttemptGrantServeV3)
	if err != nil {
		return nil, r, nil, err
	}
	if grant == nil {
		return nil, r, nil, errors.New("executor serving grant is missing")
	}
	if err := grant.CheckBinding(*executor, playback.AttemptGrantServeV3, transportID); err != nil {
		grant.Close()
		return nil, r, nil, err
	}
	writer, closeWriter, err := newExecutorGrantWriter(w, r.Context(), grant)
	if err != nil {
		grant.Close()
		return nil, r, nil, err
	}
	return writer, r.WithContext(grant.Context()), closeWriter, nil
}

// executorGrantWriter never exposes a raw-write bypass. In particular it does
// not implement ReaderFrom: ServeContent must pass each chunk through Write.
// The controller's transport deadline interrupts an already blocked write;
// checking just before Write would leave that write unbounded.
type executorGrantWriter struct {
	w          http.ResponseWriter
	controller *http.ResponseController
	grant      *playback.RuntimeGrantV3
	mu         sync.Mutex
	writeErr   error
}

func newExecutorGrantWriter(w http.ResponseWriter, ctx context.Context, grant *playback.RuntimeGrantV3) (*executorGrantWriter, func(), error) {
	writer := &executorGrantWriter{w: w, controller: http.NewResponseController(w), grant: grant}
	if err := writer.SetWriteDeadline(time.Time{}); err != nil {
		return nil, nil, err
	}
	stopRequest := context.AfterFunc(ctx, grant.Close)
	canceled := make(chan struct{})
	stopGrant := context.AfterFunc(grant.Context(), func() {
		writer.mu.Lock()
		_ = writer.controller.SetWriteDeadline(time.Now())
		writer.mu.Unlock()
		close(canceled)
	})
	cleanup := func() {
		stopRequest()
		if !stopGrant() {
			<-canceled
		}
		writer.mu.Lock()
		// Every body chunk was flushed under the grant deadline. A healthy response
		// may release its deadline; an expired/disconnected response keeps it closed.
		if grant.Check() == nil {
			_ = writer.controller.SetWriteDeadline(time.Time{})
		}
		writer.mu.Unlock()
		grant.Close()
	}
	return writer, cleanup, nil
}

func (w *executorGrantWriter) Header() http.Header { return w.w.Header() }

// SetWriteDeadline also catches outer rolling-deadline wrappers: their timeout
// may shorten, but can never extend, the grant's remaining local validity.
func (w *executorGrantWriter) SetWriteDeadline(requested time.Time) error {
	w.mu.Lock()
	defer w.mu.Unlock()
	remaining, err := w.grant.Remaining()
	if err != nil {
		return err
	}
	deadline := time.Now().Add(remaining)
	if !requested.IsZero() && requested.Before(deadline) {
		deadline = requested
	}
	return w.controller.SetWriteDeadline(deadline)
}

func (w *executorGrantWriter) WriteHeader(status int) {
	if w.writeErr != nil {
		return
	}
	if err := w.SetWriteDeadline(time.Time{}); err != nil {
		w.writeErr = err
		return
	}
	w.w.WriteHeader(status)
	w.writeErr = w.controller.Flush()
}

func (w *executorGrantWriter) Write(data []byte) (int, error) {
	if w.writeErr != nil {
		return 0, w.writeErr
	}
	if err := w.SetWriteDeadline(time.Time{}); err != nil {
		return 0, err
	}
	n, err := w.w.Write(data)
	if err == nil {
		err = w.controller.Flush()
	}
	if err != nil {
		w.writeErr = err
	}
	return n, err
}

func (w *executorGrantWriter) FlushError() error {
	if w.writeErr != nil {
		return w.writeErr
	}
	if err := w.SetWriteDeadline(time.Time{}); err != nil {
		return err
	}
	w.writeErr = w.controller.Flush()
	return w.writeErr
}
