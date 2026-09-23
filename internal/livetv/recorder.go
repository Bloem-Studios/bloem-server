package livetv

import (
	"context"
	"errors"
	"fmt"
	"io"
	"log/slog"
	"os"
	"path/filepath"
	"regexp"
	"sort"
	"strconv"
	"strings"
	"sync"
	"time"
	"unicode"

	"github.com/google/uuid"

	"github.com/Silo-Server/silo-server/internal/playback"
)

// DVR ownership design
//
// The DVR tick runs on every API replica, so the database, not the process,
// decides who records. Every transition is a compare-and-swap against a claim
// (recordingClaimStore):
//
//   - A replica claims a row with ClaimRecording: claim_token := fresh UUID,
//     lease_until := now()+recordingLeaseTTL, only if the row is still in the
//     expected status and holds no live lease. Only the winner goes on.
//   - Before FFmpeg starts, the winner reserves a tuner index in
//     livetv_sessions (the same ledger and unique index live tunes use), so
//     live viewers and recordings count against one tuner capacity.
//   - MarkRecordingStarted flips scheduled -> recording (or records a resumed
//     segment) only while the claim is still ours and the row is still
//     scheduled/recording. A user cancel in between makes it lose, and the
//     process is stopped. All later writes (lease renewals, release, finish)
//     are conditioned the same way, so a cancelled row is never resurrected.
//   - The owner renews the lease and touches its tuner session every
//     recordingLeaseRenew. A renewal that no longer matches (cancelled,
//     finished, or taken over after our lease lapsed) stops the local FFmpeg.
//   - A start failure releases the claim with last_error and start_attempts+1
//     and leaves the row scheduled; the next tick retries until stop_at, and
//     only then is it marked failed.
//   - A `recording` row whose lease expired has no live owner (the process
//     died or restarted). Any replica claims it; before stop_at it resumes
//     into a new segment file (<base>.partN.ts) and the row is flagged
//     interrupted. At stop_at the segments present locally are concatenated
//     into the base file (MPEG-TS concatenates byte-wise) and the row is
//     completed with a "partial" note, or failed if nothing was captured. A
//     row with a live lease belongs to another running replica: leave it.

// ErrRecorderClosed reports that the application has begun recorder shutdown.
var ErrRecorderClosed = errors.New("livetv recorder is closed")

const (
	recorderStoreTimeout = 5 * time.Second
	// recordingLeaseTTL is how long a recording survives without its owner
	// renewing; after that another replica resumes it.
	recordingLeaseTTL = StaleSessionTTL
	// recordingLeaseRenew keeps the lease and the tuner session well inside
	// their TTLs.
	recordingLeaseRenew = recordingLeaseTTL / 4
	// interruptedRecordingNote marks a completed recording that is missing
	// the portion recorded while no replica owned it.
	interruptedRecordingNote = "recording was interrupted; the file is partial"
)

var errRecordingClaimLost = errors.New("recording claim lost")

// recordingClaim is this process's hold on one recording row.
type recordingClaim struct {
	token          string
	tunerSessionID string
	path           string // base output path
	stop           time.Time
	interrupted    bool
}

// Recorder runs FFmpeg copy-recordings for due Live TV schedules.
type Recorder struct {
	service    *Service
	root       string
	ffmpegPath string
	leaseTTL   time.Duration
	leaseRenew time.Duration

	mu       sync.Mutex
	active   map[string]*playback.LiveRecordSession
	claims   map[string]*recordingClaim
	starting map[string]struct{}
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
		leaseTTL:        recordingLeaseTTL,
		leaseRenew:      recordingLeaseRenew,
		active:          map[string]*playback.LiveRecordSession{},
		claims:          map[string]*recordingClaim{},
		starting:        map[string]struct{}{},
		lifecycleCtx:    lifecycleCtx,
		lifecycleCancel: lifecycleCancel,
		done:            make(chan struct{}),
	}
}

func (r *Recorder) claimStore() (recordingClaimStore, error) {
	store, ok := r.service.store.(recordingClaimStore)
	if !ok {
		return nil, errRecordingClaimsUnsupported
	}
	return store, nil
}

