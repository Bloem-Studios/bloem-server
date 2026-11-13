package playback_test

import (
	"bytes"
	"context"
	"log"
	"strings"
	"sync"
	"testing"

	"github.com/Silo-Server/silo-server/internal/playback"
)

type lockedLogBuffer struct {
	mu  sync.Mutex
	buf bytes.Buffer
}

func (b *lockedLogBuffer) Write(p []byte) (int, error) {
	b.mu.Lock()
	defer b.mu.Unlock()
	return b.buf.Write(p)
}

func (b *lockedLogBuffer) String() string {
	b.mu.Lock()
	defer b.mu.Unlock()
	return b.buf.String()
}

// The restart logs the FFmpeg command it runs. For a native source that
// command holds the source input's private argument, so the log must name the
// catalog file instead.
func TestNativeInputRestartLogNamesCatalogFileNotPrivateArgument(t *testing.T) {
	opts, _, _ := nativeInputFixture(t)
	session, err := playback.StartTranscode(context.Background(), opts)
	if err != nil {
		t.Fatal(err)
	}
	t.Cleanup(func() { _ = session.Close() })
	waitNativeInput(t, session)

	logs := &lockedLogBuffer{}
	previous, flags := log.Writer(), log.Flags()
	log.SetOutput(logs)
	t.Cleanup(func() { log.SetOutput(previous); log.SetFlags(flags) })

	if err := session.Restart(context.Background(), 1, 1); err != nil {
		t.Fatal(err)
	}
	waitNativeInput(t, session)
	if err := session.WaitError(); err != nil {
		t.Fatal(err)
	}

	var restartLine string
	for _, line := range strings.Split(logs.String(), "\n") {
		if strings.Contains(line, "ffmpeg restart cmd") {
			restartLine = line
		}
	}
	if !strings.Contains(restartLine, opts.InputPath) {
		t.Fatalf("restart log does not name the catalog file %q: %q", opts.InputPath, restartLine)
	}
	if strings.Contains(logs.String(), "http://") {
		t.Fatalf("restart log carries the private source argument: %q", logs.String())
	}
}
