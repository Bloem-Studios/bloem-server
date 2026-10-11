//go:build linux

package playback_test

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

	"github.com/Silo-Server/silo-server/internal/playback"
)

func waitNativeProcessState(t *testing.T, observed func() bool) {
	t.Helper()
	deadline, cancel := context.WithTimeout(t.Context(), 5*time.Second)
	defer cancel()
	tick := time.NewTicker(time.Millisecond)
	defer tick.Stop()
	for !observed() {
		select {
		case <-tick.C:
		case <-deadline.Done():
			t.Fatal("expected native process state did not arrive")
		}
	}
}

// A configured wrapper that exits before a pipe-owning descendant must not
// release the source while that descendant can still use the private input.
func TestNativeInputExitedWrapperCancellationJoinsDescendant(t *testing.T) {
	opts, _, released := nativeInputFixture(t)
	pidPath := filepath.Join(t.TempDir(), "pids")
	tool := filepath.Join(t.TempDir(), "wrapper")
	script := "#!/bin/sh\nsleep 60 &\nchild=$!\nprintf '%s %s' \"$$\" \"$child\" > '" + pidPath + "'\nexit 0\n"
	if err := os.WriteFile(tool, []byte(script), 0700); err != nil {
		t.Fatal(err)
	}
	opts.FFmpegPath = tool
	session, err := playback.StartTranscode(context.Background(), opts)
	if err != nil {
		t.Fatal(err)
	}
	t.Cleanup(func() { _ = session.Close() })
	var pids []string
	waitNativeProcessState(t, func() bool {
		data, err := os.ReadFile(pidPath)
		if err != nil {
			return false
		}
		pids = strings.Fields(string(data))
		return len(pids) == 2
	})
	wrapper, err := strconv.Atoi(pids[0])
	if err != nil {
		t.Fatal(err)
	}
	child, err := strconv.Atoi(pids[1])
	if err != nil {
		t.Fatal(err)
	}
	t.Cleanup(func() { _ = syscall.Kill(child, syscall.SIGKILL) })
	waitNativeProcessState(t, func() bool {
		_, err := os.Stat(fmt.Sprintf("/proc/%d", wrapper))
		return errors.Is(err, os.ErrNotExist)
	})
	if released.Load() != 0 {
		t.Fatal("exited wrapper released a live pipe owner")
	}
	if err := session.CloseProcess(); err != nil {
		t.Fatal(err)
	}
	if released.Load() != 1 {
		t.Fatal("cancel did not join input")
	}
	waitNativeProcessState(t, func() bool {
		stat, err := os.ReadFile(fmt.Sprintf("/proc/%d/stat", child))
		if errors.Is(err, os.ErrNotExist) {
			return true
		}
		if err != nil {
			t.Fatal(err)
		}
		_, state, _ := strings.Cut(string(stat), ") ")
		return strings.HasPrefix(state, "Z") // adopted zombies no longer execute or hold pipes
	})
}
