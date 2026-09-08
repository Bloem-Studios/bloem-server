package handlers

import (
	"bytes"
	"encoding/json"
	"errors"
	"log/slog"
	"net/http"
	"strconv"
	"strings"

	apimw "github.com/Silo-Server/silo-server/internal/api/middleware"
	"github.com/Silo-Server/silo-server/internal/models"
	"github.com/Silo-Server/silo-server/internal/playback"
	"github.com/go-chi/chi/v5"
)

// The initial (executor-bound) subtitle producer.
//
// The legacy subtitle handlers deliberately refuse executor-bound sessions:
// they load a local session by bearer UUID and serve whatever file it names,
// with no signed recipe, no executor namespace and no live owner/source grant.
// A bound session's sidecar must instead be admitted exactly the way its media
// bytes are: a signed `st` reference or authenticated current bound session
// resolves the immutable recipe, the
// executor namespace must match the live session, and a serving grant from
// the attempt's owner lease is held for the whole response
// (guardNativeExecutorResponse). Only then does the byte/extraction path run.
//
// What is reused underneath: track ordinal resolution, external/embedded/
// downloaded lookup, extraction and font-bundle encoding. What is different
// from the legacy handlers: no session UUID acts as a bearer, the source file
// must be the recipe's media file or the session's requested file, a missing
// file never runs the legacy abort/finalizer (the sequenced stop/reconcile
// path owns terminal state), and every refusal is an *APIError the v2 adapter
// renders as a Problem.

// InitialSubtitleDelivery admits a bound subtitle or font request. It is the
// subtitle counterpart of PlaybackHandler.InitialPlaybackDelivery and wraps the
// executor-response guard the media routes already use.
func (h *StreamHandler) InitialSubtitleDelivery(next http.HandlerFunc) http.Handler {
	return http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		if h == nil || h.TM == nil || h.JWTSecret == "" || h.sessionMgr == nil || next == nil {
			writeError(w, http.StatusServiceUnavailable, "unavailable", "Initial playback is not configured")
			return
		}
		sessionID := chi.URLParam(r, "session_id")
		card, _ := initialMediaRecipeV3(r, h.TM, h.sessionMgr.GetSession, sessionID, h.JWTSecret)
		if card == nil || card.Executor == nil {
			writeNativeAuthorityUnavailable(w)
			return
		}
		next.ServeHTTP(w, r)
	})
}

// boundSubtitleSession resolves the session a bound subtitle request may read
// from, holding the serving grant for the response. The returned cleanup must
// run after the body is written. ok=false means a refusal was already written.
func (h *StreamHandler) boundSubtitleSession(w http.ResponseWriter, r *http.Request) (http.ResponseWriter, *http.Request, *playback.Session, *models.MediaFile, func(), bool) {
	if producer, ok := r.Context().Value(auxiliaryProducerContextKey{}).(*auxiliaryProducerContext); ok {
		return h.auxiliarySubtitleSession(w, r, producer)
	}
	userID := apimw.GetUserID(r.Context())
	if userID == 0 {
		writeError(w, http.StatusUnauthorized, "unauthorized", "Authentication required")
		return nil, nil, nil, nil, nil, false
	}
	sessionID := chi.URLParam(r, "session_id")
	if sessionID == "" {
		writeError(w, http.StatusBadRequest, "bad_request", "Session ID is required")
		return nil, nil, nil, nil, nil, false
	}
	setPlaybackSessionLogContext(r, sessionID)
	w, r, cleanup, ok := guardNativeExecutorResponse(w, r, h.TM, h.sessionMgr.GetSession, sessionID, h.JWTSecret)
	if !ok {
		return nil, nil, nil, nil, nil, false
	}
	// A refusal below is written through the grant-guarded writer while the
	// grant is still held, then the grant is released.
	refuse := func(write func()) (http.ResponseWriter, *http.Request, *playback.Session, *models.MediaFile, func(), bool) {
		write()
		cleanup()
		return nil, nil, nil, nil, nil, false
	}
	guarded, bound := r.Context().Value(nativeExecutorResponseKey{}).(*nativeExecutorResponse)
	if !bound {
		// The guard admits unbound sessions for the legacy routes; this producer
		// serves bound sessions only.
		return refuse(func() { writeNativeAuthorityUnavailable(w) })
	}
	session, status, _ := h.TM.LoadOrReconstructSessionDetail(r.Context(), h.sessionMgr.GetSession, sessionID, userID, guarded.card)
	switch status {
	case playback.SessionMissing:
		return refuse(func() { writePlaybackSessionNotFound(w) })
	case playback.SessionForbidden:
		return refuse(func() { writeError(w, http.StatusForbidden, "forbidden", "Session belongs to another user") })
	case playback.SessionLoaded:
	default:
		return refuse(func() { writeNativeAuthorityUnavailable(w) })
	}
	if !requireNativeGuardedSessionAPIEgressV3(w, r, session) {
		return refuse(func() {})
	}
	fileID, err := subtitleSourceFileID(r, session)
	if err != nil {
		return refuse(func() { writeError(w, http.StatusBadRequest, "bad_request", err.Error()) })
	}
	file, err := h.fileResolver.GetByID(r.Context(), fileID)
	if err != nil || file == nil {
		// No legacy abort: a bound session's terminal state is owned by the
		// sequenced stop and reconciliation paths.
		return refuse(func() { writeError(w, http.StatusNotFound, "not_found", "Media file not found") })
	}
	attachPlaybackSession(r.Context(), session, guarded.claims)
	return w, r, session, file, cleanup, true
}

