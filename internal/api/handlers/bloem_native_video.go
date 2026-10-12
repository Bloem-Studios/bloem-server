package handlers

import (
	"context"
	"crypto/sha256"
	"errors"
	"fmt"
	"io"
	"math"
	"net/http"
	"strings"
	"sync"
	"time"

	"github.com/Silo-Server/silo-server/internal/catalog"
	"github.com/Silo-Server/silo-server/internal/httpstream"
	"github.com/Silo-Server/silo-server/internal/mediasource"
	"github.com/Silo-Server/silo-server/internal/models"
	"github.com/Silo-Server/silo-server/internal/nativestorage"
	"github.com/Silo-Server/silo-server/internal/playback"
	"github.com/Silo-Server/silo-server/internal/storagesource"
	"github.com/Silo-Server/silo-server/internal/tenancy"
	"github.com/Silo-Server/silo-server/internal/tonemap"
)

// NativeVideoFile retains source admission and rechecks current authority.
type NativeVideoFile interface {
	mediasource.File
	Context() context.Context
	Cancel()
	Reauthorize(context.Context) error
	Err() error
	Identity() nativestorage.PlaybackIdentity
}

// NativeVideoAuthority is a bounded host attestation, never a bearer token.
// A delegated reader must recheck each fact against current host policy.
type NativeVideoAuthority struct {
	UserID                    int
	ProfileID                 string
	LoginSessionID            string
	APIKeyID                  int64
	APIKeyRevision            int64
	TokenType                 string
	AuthMethod                string
	ProfilePINRevision        int64
	ProfileCredentialRevision int64
	PINVerificationSkipped    bool
	ExpiresAt                 time.Time
	Tenant                    tenancy.Context
	OriginalMethod            string
	OriginalPath              string
	PlaybackSessionID         string
}

type NativeVideoBinding struct {
	SessionID string
	Authority NativeVideoAuthority
	UserID    int
	ProfileID string
	Identity  nativestorage.PlaybackIdentity
	Info      mediasource.Info
	Format    string
}

type NativeVideoBindings interface {
	Put(context.Context, string, NativeVideoBinding) error
	Get(context.Context, string, int) (NativeVideoBinding, error)
	RevokeSession(context.Context, string) error
}

// NativeVideoService is installed only with real viewer authorizers. Files is
// the unguarded catalog; unrelated path-only handlers keep their guards.
type NativeVideoService struct {
	AudioReady    bool
	Presentation  func(context.Context, *models.MediaFile) error
	Files         FilePathResolver
	Open          func(context.Context, *models.MediaFile) (NativeVideoFile, error)
	Delegation    func(context.Context) (NativeVideoAuthority, error)
	OpenDelegated func(context.Context, *models.MediaFile, NativeVideoBinding) (NativeVideoFile, error)
	// AuthorizeBound rechecks the sealed playback authority in addition to the
	// current request's source and credential policy on locally served reads.
	AuthorizeBound func(context.Context, NativeVideoBinding) error
	Bindings       NativeVideoBindings
	WorkerGrants   *NativeVideoWorkerGrantStore
	Sessions       SessionManagerInterface
	Deny           *playback.StreamDeny
}

func nativeVideoUnavailable() error {
	return &APIError{Status: http.StatusServiceUnavailable, Code: "unavailable", Message: "Native video source is unavailable"}
}

// NativeVideoSupported describes the container admission boundary. The normal
// planner still decides codecs, quality, and client decoding support.
func NativeVideoSupported(file *models.MediaFile) bool {
	return nativeVideoFileSupported(file) || NativeAudioSupported(file)
}

