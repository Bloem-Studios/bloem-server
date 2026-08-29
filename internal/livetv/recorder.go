package livetv

import (
	"context"
	"errors"
	"fmt"
	"log/slog"
	"os"
	"path/filepath"
	"regexp"
	"strings"
	"sync"
	"time"
	"unicode"

	"github.com/Silo-Server/silo-server/internal/playback"
)

// ErrRecorderClosed reports that the application has begun recorder shutdown.
var ErrRecorderClosed = errors.New("livetv recorder is closed")

const recorderStoreTimeout = 5 * time.Second

// Recorder runs FFmpeg copy-recordings for due Live TV schedules.
type Recorder struct {
	service    *Service
	root       string
	ffmpegPath string

	mu       sync.Mutex
	active   map[string]*playback.LiveRecordSession
	starting map[string]struct{}
	stopping map[string]struct{}
	closed   bool

	lifecycleCtx    context.Context
	lifecycleCancel context.CancelFunc
	wg              sync.WaitGroup
	closeOnce       sync.Once
	done            chan struct{}
}

// NewRecorder creates a DVR recorder writing under root.
func NewRecorder(service *Service, root, ffmpegPath string) *Recorder {
	if root == "" {
		root = filepath.Join(os.TempDir(), "bloem-dvr")
	}
	lifecycleCtx, lifecycleCancel := context.WithCancel(context.Background())
	return &Recorder{
		service:         service,
		root:            root,
		ffmpegPath:      ffmpegPath,
		active:          map[string]*playback.LiveRecordSession{},
		starting:        map[string]struct{}{},
		stopping:        map[string]struct{}{},
		lifecycleCtx:    lifecycleCtx,
		lifecycleCancel: lifecycleCancel,
		done:            make(chan struct{}),
	}
}

// Process starts due recordings, finishes elapsed ones, and fails orphans.
func (r *Recorder) Process(ctx context.Context) (started, completed, failed int, err error) {
	if r == nil || r.service == nil {
		return 0, 0, 0, ErrNotConfigured
	}
	processCtx, cancel, err := r.processContext(ctx)
	if err != nil {
		return 0, 0, 0, err
	}
	defer cancel()
	ctx = processCtx
	now := r.service.now()

	recordings, err := r.service.store.ListRecordings(ctx, "")
	if err != nil {
		return 0, 0, 0, err
	}

	for i := range recordings {
		rec := recordings[i]
		switch rec.Status {
		case "scheduled":
			if !rec.Start.After(now) && rec.Stop.After(now) {
				if startErr := r.startRecording(ctx, &rec); startErr != nil {
					if errors.Is(startErr, ErrRecorderClosed) || errors.Is(startErr, context.Canceled) || errors.Is(startErr, context.DeadlineExceeded) {
						return started, completed, failed, startErr
					}
					slog.WarnContext(ctx, "livetv dvr start failed",
						"recording_id", rec.ID, "error", startErr)
					rec.Status = "failed"
					rec.LastError = startErr.Error()
					if _, updErr := r.service.store.UpdateRecording(ctx, &rec); updErr != nil {
						return started, completed, failed, updErr
					}
					failed++
					continue
				}
				started++
			} else if !rec.Stop.After(now) {
				rec.Status = "failed"
				rec.LastError = "recording window elapsed before recorder could start"
				if _, updErr := r.service.store.UpdateRecording(ctx, &rec); updErr != nil {
					return started, completed, failed, updErr
				}
				failed++
			}
		case "recording":
			if !rec.Stop.After(now) {
				if finishErr := r.finishRecording(ctx, &rec, false); finishErr != nil {
					slog.WarnContext(ctx, "livetv dvr finish failed",
						"recording_id", rec.ID, "error", finishErr)
					failed++
					continue
				}
				completed++
			}
		}
	}

	// Cancelled/failed while still holding an ffmpeg process.
	r.reapStale(ctx)
	return started, completed, failed, nil
}