// HandleInitialSubtitle serves one sidecar track of a bound session:
// GET/HEAD /stream/{session_id}/subtitles/{track}.
func (h *StreamHandler) HandleInitialSubtitle(w http.ResponseWriter, r *http.Request) {
	trackIndex, requestedFormat, err := playback.ParseSubtitleTrackParam(chi.URLParam(r, "track"))
	if err != nil {
		writeError(w, http.StatusBadRequest, "bad_request", "Invalid subtitle track index")
		return
	}
	// The bound session only authorizes the read: embedded extracts describe the
	// complete track, so no session position seeds the seek.
	w, r, _, file, cleanup, ok := h.boundSubtitleSession(w, r)
	if !ok {
		return
	}
	defer cleanup()

	trackIndex, err = subtitleRouteIndex(file, trackIndex, r.URL.Query())
	if err != nil {
		if errors.Is(err, errSubtitleIdentityInvalid) {
			writeError(w, http.StatusBadRequest, "bad_request", err.Error())
		} else {
			writeError(w, http.StatusNotFound, "not_found", err.Error())
		}
		return
	}

	if rawID := strings.TrimSpace(r.URL.Query().Get(playback.DownloadedSubtitleIDParamV3)); rawID != "" {
		downloadedID, parseErr := strconv.Atoi(rawID)
		if parseErr != nil || downloadedID <= 0 {
			writeError(w, http.StatusBadRequest, "bad_request", "Invalid downloaded subtitle identity")
			return
		}
		if h.SubtitleRepo == nil || h.S3Client == nil {
			writeError(w, http.StatusNotFound, "not_found", "Subtitle track not found")
			return
		}
		downloaded, lookupErr := h.SubtitleRepo.GetDownloadedSubtitle(r.Context(), downloadedID)
		if lookupErr != nil {
			slog.ErrorContext(r.Context(), "get downloaded subtitle failed", "component", "api", "file_id", file.ID, "downloaded_subtitle_id", downloadedID, "error", lookupErr)
			writeError(w, http.StatusInternalServerError, "internal_error", "Failed to load downloaded subtitle")
			return
		}
		if downloaded == nil || downloaded.MediaFileID != file.ID {
			writeError(w, http.StatusNotFound, "not_found", "Subtitle track not found")
			return
		}
		if r.Method == http.MethodHead {
			writeSubtitleRepresentationHead(w, requestedFormat)
			return
		}
		h.serveDownloadedSubtitle(w, r, *downloaded, requestedFormat)
		return
	}
	externalCount := len(file.ExternalSubtitles)
	if trackIndex < externalCount {
		sub := file.ExternalSubtitles[trackIndex]
		if !subtitleSidecarFormatSupported(sub.Format, requestedFormat, false) {
			writeError(w, http.StatusUnsupportedMediaType, "unsupported_media_type", "Requested subtitle extension does not match the selected track")
			return
		}
		if r.Method == http.MethodHead {
			writeSubtitleRepresentationHead(w, requestedFormat)
			return
		}
		if playback.IsASS(sub.Format) && requestedFormat != subtitleFormatVTTV3 {
			data, err := playback.LoadExternalSubtitleRaw(sub.Path)
			if err != nil {
				writeError(w, http.StatusInternalServerError, "internal_error", "Failed to load external subtitle")
				return
			}
			playback.ServeSubtitle(w, data, subtitleFormatASS)
			return
		}
		vttData, err := playback.LoadExternalSubtitleAsVTT(r.Context(), sub.Path, sub.Format, h.ffmpegPath())
		if err != nil {
			writeError(w, http.StatusInternalServerError, "internal_error", "Failed to load external subtitle")
			return
		}
		playback.ServeSubtitle(w, vttData, subtitleFormatVTTV3)
		return
	}
	embeddedIndex := trackIndex - externalCount
	if embeddedIndex < len(file.SubtitleTracks) {
		track := file.SubtitleTracks[embeddedIndex]
		if playback.NeedsBurnIn(track.Codec) && !playback.IsPGS(track.Codec) {
			writeError(w, http.StatusBadRequest, "bad_request", "Bitmap subtitle tracks cannot be extracted as text")
			return
		}
		if !subtitleSidecarFormatSupported(track.Codec, requestedFormat, true) {
			writeError(w, http.StatusUnsupportedMediaType, "unsupported_media_type", "Requested subtitle extension does not match the selected track")
			return
		}
		if r.Method == http.MethodHead && requestedFormat != subtitleFormatSUP {
			writeSubtitleRepresentationHead(w, requestedFormat)
			return
		}
		h.streamEmbeddedSubtitle(w, r, file, embeddedIndex, requestedFormat)
		return
	}
	if h.SubtitleRepo != nil && h.S3Client != nil {
		downloaded, err := h.SubtitleRepo.ListDownloadedSubtitles(r.Context(), file.ID)
		if err != nil {
			slog.ErrorContext(r.Context(), "list downloaded subtitles failed", "component", "api", "file_id", file.ID, "track", trackIndex, "error", err)
			writeError(w, http.StatusInternalServerError, "internal_error", "Failed to list downloaded subtitles")
			return
		}
		downloadedIndex := embeddedIndex - len(file.SubtitleTracks)
		if downloadedIndex >= 0 && downloadedIndex < len(downloaded) {
			if r.Method == http.MethodHead {
				writeSubtitleRepresentationHead(w, requestedFormat)
				return
			}
			h.serveDownloadedSubtitle(w, r, downloaded[downloadedIndex], requestedFormat)
			return
		}
	}
	writeError(w, http.StatusNotFound, "not_found", "Subtitle track not found")
}

