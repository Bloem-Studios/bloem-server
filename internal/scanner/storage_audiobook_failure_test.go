package scanner

import (
	"context"
	"encoding/json"
	"errors"
	"fmt"
	"io"
	"os/exec"
	"testing"

	"google.golang.org/grpc/codes"
	"google.golang.org/grpc/status"

	"github.com/Silo-Server/silo-server/internal/nativestorage"
	"github.com/Silo-Server/silo-server/internal/storagesource"
)

func TestStorageAudioProbeFailureKeepsTheScanForTrackFailuresOnly(t *testing.T) {
	exited := exec.CommandContext(t.Context(), "sh", "-c", "exit 1").Run()
	cmd := exec.CommandContext(t.Context(), "sleep", "10")
	if err := cmd.Start(); err != nil {
		t.Fatal(err)
	}
	_ = cmd.Process.Kill()
	killed := cmd.Wait()
	var exit *exec.ExitError
	if !errors.As(exited, &exit) || !exit.Exited() || !errors.As(killed, &exit) || exit.Exited() {
		t.Fatalf("exits %v %v", exited, killed)
	}
	var syntax error = new(json.SyntaxError)
	probe := func(errs ...error) error { return &nativeProbeFailure{errors.Join(errs...)} }
	absent := status.Error(codes.NotFound, "entry absent")
	for name, tc := range map[string]struct {
		err    error
		status StorageAudioTrackStatus
	}{
		"absent bytes":        {probe(exited, fmt.Errorf("storage stream incomplete: %w", io.ErrUnexpectedEOF), absent, context.Canceled), StorageAudioTrackUnavailable},
		"absent at open":      {fmt.Errorf("open: %w", absent), StorageAudioTrackUnavailable},
		"changed revision":    {probe(status.Error(codes.FailedPrecondition, "revision changed")), StorageAudioTrackUnavailable},
		"timed out":           {probe(killed, context.DeadlineExceeded), StorageAudioTrackUnavailable},
		"input budget":        {probe(exited, nativestorage.ErrToolInputBudget), StorageAudioTrackInvalid},
		"output budget":       {probe(killed, ErrNativeProbeOutputBudget), StorageAudioTrackInvalid},
		"rejected bytes":      {probe(exited), StorageAudioTrackInvalid},
		"unparsable output":   {probe(syntax), StorageAudioTrackInvalid},
		"killed alone":        {probe(killed), ""},
		"canceled alone":      {probe(context.Canceled), ""},
		"provider down":       {probe(exited, status.Error(codes.Unavailable, "down")), ""},
		"denied":              {probe(status.Error(codes.PermissionDenied, "denied")), ""},
		"authority lost":      {probe(absent, storagesource.ErrSourceUnavailable), ""},
		"invalid plugin data": {probe(exited, errors.New("invalid storage stream chunk")), ""},
		"none":                {nil, ""},
	} {
		got, ok := StorageAudioProbeFailure(tc.err)
		if got != tc.status || ok != (tc.status != "") {
			t.Errorf("%s: got %q %v, want %q", name, got, ok, tc.status)
		}
	}
}