func nativeVideoFileSupported(file *models.MediaFile) bool {
	if file == nil || !storagesource.IsNativeLocation(file.FilePath) || file.IsAudioOnly() || file.BaseType == "ebook" || (file.CodecVideo == "" && len(file.VideoTracks) == 0) {
		return false
	}
	return nativeVideoFormat(file.Container) != ""
}
func nativeVideoFormat(container string) string {
	switch strings.ToLower(strings.TrimSpace(container)) {
	case "mp4", "m4v", "mov":
		return "mov"
	case "mkv", "matroska":
		return "matroska"
	case "webm":
		return "webm"
	case "avi":
		return "avi"
	case "mpeg", "mpg":
		return "mpeg"
	case "ts", "mpegts", "m2ts":
		return "mpegts"
	default:
		return ""
	}
}

type nativeVideoRequestKey struct{}
type nativeVideoRequestSnapshots struct {
	mu       sync.Mutex
	files    map[int]NativeVideoBinding
	audioPin int
}

func withNativeVideoSnapshots(ctx context.Context) context.Context {
	return context.WithValue(ctx, nativeVideoRequestKey{}, &nativeVideoRequestSnapshots{files: make(map[int]NativeVideoBinding)})
}
func nativeVideoSnapshot(ctx context.Context, fileID int) (NativeVideoBinding, bool) {
	state, ok := ctx.Value(nativeVideoRequestKey{}).(*nativeVideoRequestSnapshots)
	if !ok {
		return NativeVideoBinding{}, false
	}
	state.mu.Lock()
	defer state.mu.Unlock()
	v, ok := state.files[fileID]
	return v, ok
}
func rememberNativeVideoSnapshot(ctx context.Context, value NativeVideoBinding) error {
	state, ok := ctx.Value(nativeVideoRequestKey{}).(*nativeVideoRequestSnapshots)
	if !ok {
		return nil
	}
	state.mu.Lock()
	defer state.mu.Unlock()
	if prior, ok := state.files[value.Identity.FileID]; ok && !sameNativeVideo(prior, value) {
		return storagesource.ErrReferenceConflict
	}
	state.files[value.Identity.FileID] = value
	return nil
}