// BoundSubtitleFontBundle resolves the attached-font bundle of a bound
// session's embedded ASS/SSA track for the typed v2 operation. It performs the
// same admission as HandleInitialSubtitle (signed executor reference, viewer
// checks, serving grant held through the final response) and returns an
// *APIError the adapter renders as a Problem. A missing file never runs the
// legacy abort/finalizer.
func (h *StreamHandler) BoundSubtitleFontBundle(w http.ResponseWriter, r *http.Request) ([]playback.SubtitleFontBundleItem, error) {
	if h == nil || h.TM == nil || h.JWTSecret == "" {
		return nil, apiError(http.StatusServiceUnavailable, "unavailable", "Initial playback is not configured")
	}
	trackIndex, _, err := playback.ParseSubtitleTrackParam(chi.URLParam(r, "track"))
	if err != nil {
		return nil, apiError(http.StatusBadRequest, "bad_request", "Invalid subtitle track index")
	}
	lifetime, ok := w.(interface {
		RetainSubtitleFontResponse(http.ResponseWriter, *http.Request, func())
	})
	if !ok {
		return nil, apiError(http.StatusServiceUnavailable, "unavailable", "Font response lifetime is not configured")
	}
	capture := &capturedRefusal{ResponseWriter: w, header: http.Header{}}
	guarded, r, session, file, cleanup, ok := h.boundSubtitleSession(capture, r)
	if !ok {
		return nil, capture.apiError()
	}
	// Transfer the existing guard to the adapter before returning any items or
	// extraction error. The adapter must write the final response through this
	// writer, then release it; returning the items alone is not completion.
	capture.forward = true
	lifetime.RetainSubtitleFontResponse(guarded, r, cleanup)
	if apimw.GetProfileID(r.Context()) != session.ProfileID {
		return nil, apiError(http.StatusForbidden, "forbidden", "Session belongs to another profile")
	}
	trackIndex, err = subtitleRouteIndex(file, trackIndex, r.URL.Query())
	if err != nil {
		if errors.Is(err, errSubtitleIdentityInvalid) {
			return nil, apiError(http.StatusBadRequest, "bad_request", err.Error())
		}
		return nil, apiError(http.StatusNotFound, "not_found", err.Error())
	}
	if err := preflightPlaybackFile(r.Context(), file, h.MissingMarker, h.EventsHub); err != nil {
		if isPlaybackFileMissing(err) {
			return nil, apiError(http.StatusNotFound, "not_found", "Source media file is missing")
		}
		return nil, apiError(http.StatusInternalServerError, "internal_error", "Failed to access source media file")
	}
	embeddedIndex := trackIndex - len(file.ExternalSubtitles)
	if embeddedIndex < 0 || embeddedIndex >= len(file.SubtitleTracks) {
		return nil, apiError(http.StatusNotFound, "not_found", "Embedded subtitle track not found")
	}
	if !playback.IsASS(file.SubtitleTracks[embeddedIndex].Codec) {
		return nil, apiError(http.StatusBadRequest, "bad_request", "Subtitle font bundles are only available for ASS/SSA tracks")
	}
	fonts, err := playback.ExtractAttachedSubtitleFonts(r.Context(), file.FilePath, h.ffmpegPath())
	if err != nil {
		slog.WarnContext(r.Context(), "subtitle font extraction failed", "component", "api", "file_id", file.ID, "track", trackIndex, "error", err)
		return nil, apiError(http.StatusInternalServerError, "font_extract_failed", "Failed to extract subtitle fonts")
	}
	return playback.EncodeSubtitleFontBundle(fonts), nil
}

