package handlers

import (
	"bytes"
	"context"
	"net/http"
	"time"

	"github.com/Silo-Server/silo-server/internal/models"
	"github.com/Silo-Server/silo-server/internal/playback"
	"github.com/Silo-Server/silo-server/internal/storagesource"
)

func (h *StreamHandler) subtitleMetadataFiles() FilePathResolver {
	if h.NativeVideo != nil && h.NativeVideo.Files != nil {
		return nativeVideoCatalogFiles{h.NativeVideo.Files}
	}
	return h.fileResolver
}
func (h *StreamHandler) subtitleLibraryAllowsRecipe(ctx context.Context, card playback.RecipeCard) bool {
	if !storagesource.IsNativeLocation(card.InputPath) {
		return playbackLibraryAllowsSourceCtx(ctx, h.fileResolver, card.MediaFileID)
	}
	if h.NativeVideo == nil || h.NativeVideo.Files == nil {
		return false
	}
	file, err := h.NativeVideo.Files.GetByID(ctx, card.MediaFileID)
	if err != nil || file == nil || file.FilePath != card.InputPath || !playbackLibraryAllowsFileCtx(ctx, file) {
		return false
	}
	session := &playback.Session{ID: card.SessionID, UserID: card.UserID, ProfileID: card.ProfileID, MediaFileID: card.MediaFileID}
	opened, _, err := h.NativeVideo.OpenDelegatedBound(ctx, session, file)
	if err != nil {
		return false
	}
	defer opened.Close()
	return opened.Reauthorize(ctx) == nil
}
func (h *StreamHandler) subtitleFilePreflight(ctx context.Context, session *playback.Session, file *models.MediaFile) error {
	if !storagesource.IsNativeLocation(file.FilePath) {
		return preflightPlaybackFile(ctx, file, h.MissingMarker, h.EventsHub)
	}
	if h.NativeVideo == nil || session == nil {
		return nativeVideoUnavailable()
	}
	frozen := *session
	frozen.MediaFileID = file.ID
	opened, _, err := h.NativeVideo.OpenBound(ctx, &frozen, file)
	if err != nil {
		return err
	}
	defer opened.Close()
	return opened.Reauthorize(ctx)
}
func (h *StreamHandler) nativeSubtitleOpts(ctx context.Context, session *playback.Session, file *models.MediaFile) (playback.TranscodeOpts, error) {
	frozen := *session
	frozen.MediaFileID = file.ID
	source, format, err := h.NativeVideo.InputSource(ctx, &frozen, file)
	if err != nil {
		return playback.TranscodeOpts{}, err
	}
	return playback.TranscodeOpts{InputPath: file.FilePath, SessionID: session.ID, InputSource: source, InputFormat: format, FFmpegPath: h.ffmpegPath()}, nil
}

// Stop cancels extraction, but the supervised tool retains source admission
// through child and provider joins. A cleanup goroutine never releases it.
func (h *StreamHandler) nativeSubtitleContext(ctx context.Context, session *playback.Session) (context.Context, func()) {
	ctx, cancel := context.WithCancel(ctx)
	if h.NativeVideo.Sessions == nil {
		return ctx, cancel
	}
	stop, release := h.NativeVideo.Sessions.WatchTransportStop(session.ID)
	joined := make(chan struct{})
	go func() {
		defer close(joined)
		select {
		case <-stop:
			cancel()
		case <-ctx.Done():
		}
	}()
	return ctx, func() { cancel(); <-joined; release() }
}
func (h *StreamHandler) serveNativeEmbeddedSubtitle(w http.ResponseWriter, r *http.Request, session *playback.Session, file *models.MediaFile, index int, requested string) {
	ctx, finish := h.nativeSubtitleContext(r.Context(), session)
	defer finish()
	opts, err := h.nativeSubtitleOpts(ctx, session, file)
	if err != nil {
		writeAPIError(w, nativeVideoDeliveryError(err))
		return
	}
	codec := file.SubtitleTracks[index].Codec
	format := "vtt"
	var seek, duration float64
	switch {
	case playback.IsASS(codec):
		format = "ass"
	case playback.IsPGS(codec):
		format = "sup"
		_, seek, duration = playback.PGSWindowRequest(r.URL.Query())
	default:
		seek, duration = subtitleSeekPosition(r), subtitleWindowDuration(r)
	}
	if requested == "vtt" {
		format = "vtt"
	}
	data, err := playback.ExtractSourceSubtitle(ctx, opts, index, format, seek, duration)
	if err != nil {
		writeError(w, http.StatusInternalServerError, "subtitle_extract_failed", "Failed to extract subtitles")
		return
	}
	if err := h.subtitleFilePreflight(ctx, session, file); err != nil {
		writeAPIError(w, nativeVideoDeliveryError(err))
		return
	}
	w.Header().Set("Access-Control-Allow-Origin", "*")
	if format == "sup" {
		w.Header().Set("Content-Type", "application/octet-stream")
		w.Header().Set("Cache-Control", "no-store")
		http.ServeContent(w, r, "subtitle.sup", time.Time{}, bytes.NewReader(data))
		return
	}
	if format == "vtt" {
		serveSubtitleVTT(w, data)
		return
	}
	playback.ServeSubtitle(w, data, format)
}
func (h *StreamHandler) nativeSubtitleFonts(ctx context.Context, session *playback.Session, file *models.MediaFile) ([]playback.SubtitleFontAttachment, error) {
	ctx, finish := h.nativeSubtitleContext(ctx, session)
	defer finish()
	opts, err := h.nativeSubtitleOpts(ctx, session, file)
	if err != nil {
		return nil, err
	}
	fonts, err := playback.ExtractSourceSubtitleFonts(ctx, opts)
	if err != nil {
		return nil, err
	}
	if err := h.subtitleFilePreflight(ctx, session, file); err != nil {
		return nil, err
	}
	return fonts, nil
}
