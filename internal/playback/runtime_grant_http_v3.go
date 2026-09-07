package playback

import (
	"context"
	"errors"
	"net/http"
	"sync"
	"time"
)

// GuardExecutorResponseV3 acquires a serving grant for the whole response.
// Legacy nil namespaces pass through. Bound success transfers grant ownership
// to cleanup; callers must defer it and use the returned writer and request.
// Writers without transport write deadlines fail closed. This wrapper exposes
// neither Unwrap nor ReaderFrom, so writes cannot bypass grant checks.
func GuardExecutorResponseV3(w http.ResponseWriter, r *http.Request, provider ExecutorGrantProviderV3, transportID string, executor *ExecutorNamespaceV3) (http.ResponseWriter, *http.Request, func(), error) {
	return GuardExecutorOutputV3(w, r, provider, transportID, executor, AttemptGrantServeV3)
}

// GuardExecutorOutputV3 shares write-deadline enforcement for final delivery and
// the separately authorized execution-to-egress transfer. The caller chooses
// the purpose from its role, never from an untrusted request parameter.
func GuardExecutorOutputV3(w http.ResponseWriter, r *http.Request, provider ExecutorGrantProviderV3, transportID string, executor *ExecutorNamespaceV3, purpose AttemptGrantPurposeV3) (http.ResponseWriter, *http.Request, func(), error) {
	if purpose != AttemptGrantServeV3 && purpose != AttemptGrantTransferV3 {
		return nil, r, nil, errors.New("invalid executor output purpose")
	}
	if executor == nil {
		if purpose == AttemptGrantTransferV3 {
			return nil, r, nil, errors.New("output transfer requires an executor namespace")
		}
		return w, r, func() {}, nil
	}
	if provider == nil {
		return nil, r, nil, errors.New("executor serving grants are not configured")
	}
	grant, err := provider(r.Context(), transportID, *executor, purpose)
	if err != nil {
		if grant != nil {
			grant.Close()
		}
		return nil, r, nil, err
	}
	if grant == nil {
		return nil, r, nil, errors.New("executor serving grant is missing")
	}
	if err := grant.CheckBinding(*executor, purpose, transportID); err != nil {
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
	grant      *RuntimeGrantV3
	mu         sync.Mutex
	writeErr   error
}

func newExecutorGrantWriter(w http.ResponseWriter, ctx context.Context, grant *RuntimeGrantV3) (*executorGrantWriter, func(), error) {
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
