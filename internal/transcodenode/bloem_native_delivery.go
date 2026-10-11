package transcodenode

import (
	"context"
	"net/http"
	"sync/atomic"
	"time"
)

// Native generated artifacts retain the source's exact host admission until
// the response and its cancellation callback have joined. No tool input or
// provider byte read is needed to check that admission before framing/writing.
func serveNativeExecutionDelivery(next http.Handler, w http.ResponseWriter, r *http.Request, grant *NativeExecutionGrant) {
	if grant.OpenDelivery == nil {
		rejectNativeDelivery(w, r, http.StatusServiceUnavailable)
		return
	}
	frozen := *grant
	ctx, cancel := context.WithDeadline(r.Context(), frozen.ExpiresAt)
	owner, err := frozen.OpenDelivery(ctx)
	if err != nil || owner == nil {
		cancel()
		if owner != nil {
			owner.Cancel()
			_ = owner.Close()
		}
		rejectNativeDelivery(w, r, http.StatusForbidden)
		return
	}
	guard := &nativeDeliveryResponseWriter{inner: w, ctx: ctx, expires: frozen.ExpiresAt, owner: owner}
	cancelled := make(chan struct{})
	stopCancel := context.AfterFunc(ctx, func() {
		defer close(cancelled)
		owner.Cancel()
		if guard.successFramed.Load() {
			// Expiry/client cancellation must also interrupt a socket Write that
			// is already blocked, while its source owner is still retained.
			_ = http.NewResponseController(w).SetWriteDeadline(time.Now())
		}
	})
	defer func() {
		if guard.status == 0 || nativeDeliverySuccess(guard.status) {
			if err := ctx.Err(); err != nil {
				_ = guard.fail(err)
			}
		}
		if !stopCancel() {
			<-cancelled
		}
		cancel()
		owner.Cancel()
		if err := owner.Close(); err != nil && (guard.status == 0 || nativeDeliverySuccess(guard.status)) {
			_ = guard.fail(err)
		}
		if guard.failure != nil {
			if guard.successFramed.Load() {
				// Never append an error document to a partial binary response.
				panic(http.ErrAbortHandler)
			}
			rejectNativeDelivery(w, r, http.StatusForbidden)
		}
	}()
	if err := guard.reauthorize(); err != nil {
		return
	}
	next.ServeHTTP(guard, r.WithContext(ctx))
}

func rejectNativeDelivery(w http.ResponseWriter, r *http.Request, status int) {
	for _, name := range []string{"Content-Length", "Content-Range", "ETag", "Last-Modified", "Content-Disposition", "Content-Type", "Accept-Ranges"} {
		w.Header().Del(name)
	}
	w.Header().Set("Cache-Control", "no-store, max-age=0")
	if r.Method == http.MethodHead {
		w.WriteHeader(status)
		return
	}
	http.Error(w, "native delivery authority unavailable", status)
}

func nativeDeliverySuccess(status int) bool { return status >= 200 && status < 400 }

// ReaderFrom is deliberately absent: the rolling-deadline writer must copy
// through Write instead of handing native bytes to an unchecked sendfile path.
// Unwrap preserves ResponseController deadlines; FlushError remains guarded.
type nativeDeliveryResponseWriter struct {
	inner         http.ResponseWriter
	ctx           context.Context
	expires       time.Time
	owner         NativeDeliveryAuthority
	status        int
	failure       error
	successFramed atomic.Bool
}

func (w *nativeDeliveryResponseWriter) Header() http.Header         { return w.inner.Header() }
func (w *nativeDeliveryResponseWriter) Unwrap() http.ResponseWriter { return w.inner }
func (w *nativeDeliveryResponseWriter) fail(err error) error {
	if err != nil {
		if w.failure == nil {
			w.failure = err
		}
		w.owner.Cancel()
	}
	return w.failure
}
func (w *nativeDeliveryResponseWriter) reauthorize() error {
	if w.failure != nil {
		return w.failure
	}
	if err := w.ctx.Err(); err != nil {
		return w.fail(err)
	}
	if !time.Now().Before(w.expires) {
		return w.fail(context.DeadlineExceeded)
	}
	if err := w.owner.Reauthorize(w.ctx); err != nil {
		return w.fail(err)
	}
	if err := w.ctx.Err(); err != nil {
		return w.fail(err)
	}
	if !time.Now().Before(w.expires) {
		return w.fail(context.DeadlineExceeded)
	}
	return nil
}
func (w *nativeDeliveryResponseWriter) WriteHeader(status int) {
	if w.status != 0 || w.failure != nil {
		return
	}
	if nativeDeliverySuccess(status) {
		if err := w.reauthorize(); err != nil {
			return
		}
		w.successFramed.Store(true)
		if w.ctx.Err() != nil {
			// The cancellation callback may have observed the response just
			// before this store. Preserve its socket-interruption guarantee.
			_ = http.NewResponseController(w.inner).SetWriteDeadline(time.Now())
		}
	}
	w.status = status
	w.inner.WriteHeader(status)
}
func (w *nativeDeliveryResponseWriter) Write(p []byte) (int, error) {
	if w.failure != nil {
		return 0, w.failure
	}
	if w.status == 0 {
		w.WriteHeader(http.StatusOK)
	}
	if w.failure != nil {
		return 0, w.failure
	}
	if nativeDeliverySuccess(w.status) {
		if err := w.reauthorize(); err != nil {
			return 0, err
		}
	}
	n, err := w.inner.Write(p)
	if err != nil && nativeDeliverySuccess(w.status) {
		_ = w.fail(err)
	}
	return n, err
}
func (w *nativeDeliveryResponseWriter) FlushError() error {
	if w.status == 0 {
		w.WriteHeader(http.StatusOK)
	}
	if w.failure != nil {
		return w.failure
	}
	if nativeDeliverySuccess(w.status) {
		if err := w.reauthorize(); err != nil {
			return err
		}
	}
	err := http.NewResponseController(w.inner).Flush()
	if err != nil && nativeDeliverySuccess(w.status) {
		return w.fail(err)
	}
	return err
}
func (w *nativeDeliveryResponseWriter) Flush() { _ = w.FlushError() }
