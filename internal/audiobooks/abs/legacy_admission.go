package abs

import (
	"context"
	"net/http"
	"strconv"

	"github.com/Silo-Server/silo-server/internal/userstore"
)

// The production progress adapter owns the exact PostgreSQL pool used for
// ABS writes. Non-PostgreSQL implementations have no first-admission source.
func (h *Handler) legacyAdmission(ctx context.Context, userID string) (context.Context, func(), error) {
	ctx = userstore.WithLegacyPlaybackWrite(ctx)
	if provider, ok := h.deps.ProgressStore.(userstore.LegacyPlaybackAdmissionProvider); ok {
		account, err := strconv.Atoi(userID)
		if err != nil || account <= 0 {
			return ctx, nil, userstore.ErrPlaybackSourceUnavailable
		}
		return provider.AcquireLegacyPlaybackAdmission(ctx, account)
	}
	return ctx, func() {}, nil
}

// Media setup is fenced, but a long byte transfer must not pin the account
// gate. Release at response publication; all earlier errors release via defer.
type admissionMediaWriter struct {
	http.ResponseWriter
	release func()
}

func (w *admissionMediaWriter) Unwrap() http.ResponseWriter { return w.ResponseWriter }
func (w *admissionMediaWriter) WriteHeader(status int) {
	w.release()
	w.ResponseWriter.WriteHeader(status)
}
func (w *admissionMediaWriter) Write(data []byte) (int, error) {
	w.release()
	return w.ResponseWriter.Write(data)
}
func (w *admissionMediaWriter) Flush() {
	w.release()
	_ = http.NewResponseController(w.ResponseWriter).Flush()
}