// capturedRefusal records a refusal the shared admission path writes with the
// legacy envelope so a typed operation can re-raise it as an *APIError.
type capturedRefusal struct {
	http.ResponseWriter
	forward bool
	header  http.Header
	status  int
	body    bytes.Buffer
}

// Deadlines always reach the actual transport, even while admission refusals
// are captured. Once admitted, all output passes through the serving guard.
func (c *capturedRefusal) Unwrap() http.ResponseWriter { return c.ResponseWriter }
func (c *capturedRefusal) Header() http.Header {
	if c.forward {
		return c.ResponseWriter.Header()
	}
	return c.header
}
func (c *capturedRefusal) WriteHeader(status int) {
	if c.forward {
		c.ResponseWriter.WriteHeader(status)
		return
	}
	c.status = status
}
func (c *capturedRefusal) Write(b []byte) (int, error) {
	if c.forward {
		return c.ResponseWriter.Write(b)
	}
	return c.body.Write(b)
}
func (c *capturedRefusal) FlushError() error {
	if c.forward {
		return http.NewResponseController(c.ResponseWriter).Flush()
	}
	return nil
}
func (c *capturedRefusal) apiError() *APIError {
	var envelope errorResponse
	_ = json.Unmarshal(c.body.Bytes(), &envelope)
	status := c.status
	if status == 0 {
		status = http.StatusInternalServerError
	}
	if envelope.Error == "" {
		envelope.Error, envelope.Message = autoscanDeliveryInternalError, "Playback authority is temporarily unavailable"
	}
	return apiError(status, envelope.Error, envelope.Message)
}
