//go:build linux

package scanner

import (
	"context"
	"errors"
	"fmt"
	"os"
	"path/filepath"
	"strconv"
	"strings"
	"syscall"
	"testing"
	"time"
)

// This is a configured-wrapper/general process-contract test. Ordinary
// ffprobe is not claimed to spawn pipe-owning children.
func TestNativeProbeExitedWrapperCannotLeavePipeOwnerRunning(t *testing.T) {
	pidPath := filepath.Join(t.TempDir(), "child.pid")
	t.Setenv("NATIVE_PROBE_TEST_CHILD_PID", pidPath)
	_, file, ctx, cfg := nativeProbeFixture(t, []byte("ordinary fixture bytes"))
	parent, cancelParent := context.WithCancel(ctx)
	defer cancelParent()
	tool := nativeProbeTestTool(t, "descendant")
	result := make(chan error, 1)
	go func() {
		got, err := ProbeNative(parent, tool, file, cfg)
		if got != nil {
			err = errors.Join(err, errors.New("accepted canceled wrapper output"))
		}
		result <- err
	}()
	var pids []string
	nativeProbeWaitLinuxState(t, func() bool {
		data, err := os.ReadFile(pidPath)
		if errors.Is(err, os.ErrNotExist) {
			return false
		}
		if err != nil {
			t.Fatal(err)
		}
		pids = strings.Fields(string(data))
		return len(pids) == 2
	})
	wrapper, err := strconv.Atoi(pids[0])
	if err != nil {
		t.Fatal(err)
	}
	pid, err := strconv.Atoi(pids[1])
	if err != nil {
		t.Fatal(err)
	}
	t.Cleanup(func() { _ = syscall.Kill(pid, syscall.SIGKILL) })
	// Observe direct-child exit before cancellation. A deadline measured
	// from startup can fire before a race-instrumented helper even starts.
	nativeProbeWaitLinuxState(t, func() bool {
		_, err := os.Stat(fmt.Sprintf("/proc/%d", wrapper))
		return errors.Is(err, os.ErrNotExist)
	})
	cancelParent()
	select {
	case err := <-result:
		if !errors.Is(err, context.Canceled) {
			t.Fatalf("pipe-owner cancellation lost: %v", err)
		}
	case <-time.After(5 * time.Second):
		t.Fatal("wrapper cancellation did not join")
	}
	if file.closed.Load() != 1 {
		t.Fatalf("close count %d", file.closed.Load())
	}
	nativeProbeWaitLinuxState(t, func() bool {
		stat, err := os.ReadFile(fmt.Sprintf("/proc/%d/stat", pid))
		if errors.Is(err, os.ErrNotExist) {
			return true
		}
		if err != nil {
			t.Fatal(err)
		}
		// An adopted zombie no longer executes or owns a pipe. Reaping it is
		// the adopting OS parent's responsibility, not a false child Wait.
		_, state, _ := strings.Cut(string(stat), ") ")
		return strings.HasPrefix(state, "Z")
	})
}

func nativeProbeWaitLinuxState(t *testing.T, observed func() bool) {
	t.Helper()
	deadline, cancel := context.WithTimeout(t.Context(), 5*time.Second)
	defer cancel()
	tick := time.NewTicker(time.Millisecond)
	defer tick.Stop()
	for !observed() {
		select {
		case <-tick.C:
		case <-deadline.Done():
			t.Fatal("expected process state did not arrive")
		}
	}
}
