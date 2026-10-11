//go:build !unix

package playback

import (
	"context"
	"os/exec"
)

func configureTranscodeInputProcess(_ *exec.Cmd)                       {}
func waitTranscodeInputProcess(_ context.Context, cmd *exec.Cmd) error { return cmd.Wait() }
