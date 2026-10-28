package scanner

import (
	"context"
	"encoding/json"
	"errors"
	"fmt"
	"io"
	"os/exec"
	"strings"

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
	probeFailureCauses(err, func(e error) {
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
	})
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

// StorageAudioProbeFailureCauses names the causes at the bottom of a failed
// probe for the operator log: each one's type with its gRPC code or text.
func StorageAudioProbeFailureCauses(err error) string {
	var causes []string
	probeFailureCauses(err, func(e error) {
		if s, ok := status.FromError(e); ok {
			causes = append(causes, fmt.Sprintf("%T:%s", e, s.Code()))
			return
		}
		causes = append(causes, fmt.Sprintf("%T:%s", e, e.Error()))
	})
	return strings.Join(causes, ", ")
}

// probeFailureCauses calls visit with each cause at the bottom of err's tree.
func probeFailureCauses(err error, visit func(error)) {
	if err == nil {
		return
	}
	if joined, ok := err.(interface{ Unwrap() []error }); ok {
		for _, cause := range joined.Unwrap() {
			probeFailureCauses(cause, visit)
		}
		return
	}
	if cause := errors.Unwrap(err); cause != nil {
		probeFailureCauses(cause, visit)
		return
	}
	visit(err)
}
