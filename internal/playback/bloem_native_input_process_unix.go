//go:build unix

package playback

import (
	"context"
	"errors"
	"os"
	"os/exec"
	"syscall"
)

func configureTranscodeInputProcess(cmd *exec.Cmd) {
	cmd.SysProcAttr = &syscall.SysProcAttr{Setpgid: true}
	cmd.Cancel = func() error { return killTranscodeInputGroup(cmd) }
}

func killTranscodeInputGroup(cmd *exec.Cmd) error {
	err := syscall.Kill(-cmd.Process.Pid, syscall.SIGKILL)
	if errors.Is(err, syscall.ESRCH) {
		return os.ErrProcessDone
	}
	return err
}

func waitTranscodeInputProcess(ctx context.Context, cmd *exec.Cmd) error {
	joined := make(chan struct{})
	stop := context.AfterFunc(ctx, func() { defer close(joined); _ = killTranscodeInputGroup(cmd) })
	err := cmd.Wait()
	if !stop() {
		<-joined
	}
	cleanupErr := killTranscodeInputGroup(cmd)
	if errors.Is(cleanupErr, os.ErrProcessDone) {
		cleanupErr = nil
	}
	return errors.Join(err, cleanupErr)
}
