package storageplugin

import (
	"context"
	"errors"
	"io"
	"os"
	"os/exec"
	"strconv"

	"github.com/hashicorp/go-plugin/runner"
)

// processRunner owns the actual command, independent of go-plugin's transport
// specification. go-plugin calls Wait once and waits for it during Kill, so Done
// remains a process-reaping barrier, including negotiation/startup failures.
type processRunner struct {
	command *exec.Cmd
	stdout  io.ReadCloser
	stderr  io.ReadCloser
	pid     int
}

var _ runner.Runner = (*processRunner)(nil)

func newProcessRunner(command *exec.Cmd) (*processRunner, error) {
	stdout, err := command.StdoutPipe()
	if err != nil {
		return nil, err
	}
	stderr, err := command.StderrPipe()
	if err != nil {
		_ = stdout.Close()
		return nil, err
	}
	return &processRunner{command: command, stdout: stdout, stderr: stderr}, nil
}

func (r *processRunner) Start(ctx context.Context) error {
	if err := ctx.Err(); err != nil {
		_ = r.stdout.Close()
		_ = r.stderr.Close()
		return err
	}
	if err := r.command.Start(); err != nil {
		return err
	}
	r.pid = r.command.Process.Pid
	return nil
}
func (r *processRunner) Wait(context.Context) error { return r.command.Wait() }
func (r *processRunner) Kill(context.Context) error {
	if r.command.Process == nil {
		return nil
	}
	err := r.command.Process.Kill()
	if errors.Is(err, os.ErrProcessDone) {
		return nil
	}
	return err
}
func (r *processRunner) Stdout() io.ReadCloser { return r.stdout }
func (r *processRunner) Stderr() io.ReadCloser { return r.stderr }
func (r *processRunner) Name() string          { return r.command.Path }
func (r *processRunner) ID() string            { return strconv.Itoa(r.pid) }
func (r *processRunner) Diagnose(context.Context) string {
	return "native provider failed to start or negotiate the runtime protocol"
}
func (r *processRunner) PluginToHost(network, address string) (string, string, error) {
	return network, address, nil
}
func (r *processRunner) HostToPlugin(network, address string) (string, string, error) {
	return network, address, nil
}