// sameNativeVideo compares the source a binding pins, not its delegation.
func sameNativeVideo(a, b NativeVideoBinding) bool {
	return a.Identity == b.Identity && sameNativeVideoInfo(a.Info, b.Info) && a.Format == b.Format
}
func (s *NativeVideoService) open(ctx context.Context, file *models.MediaFile) (NativeVideoFile, NativeVideoBinding, error) {
	if s == nil {
		return nil, NativeVideoBinding{}, nativeVideoUnavailable()
	}
	return s.openWith(ctx, file, s.Open)
}
func (s *NativeVideoService) openWith(ctx context.Context, file *models.MediaFile, open func(context.Context, *models.MediaFile) (NativeVideoFile, error)) (NativeVideoFile, NativeVideoBinding, error) {
	if open == nil || !NativeVideoSupported(file) || file.MissingSince != nil || NativeAudioSupported(file) && !s.AudioReady {
		return nil, NativeVideoBinding{}, catalog.ErrItemNotFound
	}
	opened, err := open(ctx, file)
	if err != nil {
		if opened != nil {
			_ = opened.Close()
		}
		return nil, NativeVideoBinding{}, err
	}
	if opened == nil {
		return nil, NativeVideoBinding{}, nativeVideoUnavailable()
	}
	format := nativeVideoFormat(file.Container)
	if NativeAudioSupported(file) {
		format = "audio/" + file.Container
	}
	value := normalizeNativeVideoBinding(NativeVideoBinding{Identity: opened.Identity(), Info: opened.Info(), Format: format})
	if value.Identity.FileID != file.ID || value.Identity.FilePath != file.FilePath || value.Identity.MediaFolderID != file.MediaFolderID || value.Identity.ContentID != file.ContentID || value.Info.Size < 0 || value.Info.Revision == "" || value.Info.Revision != value.Identity.Reference.Revision {
		_ = opened.Close()
		return nil, NativeVideoBinding{}, storagesource.ErrReferenceConflict
	}
	return opened, value, nil
}
func (s *NativeVideoService) Preflight(ctx context.Context, file *models.MediaFile) error {
	opened, value, err := s.open(ctx, file)
	if err != nil {
		return err
	}
	defer opened.Close()
	if err = opened.Reauthorize(ctx); err != nil {
		return err
	}
	return rememberNativeVideoSnapshot(ctx, value)
}
func (s *NativeVideoService) Bind(ctx context.Context, session *playback.Session, file *models.MediaFile) error {
	if s == nil || s.Bindings == nil || session == nil || file == nil || session.ID == "" || session.UserID <= 0 || session.MediaFileID != file.ID {
		return nativeVideoUnavailable()
	}
	opened, value, err := s.open(ctx, file)
	if err != nil {
		return err
	}
	defer opened.Close()
	if prior, ok := nativeVideoSnapshot(ctx, file.ID); ok && !sameNativeVideo(prior, value) {
		return storagesource.ErrReferenceConflict
	}
	value.UserID, value.ProfileID = session.UserID, session.ProfileID
	value.SessionID = session.ID
	if s.Delegation != nil {
		value.Authority, err = s.Delegation(ctx)
		if err != nil {
			return err
		}
		if value.Authority.UserID != session.UserID || value.Authority.ProfileID != session.ProfileID || !time.Now().Before(value.Authority.ExpiresAt) {
			return nativeVideoUnavailable()
		}
	}
	if s.Deny.Denied(ctx, session.ID) {
		return errPlaybackSessionEnded
	}
	if err := opened.Reauthorize(ctx); err != nil {
		return err
	}
	if prior, err := s.Bindings.Get(ctx, session.ID, file.ID); err == nil {
		if prior.SessionID != value.SessionID || prior.UserID != value.UserID || prior.ProfileID != value.ProfileID || !sameNativeVideo(prior, value) {
			return storagesource.ErrReferenceConflict
		}
		// A fresh request validates the same source but does not extend the
		// original execution authority or change its sealed descriptor.
		return s.Bindings.Put(ctx, session.ID, prior)
	}
	return s.Bindings.Put(ctx, session.ID, value)
}
func (s *NativeVideoService) OpenBound(ctx context.Context, session *playback.Session, file *models.MediaFile) (NativeVideoFile, NativeVideoBinding, error) {
	return s.openBound(ctx, session, file, false)
}