// Process starts or resumes due recordings and finishes elapsed ones.
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
	store, err := r.claimStore()
	if err != nil {
		return 0, 0, 0, err
	}
	now := r.service.now()

	var recordings []Recording
	for _, status := range []string{"scheduled", "recording"} {
		rows, listErr := r.service.store.ListRecordings(ctx, status)
		if listErr != nil {
			return 0, 0, 0, listErr
		}
		recordings = append(recordings, rows...)
	}

	count := func(ok bool) {
		if ok {
			completed++
		} else {
			failed++
		}
	}
	for i := range recordings {
		rec := recordings[i]
		if r.ownsLocally(rec.ID) {
			if rec.Status == "recording" && !rec.Stop.After(now) {
				if claim := r.stopLocal(ctx, rec.ID); claim != nil {
					count(r.finalize(ctx, store, rec.ID, claim.token, claim.path, claim.interrupted || rec.Interrupted))
				}
			}
			continue
		}
		if rec.Status == "scheduled" && rec.Start.After(now) {
			continue
		}
		claimed, claimErr := r.claim(ctx, store, rec.ID, rec.Status)
		if claimErr != nil {
			return started, completed, failed, claimErr
		}
		if claimed == nil {
			continue // another replica owns it, or it was cancelled
		}
		// A previous owner's tuner reservation is dead with its lease.
		r.releaseTuner(ctx, claimed.TunerSessionID)
		if !claimed.Stop.After(now) {
			if claimed.Status == "scheduled" {
				msg := "recording window elapsed before recorder could start"
				if claimed.LastError != "" {
					msg += ": " + claimed.LastError
				}
				if _, finErr := store.FinishRecordingClaim(ctx, claimed.ID, claimed.ClaimToken, "failed", "", msg); finErr != nil {
					return started, completed, failed, finErr
				}
				failed++
				continue
			}
			count(r.finalize(ctx, store, claimed.ID, claimed.ClaimToken, r.basePath(claimed), true))
			continue
		}
		if startErr := r.startRecording(ctx, store, claimed); startErr != nil {
			if errors.Is(startErr, ErrRecorderClosed) || errors.Is(startErr, context.Canceled) || errors.Is(startErr, context.DeadlineExceeded) {
				return started, completed, failed, startErr
			}
			if !errors.Is(startErr, errRecordingClaimLost) {
				slog.WarnContext(ctx, "livetv dvr start failed; will retry",
					"recording_id", rec.ID, "error", startErr)
			}
			continue
		}
		started++
	}

	r.reapStale(ctx)
	return started, completed, failed, nil
}

func (r *Recorder) claim(ctx context.Context, store recordingClaimStore, id, status string) (*Recording, error) {
	return store.ClaimRecording(ctx, id, status, uuid.NewString(), r.service.ownerNodeID, r.leaseTTL)
}

