package scanner

import (
	"context"
	"encoding/json"
	"errors"
	"io"
	"os/exec"

	"google.golang.org/grpc/codes"
	"google.golang.org/grpc/status"

	"github.com/Silo-Server/silo-server/internal/nativestorage"
)

// StorageAudioProbeFailure sorts a failed open or probe of one native
// audiobook track by whether the failure is the track's own, so a catalog
// scan can record that track and go on with the next book. Every cause in err
// must be one of these, or the failure is the scan's and ok is false:
//   - the provider no longer has the pinned bytes (absent, or a changed
//     revision), or the probe ran out of time: unavailable for this scan;
//   - the tool rejected the bytes, or needed more input or output than its
//     budget: invalid for that exact revision.
//
// A cancellation, killed tool or short read that comes with one of those is
// its teardown. Authority, plugin, provider-outage and host failures are
// never the track's own.
func StorageAudioProbeFailure(err error) (StorageAudioTrackStatus, bool) {
	var unavailable, invalid, other bool
	var visit func(error)
	visit = func(e error) {
		if joined, ok := e.(interface{ Unwrap() []error }); ok {
			for _, cause := range joined.Unwrap() {
				visit(cause)
			}
			return
		}
		if cause := errors.Unwrap(e); cause != nil {
			visit(cause)
			return
		}
		var exit *exec.ExitError
		var syntax *json.SyntaxError
		var typed *json.UnmarshalTypeError
		switch {
		case errors.Is(e, context.DeadlineExceeded), errors.Is(e, nativestorage.ErrToolInputBusy):
			unavailable = true
		case status.Code(e) == codes.NotFound, status.Code(e) == codes.FailedPrecondition, status.Code(e) == codes.DeadlineExceeded:
			unavailable = true
		case errors.Is(e, nativestorage.ErrToolInputBudget), errors.Is(e, ErrNativeProbeOutputBudget), errors.As(e, &syntax), errors.As(e, &typed):
			invalid = true
		case errors.As(e, &exit):
			// A tool that exited on its own judged the bytes; one killed
			// was stopped by another cause.
			invalid = invalid || exit.Exited()
		case errors.Is(e, context.Canceled), errors.Is(e, io.ErrUnexpectedEOF), status.Code(e) == codes.Canceled:
		default:
			other = true
		}
	}
	if err != nil {
		visit(err)
	}
	switch {
	case other:
		return "", false
	case unavailable:
		return StorageAudioTrackUnavailable, true
	case invalid:
		return StorageAudioTrackInvalid, true
	}
	return "", false
}
