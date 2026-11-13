package playback

import (
	"context"
	"errors"
	"sync"
	"testing"
	"time"
)

// closeRaceInput is a source input that records when it is closed. Like a
// real input, it runs the child on the execution lifetime Open received.
type closeRaceInput struct {
	ctx    context.Context
	once   sync.Once
	closed chan struct{}
}

func (i *closeRaceInput) Run(consume func(context.Context, string) error) error {
	return consume(i.ctx, "pipe:0")
}

func (i *closeRaceInput) Close() error {
	i.once.Do(func() { close(i.closed) })
	return nil
}

// A session closed while a restart opens its next source input: the restart
// stops at the closing check, and the input it prepared must still be joined
// and closed, as on every other path that starts no FFmpeg.
func TestBloemCloseDuringRestartClosesPreparedInput(t *testing.T) {
	dir := t.TempDir()
	inputs := make(chan *closeRaceInput, 2)
	opening, releaseOpen := make(chan struct{}), make(chan struct{})
	opened := 0
	s := &TranscodeSession{
		outputDir: dir,
		opts: TranscodeOpts{
			SessionID:   "sess-native-close",
			FFmpegPath:  fakeFFmpegScript(t),
			OutputDir:   dir,
			InputPath:   "native-storage:close-race",
			InputFormat: "mov",
			InputSource: &TranscodeInputSource{Open: func(ctx context.Context, _ TranscodeOpts) (TranscodeInput, error) {
				opened++
				if opened == 2 {
					close(opening)
					<-releaseOpen
				}
				input := &closeRaceInput{ctx: ctx, closed: make(chan struct{})}
				inputs <- input
				return input, nil
			}},
		},
	}
	t.Cleanup(func() {
		s.StopThrottler()
		_ = s.Close()
	})
	if err := s.Restart(context.Background(), 0, 0); err != nil {
		t.Fatal(err)
	}
	<-inputs

	restarted := make(chan error, 1)
	go func() { restarted <- s.Restart(context.Background(), 10, 5) }()
	<-opening

	// Close stops the throttler first thing once it marks the session
	// closing; install one to observe that point.
	marker := NewTranscodeThrottler(s, nil, minThresholdSeconds, 2)
	s.mu.Lock()
	s.throttler = marker
	s.mu.Unlock()
	closed := make(chan error, 1)
	go func() { closed <- s.Close() }()
	<-marker.stopCh
	close(releaseOpen)

	second := <-inputs
	restartErr := <-restarted
	if err := <-closed; err != nil {
		t.Fatal(err)
	}
	if !errors.Is(restartErr, ErrSessionSuperseded) {
		t.Fatalf("restart racing Close = %v, want ErrSessionSuperseded", restartErr)
	}
	select {
	case <-second.closed:
	case <-time.After(5 * time.Second):
		t.Fatal("the restart that met a closing session left its prepared source input open")
	}
}