func (r *Recorder) processContext(taskCtx context.Context) (context.Context, context.CancelFunc, error) {
	r.mu.Lock()
	if r.closed {
		r.mu.Unlock()
		return nil, nil, ErrRecorderClosed
	}
	lifecycleCtx := r.lifecycleCtx
	r.mu.Unlock()

	ctx, cancel := context.WithCancel(taskCtx)
	stopLifecycleCancel := context.AfterFunc(lifecycleCtx, cancel)
	return ctx, func() {
		stopLifecycleCancel()
		cancel()
	}, nil
}

func (r *Recorder) startRecording(ctx context.Context, rec *Recording) error {
	r.mu.Lock()
	if r.closed {
		r.mu.Unlock()
		return ErrRecorderClosed
	}
	if _, exists := r.active[rec.ID]; exists {
		r.mu.Unlock()
		return nil
	}
	if _, exists := r.starting[rec.ID]; exists {
		r.mu.Unlock()
		return nil
	}
	r.starting[rec.ID] = struct{}{}
	r.wg.Add(1)
	lifecycleCtx := r.lifecycleCtx
	r.mu.Unlock()
	handedToWatcher := false
	defer func() {
		if handedToWatcher {
			return
		}
		r.mu.Lock()
		delete(r.starting, rec.ID)
		r.mu.Unlock()
		r.wg.Done()
	}()

	ch, err := r.service.store.GetChannel(ctx, rec.ChannelID)
	if err != nil {
		return err
	}
	if ch == nil || strings.TrimSpace(ch.StreamURL) == "" {
		return fmt.Errorf("channel stream unavailable")
	}
	if err := ValidateMediaFetchURL(ch.StreamURL); err != nil {
		return err
	}
	if err := lifecycleCtx.Err(); err != nil {
		return ErrRecorderClosed
	}

	outPath := r.outputPath(rec)
	sess, err := playback.StartLiveRecord(lifecycleCtx, playback.LiveRecordOpts{
		ID:         rec.ID,
		InputURL:   ch.StreamURL,
		OutputPath: outPath,
		FFmpegPath: r.ffmpegPath,
		StopAt:     rec.Stop.Add(15 * time.Second),
	})
	if err != nil {
		return err
	}

	r.mu.Lock()
	if r.closed {
		r.mu.Unlock()
		_ = sess.Close()
		return ErrRecorderClosed
	}
	delete(r.starting, rec.ID)
	r.active[rec.ID] = sess
	r.mu.Unlock()

	rec.Status = "recording"
	rec.Path = outPath
	rec.LastError = ""
	storeCtx, cancelStore := context.WithTimeout(lifecycleCtx, recorderStoreTimeout)
	_, err = r.service.store.UpdateRecording(storeCtx, rec)
	cancelStore()
	if err != nil {
		_ = sess.Close()
		r.mu.Lock()
		delete(r.active, rec.ID)
		r.mu.Unlock()
		if lifecycleCtx.Err() != nil {
			return ErrRecorderClosed
		}
		return err
	}

	handedToWatcher = true
	go func(id string) {
		defer r.wg.Done()
		<-sess.Done()
		r.mu.Lock()
		_, stopping := r.stopping[id]
		closed := r.closed
		if cur := r.active[id]; cur == sess {
			delete(r.active, id)
		}
		r.mu.Unlock()
		if stopping || closed {
			return
		}
		// Process exited early — mark failed on next tick via status still recording
		// with missing active handle, or update immediately.
		storeCtx, cancelStore := context.WithTimeout(r.lifecycleCtx, recorderStoreTimeout)
		defer cancelStore()
		existing, getErr := r.service.store.GetRecording(storeCtx, id)
		if getErr != nil || existing == nil || existing.Status != "recording" {
			return
		}
		if sess.Err() != nil && time.Now().Before(existing.Stop) {
			existing.Status = "failed"
			existing.LastError = sess.Err().Error()
			_, _ = r.service.store.UpdateRecording(storeCtx, existing)
		}
	}(rec.ID)

	slog.InfoContext(ctx, "livetv dvr recording started",
		"recording_id", rec.ID, "path", outPath)
	return nil
}

