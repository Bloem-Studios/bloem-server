package handlers

import (
	"context"
	"errors"
	"io"
	"net/http"

	"github.com/Silo-Server/silo-server/internal/httpstream"
	"github.com/Silo-Server/silo-server/internal/playback"
	"github.com/Silo-Server/silo-server/internal/storagesource"
	"github.com/go-chi/chi/v5"
)

func (h *PlaybackHandler) serveNativeVideoSegment(w *httpstream.RollingDeadlineWriter, r *http.Request, session *playback.Session, inputPath string, lease *playback.SegmentLease, transcode *playback.TranscodeSession) bool {
	if !storagesource.IsNativeLocation(inputPath) {
		return false
	}
	// Classify by the executing generation, not the current catalog path. A
	// catalog removal or path replacement cannot turn its native artifact into
	// an ordinary filesystem response and bypass current source authority.
	if h == nil || h.NativeVideo == nil || h.NativeVideo.Files == nil || h.sessionMgr == nil || session == nil {
		writeAPIError(w, nativeVideoUnavailable())
		return true
	}
	ctx, cancel := context.WithCancel(r.Context())
	defer cancel()
	stopped, release := h.sessionMgr.WatchTransportStop(session.ID)
	defer release()
	joined := make(chan struct{})
	go func() {
		defer close(joined)
		select {
		case <-stopped:
			cancel()
		case <-ctx.Done():
		}
	}()
	defer func() { cancel(); <-joined }()
	file, err := h.NativeVideo.Files.GetByID(ctx, session.MediaFileID)
	if err == nil && (file == nil || file.FilePath != inputPath) {
		err = storagesource.ErrReferenceConflict
	}
	if err != nil {
		writeAPIError(w, nativeVideoDeliveryError(err))
		return true
	}
	opened, _, err := h.NativeVideo.OpenBound(ctx, session, file)
	if err != nil {
		writeAPIError(w, nativeVideoDeliveryError(err))
		return true
	}
	defer opened.Close()
	stopCancel := context.AfterFunc(ctx, opened.Cancel)
	defer stopCancel()
	stream := &nativeVideoArtifactReader{input: lease.File, authority: opened, ctx: ctx}
	// ServeContent may answer HEAD or a conditional request without reading or
	// seeking the representation. Check before any response headers too.
	if err := stream.reauthorize(); err != nil {
		writeAPIError(w, nativeVideoDeliveryError(err))
		return true
	}
	http.ServeContent(w, r.WithContext(ctx), lease.Info.Name(), lease.Info.ModTime(), stream)
	if stream.failure != nil || opened.Err() != nil || ctx.Err() != nil {
		// Framing can already be committed; a partial binary response must never
		// acquire a JSON error document or count as a completed generation.
		panic(http.ErrAbortHandler)
	}
	if r.Method == http.MethodGet && w.CompletedFullResponse(lease.Info.Size()) {
		if segment, err := playback.ParseSegmentNumber(chi.URLParam(r, "name")); err == nil {
			transcode.ReportSegmentDownloadedForGeneration(segment, lease.Generation)
		}
	}
	return true
}

type nativeVideoArtifactReader struct {
	input     io.ReadSeeker
	authority NativeVideoFile
	ctx       context.Context
	failure   error
}

func (s *nativeVideoArtifactReader) fail(err error) error {
	if err != nil {
		if s.failure == nil {
			s.failure = err
		}
		s.authority.Cancel()
	}
	return err
}
func (s *nativeVideoArtifactReader) reauthorize() error {
	if s.failure != nil {
		return s.failure
	}
	if err := s.ctx.Err(); err != nil {
		return s.fail(err)
	}
	return s.fail(s.authority.Reauthorize(s.ctx))
}
func (s *nativeVideoArtifactReader) Read(p []byte) (int, error) {
	if err := s.reauthorize(); err != nil {
		clear(p)
		return 0, err
	}
	n, err := s.input.Read(p)
	if denied := s.reauthorize(); denied != nil {
		clear(p)
		return 0, denied
	}
	if err != nil && !errors.Is(err, io.EOF) {
		clear(p)
		return 0, s.fail(err)
	}
	return n, err
}
func (s *nativeVideoArtifactReader) Seek(offset int64, whence int) (int64, error) {
	if err := s.reauthorize(); err != nil {
		return 0, err
	}
	pos, err := s.input.Seek(offset, whence)
	if denied := s.reauthorize(); denied != nil {
		return 0, denied
	}
	if err != nil {
		return 0, s.fail(err)
	}
	return pos, nil
}
