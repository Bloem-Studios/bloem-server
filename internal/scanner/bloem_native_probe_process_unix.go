//go:build unix

package scanner

import (
	"context"
	"errors"
	"os"
	"os/exec"
	"syscall"
)

// ffprobe is a trusted binary, not a script launcher. Also stop inherited pipe
// owners when a configured wrapper exits before them. CommandContext's watcher
// alone retires after the direct child exits; our hook lives through pipe join.
func runNativeProbeProcess(ctx context.Context, cmd *exec.Cmd) error {
	cmd.SysProcAttr = &syscall.SysProcAttr{Setpgid: true}
	killGroup := func() error {
		err := syscall.Kill(-cmd.Process.Pid, syscall.SIGKILL)
		if errors.Is(err, syscall.ESRCH) {
			return os.ErrProcessDone
		}
		return err
	}
	cmd.Cancel = killGroup
	if err := cmd.Start(); err != nil {
		return err
	}
	joined := make(chan struct{})
	stop := context.AfterFunc(ctx, func() { defer close(joined); _ = killGroup() })
	err := cmd.Wait()
	if !stop() {
		<-joined
	}
	// A normal parent exit may leave a descendant that does not own a pipe.
	// Clean the process group on every started-command outcome as well.
	cleanupErr := killGroup()
	if errors.Is(cleanupErr, os.ErrProcessDone) {
		cleanupErr = nil
	}
	return errors.Join(err, cleanupErr)
}
