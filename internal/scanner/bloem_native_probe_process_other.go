//go:build !unix

package scanner

import (
	"context"
	"os/exec"
)

// CommandContext retains its ordinary direct-child cancellation. The caller
// supplies a trusted ffprobe binary; it does not launch another tool process.
func runNativeProbeProcess(_ context.Context, cmd *exec.Cmd) error { return cmd.Run() }