// Close stops all active recording processes and waits for recorder-owned work.
// It is safe to call more than once; each caller's context bounds only its wait.
func (r *Recorder) Close(ctx context.Context) error {
	if r == nil {
		return nil
	}
	r.closeOnce.Do(func() {
		r.mu.Lock()
		r.closed = true
		r.lifecycleCancel()
		sessions := make([]*playback.LiveRecordSession, 0, len(r.active))
		for id, session := range r.active {
			r.stopping[id] = struct{}{}
			sessions = append(sessions, session)
		}
		r.mu.Unlock()

		for _, session := range sessions {
			go func() { _ = session.Close() }()
		}
		go func() {
			r.wg.Wait()
			close(r.done)
		}()
	})

	select {
	case <-r.done:
		return nil
	case <-ctx.Done():
		return ctx.Err()
	}
}

func (r *Recorder) finishRecording(ctx context.Context, rec *Recording, cancel bool) error {
	r.mu.Lock()
	sess := r.active[rec.ID]
	r.stopping[rec.ID] = struct{}{}
	r.mu.Unlock()
	defer func() {
		r.mu.Lock()
		delete(r.stopping, rec.ID)
		delete(r.active, rec.ID)
		r.mu.Unlock()
	}()

	if sess != nil {
		_ = sess.Close()
	}

	path := rec.Path
	if path == "" && sess != nil {
		path = sess.OutputPath
	}
	rec.Path = path

	if cancel {
		rec.Status = "cancelled"
		_, err := r.service.store.UpdateRecording(ctx, rec)
		return err
	}

	info, statErr := os.Stat(path)
	if statErr != nil || info.Size() == 0 {
		rec.Status = "failed"
		if statErr != nil {
			rec.LastError = statErr.Error()
		} else {
			rec.LastError = "recording file empty"
		}
		if _, err := r.service.store.UpdateRecording(ctx, rec); err != nil {
			return err
		}
		return fmt.Errorf("recording file missing or empty")
	}

	rec.Status = "completed"
	rec.LastError = ""
	_, err := r.service.store.UpdateRecording(ctx, rec)
	if err != nil {
		return err
	}
	slog.InfoContext(ctx, "livetv dvr recording completed",
		"recording_id", rec.ID, "path", path, "bytes", info.Size())
	return nil
}

func (r *Recorder) reapStale(ctx context.Context) {
	r.mu.Lock()
	ids := make([]string, 0, len(r.active))
	for id := range r.active {
		ids = append(ids, id)
	}
	r.mu.Unlock()
	for _, id := range ids {
		rec, err := r.service.store.GetRecording(ctx, id)
		if err != nil || rec == nil {
			continue
		}
		if rec.Status == "cancelled" || rec.Status == "failed" || rec.Status == "completed" {
			_ = r.finishRecording(ctx, rec, rec.Status == "cancelled")
		}
	}
}

func (r *Recorder) outputPath(rec *Recording) string {
	day := rec.Start.UTC().Format("2006-01-02")
	stamp := rec.Start.UTC().Format("150405")
	title := sanitizeFilename(rec.Title)
	if title == "" {
		title = "recording"
	}
	name := fmt.Sprintf("%s_%s_%s.ts", title, stamp, shortID(rec.ID))
	return filepath.Join(r.root, day, name)
}

var nonFileChars = regexp.MustCompile(`[^A-Za-z0-9._-]+`)

func sanitizeFilename(name string) string {
	name = strings.TrimSpace(name)
	name = strings.Map(func(r rune) rune {
		if unicode.IsControl(r) {
			return -1
		}
		return r
	}, name)
	name = nonFileChars.ReplaceAllString(name, "_")
	name = strings.Trim(name, "._-")
	if len(name) > 80 {
		name = name[:80]
	}
	return name
}

func shortID(id string) string {
	id = strings.TrimSpace(id)
	if len(id) <= 8 {
		return id
	}
	return id[len(id)-8:]
}