// OpenDelegatedBound is for host-owned execution/recovery. Its policy reloads
// the durable descriptor and current actor authority before opening a source.
func (s *NativeVideoService) OpenDelegatedBound(ctx context.Context, session *playback.Session, file *models.MediaFile) (NativeVideoFile, NativeVideoBinding, error) {
	return s.openBound(ctx, session, file, true)
}
func (s *NativeVideoService) openBound(ctx context.Context, session *playback.Session, file *models.MediaFile, delegated bool) (NativeVideoFile, NativeVideoBinding, error) {
	if s == nil || s.Bindings == nil || session == nil || file == nil || session.MediaFileID != file.ID || s.Deny.Denied(ctx, session.ID) {
		return nil, NativeVideoBinding{}, errPlaybackSessionEnded
	}
	expected, err := s.Bindings.Get(ctx, session.ID, file.ID)
	if err != nil {
		return nil, NativeVideoBinding{}, err
	}
	if expected.SessionID != session.ID || expected.UserID != session.UserID || expected.ProfileID != session.ProfileID {
		return nil, NativeVideoBinding{}, catalog.ErrItemNotFound
	}
	open := s.Open
	if delegated && s.OpenDelegated != nil {
		open = func(ctx context.Context, file *models.MediaFile) (NativeVideoFile, error) {
			return s.OpenDelegated(ctx, file, expected)
		}
	}
	opened, actual, err := s.openWith(ctx, file, open)
	if err != nil {
		return nil, NativeVideoBinding{}, err
	}
	if !sameNativeVideo(expected, actual) {
		_ = opened.Close()
		return nil, NativeVideoBinding{}, storagesource.ErrReferenceConflict
	}
	if !delegated && s.AuthorizeBound != nil {
		lifetime, cancel := context.WithCancel(opened.Context())
		guard := &nativeVideoWorkerFile{NativeVideoFile: opened, ctx: lifetime, cancel: cancel, check: func(check context.Context) error {
			return s.AuthorizeBound(check, expected)
		}}
		guard.watchCancellation()
		if err := guard.Reauthorize(ctx); err != nil {
			_ = guard.Close()
			return nil, NativeVideoBinding{}, err
		}
		opened = guard
	}
	return opened, expected, nil
}
func (s *NativeVideoService) Validate(ctx context.Context, session *playback.Session) error {
	if s == nil || s.Files == nil || session == nil {
		return nativeVideoUnavailable()
	}
	file, err := s.Files.GetByID(ctx, session.MediaFileID)
	if err != nil {
		return err
	}
	opened, _, err := s.OpenBound(ctx, session, file)
	if err != nil {
		return err
	}
	defer opened.Close()
	return opened.Reauthorize(ctx)
}
func (s *NativeVideoService) Serve(w http.ResponseWriter, r *http.Request, session *playback.Session, file *models.MediaFile) error {
	opened, binding, err := s.OpenBound(r.Context(), session, file)
	if err != nil {
		return err
	}
	// The read-ahead takes ownership of opened. Closing runs after the result
	// check below, also when it aborts: Close cancels fetches in flight.
	var owner io.Closer = opened
	defer func() { _ = owner.Close() }()
	if session.PlayMethod != playback.PlayDirect {
		return &APIError{Status: 415, Code: "unsupported", Message: "Native video requires a direct or HLS transport"}
	}
	ctx, cancel := context.WithCancel(r.Context())
	defer cancel()
	if s.Sessions != nil {
		stopped, release := s.Sessions.WatchTransportStop(session.ID)
		defer release()
		joined := make(chan struct{})
		go func() {
			defer close(joined)
			select {
			case <-stopped:
				opened.Cancel()
				cancel()
			case <-ctx.Done():
			}
		}()
		defer func() { cancel(); <-joined }()
	}
	digest := sha256.Sum256([]byte(fmt.Sprintf("%d:%d:%s", file.ID, binding.Info.Size, binding.Info.Revision)))
	w.Header().Set("ETag", fmt.Sprintf("\"%x\"", digest))
	w.Header().Set("X-Content-Type-Options", "nosniff")
	contentType := nativeVideoContentType(file.Container)
	name := "video." + file.Container
	if NativeAudioSupported(file) {
		contentType = nativeAudioContentType(file.Container)
		name = "audio." + file.Container
	}
	w.Header().Set("Content-Type", contentType)
	reader, err := nativestorage.NewReadAhead(ctx, opened, nativeVideoDirectReadAhead)
	if err != nil {
		return err
	}
	owner = reader
	// This check also opens the first authorization window for the reads.
	if err := reader.Reauthorize(ctx); err != nil {
		return err
	}
	if r.Header.Get("If-Range") == "" {
		reader.SetRangeEnd(nativestorage.RangeEnd(r.Header.Get("Range"), binding.Info.Size))
	}
	stream := &nativeEbookStream{File: reader}
	http.ServeContent(httpstream.NewRollingDeadlineWriter(w), r.WithContext(ctx), name, binding.Info.ModifiedAt, stream)
	if stream.failure != nil || reader.Err() != nil || opened.Err() != nil || ctx.Err() != nil {
		panic(http.ErrAbortHandler)
	}
	return nil
}

// Native direct play reads 1 MiB provider ranges on aligned block boundaries,
// three ahead of a sequential client, instead of one provider read per 32 KiB
// copy. Memory is at most 5 MiB per response. Delivery stops at the next read
// after a session stop or source failure, and within one second of a
// revocation that only a fresh authority check observes (see
// nativestorage.ReadAheadConfig).
var nativeVideoDirectReadAhead = nativestorage.ReadAheadConfig{BlockBytes: 1 << 20, CacheBlocks: 5, PrefetchBlocks: 3, AuthorizationWindow: time.Second}