func (r *Recorder) ownsLocally(id string) bool {
	r.mu.Lock()
	defer r.mu.Unlock()
	_, owned := r.claims[id]
	_, starting := r.starting[id]
	return owned || starting
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

func (r *Recorder) basePath(rec *Recording) string {
	if rec.Path != "" {
		return rec.Path
	}
	return r.outputPath(rec)
}

// segmentPath names segment n of a recording; segment 0 is the base file.
func segmentPath(base string, n int) string {
	if n <= 0 {
		return base
	}
	return fmt.Sprintf("%s.part%d%s", strings.TrimSuffix(base, filepath.Ext(base)), n, filepath.Ext(base))
}

// startRecording starts (or resumes) a claimed recording. On failure the claim
// is released with the error so the row stays retryable until stop_at.
func (r *Recorder) startRecording(ctx context.Context, store recordingClaimStore, rec *Recording) (err error) {
	r.mu.Lock()
	if r.closed {
		r.mu.Unlock()
		return ErrRecorderClosed
	}
	if _, exists := r.starting[rec.ID]; exists {
		r.mu.Unlock()
		return errRecordingClaimLost
	}
	r.starting[rec.ID] = struct{}{}
	r.wg.Add(1)
	lifecycleCtx := r.lifecycleCtx
	r.mu.Unlock()
	handedToWatcher := false
	var tunerSessionID string
	defer func() {
		if handedToWatcher {
			return
		}
		r.mu.Lock()
		delete(r.starting, rec.ID)
		r.mu.Unlock()
		r.wg.Done()
		if err == nil || errors.Is(err, errRecordingClaimLost) {
			return
		}
		storeCtx, cancelStore := context.WithTimeout(context.WithoutCancel(ctx), recorderStoreTimeout)
		defer cancelStore()
		r.releaseTuner(storeCtx, tunerSessionID)
		msg := err.Error()
		if errors.Is(err, ErrRecorderClosed) {
			msg = "recorder shut down before the recording started"
		}
		if _, relErr := store.ReleaseRecordingClaim(storeCtx, rec.ID, rec.ClaimToken, msg); relErr != nil {
			slog.WarnContext(ctx, "livetv dvr release claim failed", "recording_id", rec.ID, "error", relErr)
		}
	}()

	ch, err := r.service.store.GetChannel(ctx, rec.ChannelID)
	if err != nil {
		return err
	}
	if ch == nil || strings.TrimSpace(ch.StreamURL) == "" {
		return fmt.Errorf("channel stream unavailable")
	}
	open := r.service.xtreamChannelOpener(ch)
	if open == nil {
		if err := ValidateMediaFetchURL(ch.StreamURL); err != nil {
			return err
		}
	}
	if err := lifecycleCtx.Err(); err != nil {
		return ErrRecorderClosed
	}
	tunerSession, err := r.service.claimRecordingTuner(ctx, rec, ch)
	if err != nil {
		return err
	}
	tunerSessionID = tunerSession.ID

	basePath := r.basePath(rec)
	sess, err := playback.StartLiveRecord(lifecycleCtx, playback.LiveRecordOpts{
		ID:         rec.ID,
		InputURL:   ch.StreamURL,
		OpenMPEGTS: open,
		OutputPath: segmentPath(basePath, rec.Segments),
		FFmpegPath: r.ffmpegPath,
		StopAt:     rec.Stop.Add(15 * time.Second),
	})
	if err != nil {
		return err
	}

	storeCtx, cancelStore := context.WithTimeout(lifecycleCtx, recorderStoreTimeout)
	ok, err := store.MarkRecordingStarted(storeCtx, rec.ID, rec.ClaimToken, basePath, tunerSessionID, r.leaseTTL)
	cancelStore()
	if err == nil && !ok {
		err = errRecordingClaimLost
	}
	if err != nil {
		_ = sess.Close()
		if errors.Is(err, errRecordingClaimLost) {
			// Cancelled (or otherwise finished) between claim and start.
			r.releaseTuner(context.WithoutCancel(ctx), tunerSessionID)
			return err
		}
		if lifecycleCtx.Err() != nil {
			return ErrRecorderClosed
		}
		return err
	}

	claim := &recordingClaim{
		token:          rec.ClaimToken,
		tunerSessionID: tunerSessionID,
		path:           basePath,
		stop:           rec.Stop,
		interrupted:    rec.Interrupted || rec.Segments > 0,
	}
	r.mu.Lock()
	if r.closed {
		r.mu.Unlock()
		_ = sess.Close()
		return ErrRecorderClosed
	}
	delete(r.starting, rec.ID)
	r.active[rec.ID] = sess
	r.claims[rec.ID] = claim
	r.mu.Unlock()

	handedToWatcher = true
	go r.watch(store, rec.ID, sess, claim)

	slog.InfoContext(ctx, "livetv dvr recording started",
		"recording_id", rec.ID, "path", segmentPath(basePath, rec.Segments), "segment", rec.Segments)
	return nil
}

// watch renews the owner's lease and tuner session while FFmpeg runs and
// settles the row when FFmpeg exits on its own.
func (r *Recorder) watch(store recordingClaimStore, id string, sess *playback.LiveRecordSession, claim *recordingClaim) {
	defer r.wg.Done()
	ticker := time.NewTicker(r.leaseRenew)
	defer ticker.Stop()
	for running := true; running; {
		select {
		case <-sess.Done():
			running = false
		case <-ticker.C:
			ctx, cancel := context.WithTimeout(r.lifecycleCtx, recorderStoreTimeout)
			ok, err := store.RenewRecordingLease(ctx, id, claim.token, r.leaseTTL)
			if err == nil && ok {
				_ = r.service.store.TouchSession(ctx, claim.tunerSessionID)
			}
			cancel()
			if err == nil && !ok {
				// Cancelled, finished, or taken over: this process no longer owns it.
				slog.InfoContext(r.lifecycleCtx, "livetv dvr recording claim lost; stopping", "recording_id", id)
				r.stopLocal(context.Background(), id)
				return
			}
			if err != nil {
				slog.WarnContext(r.lifecycleCtx, "livetv dvr lease renew failed", "recording_id", id, "error", err)
			}
		}
	}

	r.mu.Lock()
	mine := r.claims[id] == claim
	if mine {
		delete(r.claims, id)
	}
	if r.active[id] == sess {
		delete(r.active, id)
	}
	closed := r.closed
	r.mu.Unlock()
	if !mine {
		return // stopLocal owns the settlement
	}

	ctx, cancel := context.WithTimeout(context.Background(), recorderStoreTimeout)
	defer cancel()
	r.releaseTuner(ctx, claim.tunerSessionID)
	switch {
	case closed:
		// Graceful shutdown: hand the recording to any surviving replica now
		// instead of waiting out the lease.
		_, _ = store.ReleaseRecordingClaim(ctx, id, claim.token, "recorder shut down mid-recording; resuming")
	case r.service.now().Before(claim.stop):
		msg := "recorder stopped unexpectedly; resuming"
		if sess.Err() != nil {
			msg = sess.Err().Error() + "; resuming"
		}
		_, _ = store.ReleaseRecordingClaim(ctx, id, claim.token, msg)
	default:
		r.finalize(ctx, store, id, claim.token, claim.path, claim.interrupted)
	}
}

// stopLocal stops this process's FFmpeg for id and frees its tuner without
// touching the recording row. It returns the claim it held, if any.
func (r *Recorder) stopLocal(ctx context.Context, id string) *recordingClaim {
	r.mu.Lock()
	sess := r.active[id]
	claim := r.claims[id]
	delete(r.claims, id)
	delete(r.active, id)
	r.mu.Unlock()
	if sess != nil {
		_ = sess.Close()
	}
	if claim != nil {
		r.releaseTuner(context.WithoutCancel(ctx), claim.tunerSessionID)
	}
	return claim
}

func (r *Recorder) releaseTuner(ctx context.Context, sessionID string) {
	if sessionID == "" {
		return
	}
	ctx, cancel := context.WithTimeout(ctx, recorderStoreTimeout)
	defer cancel()
	if _, err := r.service.store.ReleaseSession(ctx, sessionID); err != nil {
		slog.WarnContext(ctx, "livetv dvr tuner release failed", "session_id", sessionID, "error", err)
	}
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
		for _, session := range r.active {
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

// finalize merges the recording's segments and moves the claimed row to
// completed (true) or failed (false).
func (r *Recorder) finalize(ctx context.Context, store recordingClaimStore, id, token, basePath string, interrupted bool) bool {
	size, mergeErr := mergeSegments(basePath)
	status, lastError := "completed", ""
	if interrupted {
		lastError = interruptedRecordingNote
	}
	if mergeErr != nil {
		status, lastError = "failed", mergeErr.Error()
	}
	ok, err := store.FinishRecordingClaim(ctx, id, token, status, basePath, lastError)
	if err != nil || !ok {
		slog.WarnContext(ctx, "livetv dvr finish not recorded",
			"recording_id", id, "status", status, "claim_held", ok, "error", err)
		return false
	}
	if status == "failed" {
		slog.WarnContext(ctx, "livetv dvr recording failed", "recording_id", id, "error", lastError)
		return false
	}
	slog.InfoContext(ctx, "livetv dvr recording completed",
		"recording_id", id, "path", basePath, "bytes", size, "interrupted", interrupted)
	return true
}

// mergeSegments appends every <base>.partN file present on this node to the
// base file (promoting the first part when the base is absent) and returns
// the resulting size. An absent or empty result is an error.
func mergeSegments(basePath string) (int64, error) {
	if basePath == "" {
		return 0, errors.New("recording path unknown")
	}
	pattern := strings.TrimSuffix(basePath, filepath.Ext(basePath)) + ".part*" + filepath.Ext(basePath)
	parts, _ := filepath.Glob(pattern)
	sort.Slice(parts, func(i, j int) bool { return partIndex(parts[i]) < partIndex(parts[j]) })
	for _, part := range parts {
		if err := appendFile(basePath, part); err != nil {
			return 0, err
		}
		_ = os.Remove(part)
	}
	info, err := os.Stat(basePath)
	if err != nil {
		return 0, err
	}
	if info.Size() == 0 {
		return 0, errors.New("recording file empty")
	}
	return info.Size(), nil
}

func partIndex(path string) int {
	name := strings.TrimSuffix(path, filepath.Ext(path))
	i := strings.LastIndex(name, ".part")
	n, _ := strconv.Atoi(name[i+len(".part"):])
	return n
}

func appendFile(dst, src string) error {
	in, err := os.Open(src)
	if err != nil {
		return err
	}
	defer in.Close()
	out, err := os.OpenFile(dst, os.O_CREATE|os.O_WRONLY|os.O_APPEND, 0o644)
	if err != nil {
		return err
	}
	if _, err := io.Copy(out, in); err != nil {
		_ = out.Close()
		return err
	}
	return out.Close()
}

// reapStale stops local recordings whose row was cancelled, finished, or
// claimed by someone else since the last lease renewal.
func (r *Recorder) reapStale(ctx context.Context) {
	r.mu.Lock()
	owned := make(map[string]string, len(r.claims))
	for id, claim := range r.claims {
		owned[id] = claim.token
	}
	r.mu.Unlock()
	for id, token := range owned {
		rec, err := r.service.store.GetRecording(ctx, id)
		if err != nil {
			continue
		}
		if rec == nil || rec.Status != "recording" || rec.ClaimToken != token {
			r.stopLocal(ctx, id)
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