// FFmpeg reads progressive remux and HLS input through the tool bridge. One
// read-ahead serves every HTTP range of one execution; the bridge copies 1 MiB
// chunks and admits four concurrent ranges, so an FFmpeg seek that reconnects
// before the previous range drains does not exhaust its slots.
var nativeVideoToolReadAhead = nativestorage.ReadAheadConfig{BlockBytes: 1 << 20, CacheBlocks: 6, PrefetchBlocks: 3, AuthorizationWindow: time.Second}

const (
	nativeVideoToolChunkBytes = 1 << 20
	nativeVideoToolReads      = 4
)

func nativeVideoContentType(container string) string {
	switch nativeVideoFormat(container) {
	case "mov":
		return "video/mp4"
	case "webm":
		return "video/webm"
	case "matroska":
		return "video/x-matroska"
	case "avi":
		return "video/x-msvideo"
	case "mpegts":
		return "video/mp2t"
	default:
		return "video/mpeg"
	}
}

// InputSource reopens the frozen binding for every execution and restart.
// Only the opaque catalog identity remains in TranscodeOpts and RecipeCard.
func (s *NativeVideoService) InputSource(ctx context.Context, session *playback.Session, file *models.MediaFile) (*playback.TranscodeInputSource, string, error) {
	if NativeAudioSupported(file) {
		return nil, "", &APIError{Status: 415, Code: "unsupported", Message: "Remote audiobook tracks play only as original files"}
	}
	if s == nil || s.Bindings == nil || session == nil || file == nil {
		return nil, "", nativeVideoUnavailable()
	}
	binding, err := s.Bindings.Get(ctx, session.ID, file.ID)
	if err != nil {
		return nil, "", err
	}
	if binding.SessionID != session.ID || binding.UserID != session.UserID || binding.ProfileID != session.ProfileID {
		return nil, "", catalog.ErrItemNotFound
	}
	frozenSession, frozenFile := *session, *file
	source := &playback.TranscodeInputSource{Open: func(execution context.Context, opts playback.TranscodeOpts) (playback.TranscodeInput, error) {
		if opts.InputPath != binding.Identity.FilePath || opts.SessionID != frozenSession.ID {
			return nil, storagesource.ErrReferenceConflict
		}
		opened, actual, err := s.OpenDelegatedBound(execution, &frozenSession, &frozenFile)
		if err != nil {
			return nil, err
		}
		if !sameNativeVideoBinding(actual, binding) {
			_ = opened.Close()
			return nil, storagesource.ErrReferenceConflict
		}
		if binding.Info.Size > math.MaxInt64/8 {
			_ = opened.Close()
			return nil, nativestorage.ErrToolInputBudget
		}
		reader, err := nativestorage.NewReadAhead(opened.Context(), opened, nativeVideoToolReadAhead)
		if err != nil {
			_ = opened.Close()
			return nil, err
		}
		// The tool input owns the reader, which owns opened. CancelSource
		// still cancels opened, failing any read-ahead fetch in flight.
		input, err := nativestorage.NewToolInput(opened.Context(), reader, nativestorage.ToolInputConfig{CancelSource: opened.Cancel, Reauthorize: func(check context.Context) error {
			if s.Deny.Denied(check, frozenSession.ID) {
				return errPlaybackSessionEnded
			}
			return opened.Reauthorize(check)
		}, MaxBytes: max(int64(1<<30), binding.Info.Size*8), ChunkBytes: nativeVideoToolChunkBytes, MaxConcurrentReads: nativeVideoToolReads, Timeout: 12 * time.Hour})
		if err != nil {
			_ = reader.Close()
		}
		return input, err
	}}
	source.Validate = func(check context.Context, opts playback.TranscodeOpts) error {
		if s.Files == nil || opts.InputPath != binding.Identity.FilePath || opts.ToneMapSourceRevision.MediaFileID != frozenFile.ID {
			return storagesource.ErrReferenceConflict
		}
		current, err := s.Files.GetByID(check, frozenFile.ID)
		if err != nil {
			return err
		}
		if current == nil || tonemap.RevisionForFile(current) != opts.ToneMapSourceRevision {
			return tonemap.ErrSourceRevisionChanged
		}
		opened, actual, err := s.OpenDelegatedBound(check, &frozenSession, current)
		if err != nil {
			return err
		}
		defer opened.Close()
		if !sameNativeVideoBinding(actual, binding) {
			return storagesource.ErrReferenceConflict
		}
		if err = opened.Reauthorize(check); err != nil {
			return err
		}
		if err = playback.ValidateSourceToneMap(check, opts); err != nil {
			return err
		}
		current, err = s.Files.GetByID(check, frozenFile.ID)
		if err != nil {
			return err
		}
		if current == nil || tonemap.RevisionForFile(current) != opts.ToneMapSourceRevision {
			return tonemap.ErrSourceRevisionChanged
		}
		return opened.Reauthorize(check)
	}
	return source, binding.Format, nil
}

// ResolveNativeVideoFile is used only by the installed per-open viewer policy;
// path-only routes keep the default guarded resolver.
func (h *PlaybackHandler) ResolveNativeVideoFile(ctx context.Context, id int) (*models.MediaFile, error) {
	if h == nil || h.NativeVideo == nil || h.NativeVideo.Files == nil {
		return nil, catalog.ErrItemNotFound
	}
	r := (&http.Request{Header: make(http.Header)}).WithContext(ctx)
	file, err := h.loadAuthorizedFileFrom(r, h.NativeVideo.Files, id)
	if err == nil && !NativeVideoSupported(file) {
		return nil, catalog.ErrItemNotFound
	}
	if err == nil && NativeAudioSupported(file) {
		if !h.NativeVideo.AudioReady || h.NativeVideo.Presentation == nil {
			return nil, catalog.ErrItemNotFound
		}
		if err := h.NativeVideo.Presentation(ctx, file); err != nil {
			return nil, err
		}
	}
	return file, err
}
func (h *PlaybackHandler) nativeVideoPreflight(r *http.Request, file *models.MediaFile) error {
	if err := pinNativeAudioRequest(r.Context(), file); err != nil {
		return err
	}
	if NativeAudioSupported(file) {
		if h.NativeVideo == nil || !h.NativeVideo.AudioReady || h.NativeVideo.Presentation == nil {
			return &playbackFilePreflightError{notFound: true}
		}
		if err := h.NativeVideo.Presentation(r.Context(), file); err != nil {
			return &playbackFilePreflightError{notFound: errors.Is(err, storagesource.ErrReferenceConflict) || errors.Is(err, catalog.ErrItemNotFound), err: err}
		}
	}
	if file != nil && storagesource.IsNativeLocation(file.FilePath) {
		if h.NativeVideo == nil {
			return catalog.ErrItemNotFound
		}
		return h.NativeVideo.Preflight(r.Context(), file)
	}
	return preflightPlaybackFile(r.Context(), file, h.MissingMarker, h.EventsHub)
}
func nativeVideoDeliveryError(err error) error {
	if errors.Is(err, storagesource.ErrReferenceConflict) {
		return &APIError{Status: 409, Code: "source_revision_changed", Message: "Native video revision changed; start a new playback attempt"}
	}
	if errors.Is(err, catalog.ErrItemNotFound) {
		return &APIError{Status: 404, Code: "not_found", Message: "Media file not found"}
	}
	return nativeVideoUnavailable()
}

var _ io.ReadSeeker = NativeVideoFile(nil)
