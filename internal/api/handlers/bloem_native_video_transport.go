package handlers

import (
	"context"
	"errors"
	"math"
	"net/http"
	"strconv"

	"github.com/Silo-Server/silo-server/internal/catalog"
	"github.com/Silo-Server/silo-server/internal/config"
	"github.com/Silo-Server/silo-server/internal/models"
	"github.com/Silo-Server/silo-server/internal/noderouting"
	"github.com/Silo-Server/silo-server/internal/playback"
	"github.com/Silo-Server/silo-server/internal/storagesource"
	"github.com/Silo-Server/silo-server/internal/streamtoken"
)

type nativeVideoCatalogFiles struct{ FilePathResolver }

func (f nativeVideoCatalogFiles) GetByID(ctx context.Context, id int) (*models.MediaFile, error) {
	file, err := f.FilePathResolver.GetByID(ctx, id)
	if err == nil && file != nil && storagesource.IsNativeLocation(file.FilePath) && !NativeVideoSupported(file) {
		return nil, catalog.ErrItemNotFound
	}
	return file, err
}
func (h *PlaybackHandler) playbackMetadataFiles() FilePathResolver {
	if h.NativeVideo != nil && h.NativeVideo.Files != nil {
		return nativeVideoCatalogFiles{h.NativeVideo.Files}
	}
	return h.fileResolver
}
func (h *PlaybackHandler) nativeVideoLibraryAllowsSession(r *http.Request, session *playback.Session) bool {
	if h.NativeVideo != nil && h.NativeVideo.Files != nil {
		file, err := h.NativeVideo.Files.GetByID(r.Context(), session.MediaFileID)
		if err != nil {
			return false
		}
		if file != nil && storagesource.IsNativeLocation(file.FilePath) {
			return NativeVideoSupported(file) && h.NativeVideo.Validate(r.Context(), session) == nil
		}
	}
	return playbackLibraryAllowsSource(r, h.fileResolver, session.MediaFileID)
}
func (h *StreamHandler) serveNativeVideoSession(w http.ResponseWriter, r *http.Request, session *playback.Session, claims *streamtoken.Claims) bool {
	if h.NativeVideo == nil || h.NativeVideo.Files == nil {
		return false
	}
	file, err := h.NativeVideo.Files.GetByID(r.Context(), session.MediaFileID)
	if err != nil || file == nil || !storagesource.IsNativeLocation(file.FilePath) {
		return false
	}
	attachPlaybackSession(r.Context(), session, claims)
	if err = h.NativeVideo.Validate(r.Context(), session); err == nil {
		if err = h.sessionMgr.BeginTransport(session.ID); err == nil {
			defer func() { _ = h.sessionMgr.EndTransport(session.ID) }()
			switch session.PlayMethod {
			case playback.PlayDirect:
				err = h.NativeVideo.Serve(w, r, session, file)
			case playback.PlayRemux:
				err = h.serveNativeVideoRemux(w, r, session, file)
			default:
				err = &APIError{Status: 400, Code: "bad_request", Message: "HLS playback uses the manifest endpoint"}
			}
		}
	}
	if err != nil {
		writeAPIError(w, nativeVideoDeliveryError(err))
	}
	return true
}
func (h *StreamHandler) serveNativeVideoRemux(w http.ResponseWriter, r *http.Request, session *playback.Session, file *models.MediaFile) error {
	source, format, err := h.NativeVideo.InputSource(r.Context(), session, file)
	if err != nil {
		return err
	}
	seek := 0.0
	if value := r.URL.Query().Get("seek"); value != "" {
		seek, err = strconv.ParseFloat(value, 64)
		if err != nil || seek < 0 || math.IsNaN(seek) || math.IsInf(seek, 0) {
			return &APIError{Status: 400, Code: "bad_request", Message: "Invalid seek position"}
		}
	}
	if r.Method == http.MethodHead {
		opened, _, err := h.NativeVideo.OpenDelegatedBound(r.Context(), session, file)
		if err != nil {
			return err
		}
		// Validate fresh source authority and join the admitted file owner.
		// HEAD never starts FFmpeg or reads the movie's bytes.
		if err := errors.Join(opened.Reauthorize(r.Context()), opened.Close()); err != nil {
			return err
		}
		w.Header().Set("Content-Type", "video/mp4")
		w.Header().Set("Cache-Control", "no-store, max-age=0")
		w.WriteHeader(http.StatusOK)
		return nil
	}
	abort, release := h.sessionMgr.WatchTransportStop(session.ID)
	defer release()
	return playback.ServeSourceRemux(w, r, playback.TranscodeOpts{InputPath: file.FilePath, InputSource: source, InputFormat: format, SessionID: session.ID, FFmpegPath: h.ffmpegPath(), AudioTrackIndex: session.AudioTrackIndex, SeekSeconds: seek}, "mp4", session.TranscodeAudio, file.PrimaryDVProfile(), playback.RemuxServeOptions{DVMode: session.RemuxDVMode, FFmpegPath: h.ffmpegPath(), DropResumeLeadingPictures: session.RemuxResumeLeadingPictureDrop, ContentType: playback.RemuxContentType(false), AudioOnly: false, Abort: abort, SourceAudioChannels: session.SourceAudioChannels, TargetAudioChannels: session.TargetAudioChannels, TargetAudioBitrateKbps: session.TargetAudioBitrateKbps, CopyAudioCodec: session.TargetAudioCodec})
}

// A native worker stop must revoke shared authority before the session can
// disappear; recovery deliberately permits an absent in-memory session.
func (h *PlaybackHandler) revokeRequiredNativeVideoAuthority(ctx context.Context, session *playback.Session) error {
	if h == nil || h.NativeVideo == nil || session == nil {
		return nil
	}
	revokeCtx, cancel := context.WithTimeout(context.WithoutCancel(ctx), nodeRecipeWriteTimeoutV3)
	defer cancel()
	// A session can keep native recovery authority after its current file or
	// catalog path changes. Consult host-owned history before current metadata.
	required := false
	if history, ok := h.NativeVideo.Bindings.(nativeVideoBindingHistory); ok {
		var err error
		required, err = history.HasSession(revokeCtx, session.ID)
		if err != nil {
			return err
		}
	}
	if !required && h.NativeVideo.Files != nil {
		file, err := h.NativeVideo.Files.GetByID(ctx, session.MediaFileID)
		if err != nil {
			return err
		}
		required = file != nil && storagesource.IsNativeLocation(file.FilePath)
	}
	if !required {
		return nil
	}
	if h.NativeVideo.Bindings == nil {
		return nativeVideoUnavailable()
	}
	if err := h.NativeVideo.Bindings.RevokeSession(revokeCtx, session.ID); err != nil {
		return err
	}
	if session.TranscodeTransportID == "" {
		return nil
	}
	if !h.NativeVideo.WorkerGrants.Enabled() {
		return nativeVideoUnavailable()
	}
	return h.NativeVideo.WorkerGrants.RevokeTransport(revokeCtx, session.TranscodeTransportID)
}

func (h *PlaybackHandler) prepareNativeVideoTransportV3(r *http.Request, session *playback.Session, file *models.MediaFile, result playback.PlannerResultV3, mode mediaAuthModeV3, policy config.PlaybackRoutingPolicy) (preparedTransportV3, *transportErrorV3) {
	if err := nativeAudioPlanRefusal(file, result); err != nil {
		return preparedTransportV3{}, err
	}
	if result.Plan == nil {
		return preparedTransportV3{}, &transportErrorV3{reason: "internal_error", message: "Native video plan is missing."}
	}
	if admissionErr := h.nativeVideoPredecessorAdmissionV3(r.Context(), session, result); admissionErr != nil {
		return preparedTransportV3{}, admissionErr
	}
	routeSession := *session
	routeSession.MediaFileID = file.ID
	if err := h.NativeVideo.Bind(r.Context(), &routeSession, file); err != nil {
		return preparedTransportV3{}, &transportErrorV3{reason: "source_revision_changed", message: "Native video revision is unavailable; start a new attempt.", cause: err}
	}
	workload := routingWorkloadV3(result)
	// Until a worker supplies typed input authority, native sources remain on a
	// legal integrated route. Worker-only/proxy-only policy fails closed.
	if workload == noderouting.WorkloadDirectPlay && policy.DirectPlayEgress == config.PlaybackEgressProxyOnly || workload == noderouting.WorkloadRemux && (policy.RemuxExecution == config.PlaybackExecutionWorkerOnly || policy.RemuxEgress == config.PlaybackEgressProxyOnly) || workload == noderouting.WorkloadVideoTranscode && (policy.VideoTranscodeExecution == config.PlaybackExecutionWorkerOnly || policy.VideoTranscodeEgress == config.PlaybackEgressProxyOnly) {
		return preparedTransportV3{}, &transportErrorV3{reason: routeCapabilityUnavailableReasonV3, message: "No available route has native source authority.", retryable: true}
	}
	timeline, timelineErr := h.nativeVideoTimelineV3(r.Context(), &routeSession, file, result)
	if timelineErr != nil {
		return preparedTransportV3{}, timelineErr
	}
	if result.Plan.Delivery == playback.DeliveryTranscodeHLSV3 || result.Plan.Delivery == playback.DeliveryRemuxHLSV3 {
		return h.prepareLocalTransportV3(r, &routeSession, file, result, timeline, mode)
	}
	unlock := h.tm.LockSessionLifecycle(session.ID)
	if result.Plan.SessionID != "" {
		if result.Plan.SessionID != session.ID {
			unlock()
			return preparedTransportV3{}, &transportErrorV3{reason: "internal_error", message: "The playback plan does not belong to the session being prepared."}
		}
		current, err := h.sessionMgr.GetSession(session.ID)
		if err != nil {
			unlock()
			return preparedTransportV3{}, &transportErrorV3{reason: "session_expired", message: "The playback session ended before its route could be prepared.", retryable: true, cause: err}
		}
		routeSession = *current
		routeSession.MediaFileID = file.ID
	}
	// Recheck the live route under the lifecycle lock: the caller's snapshot
	// may predate a remote route assignment. Stop/start owns worker revocation.
	if admissionErr := h.nativeVideoPredecessorAdmissionV3(r.Context(), &routeSession, result); admissionErr != nil {
		unlock()
		return preparedTransportV3{}, admissionErr
	}
	routeSession.TranscodeNodeURL = ""
	routeSession.TranscodeTransportID = ""
	routeSession.RoutingExecutionNodeID = 0
	routeSession.RoutingExecutionNodeURL = ""
	routeSession.RoutingEgressNodeID = 0
	routeSession.RoutingEgressNodeURL = ""
	routeSession.PlayMethod = result.PlayMethod
	routeSession.BasePlayMethod = result.PlayMethod
	routeSession.RequireMediaAuthorization = mode.headerAuth
	routeSession.TranscodeAudio = result.TranscodeAudio
	routeSession.TargetAudioCodec = result.TargetAudioCodec
	routeSession.TargetAudioChannels = result.TargetAudioChannels
	routeSession.TargetAudioBitrateKbps = result.TargetAudioBitrateKbps
	routeSession.SourceAudioChannels = result.SourceAudioChannels
	routeSession.AudioTrackIndex = plannedAudioTrackIndexV3(result, session.AudioTrackIndex)
	routeSession.RemuxDVMode = remuxDVModeForPlanV3(result.Plan)
	routeSession.RemuxResumeLeadingPictureDrop = result.RemuxResumeLeadingPictureDrop
	routeSession.RoutingWorkload = string(workload)
	routeSession.RoutingExecution = string(noderouting.ExecutionNone)
	routeSession.RoutingEgress = string(noderouting.EgressAPI)
	if workload == noderouting.WorkloadRemux {
		routeSession.RoutingExecution = string(noderouting.ExecutionAPI)
	}
	card := identityRecipeCard(&routeSession)
	card.InputPath = file.FilePath
	card.DVProfile = file.PrimaryDVProfile()
	url := "/stream/" + session.ID
	url = appendStreamToken(url, h.signSessionToken(card, mode.headerAuth))
	if result.PlayMethod == playback.PlayRemux && timeline.seekSeconds > 0 {
		url = appendPlaybackQueryV3(url, "seek", strconv.FormatFloat(timeline.seekSeconds, 'f', -1, 64))
	}
	completed := false
	return preparedTransportV3{url: url, routingWorkload: workload, routingExecution: noderouting.Execution(routeSession.RoutingExecution), routingEgress: noderouting.EgressAPI, commit: func() *transportErrorV3 {
		if completed {
			return nil
		}
		completed = true
		h.tm.CloseTranscodeSession(session.ID, "")
		if releaser, ok := h.NodePlanner.(sessionReservationReleaserV3); ok {
			releaser.ReleaseSession(session.ID)
		}
		h.deleteProxyGrantV3(r.Context(), session.ID)
		h.applyRemoteTransportMarkV3(r.Context(), session.ID, false)
		unlock()
		return nil
	}, rollback: func() {
		if !completed {
			completed = true
			unlock()
		}
	}}, nil
}
func (h *PlaybackHandler) nativeVideoTimelineV3(ctx context.Context, session *playback.Session, file *models.MediaFile, result playback.PlannerResultV3) (preparedTimelineV3, *transportErrorV3) {
	if err := nativeAudioPlanRefusal(file, result); err != nil {
		return preparedTimelineV3{}, err
	}
	if result.Plan.Delivery != playback.DeliveryRemuxProgressiveV3 && result.Plan.Delivery != playback.DeliveryRemuxHLSV3 {
		return h.prepareTransportTimelineV3(ctx, session, file, result)
	}
	requested := result.Plan.Timeline.SourceStartSeconds
	origin, segment := 0.0, 0
	if requested > 0 {
		source, format, err := h.NativeVideo.InputSource(ctx, session, file)
		if err != nil {
			return preparedTimelineV3{}, &transportErrorV3{reason: "source_unavailable", message: "Native video seek source is unavailable.", cause: err}
		}
		probeSource := &playback.TranscodeInputSource{Open: func(ctx context.Context, opts playback.TranscodeOpts) (playback.TranscodeInput, error) {
			opts.SessionID = session.ID
			return source.Open(ctx, opts)
		}}
		origin, segment, err = playback.ResolveSourceCopySeekAnchor(ctx, h.playbackConfig().FFmpegPath, file.FilePath, probeSource, format, requested, playback.DefaultSegmentDuration)
		if err != nil {
			return preparedTimelineV3{}, &transportErrorV3{reason: "transcode_start_failed", message: "Failed to resolve native video seek position.", cause: err}
		}
	}
	configureCopyRemuxTimelineV3(result.Plan, origin)
	return preparedTimelineV3{seekSeconds: requested, streamOriginSeconds: origin, startSegmentNumber: segment, copySeekAnchorResolved: true}, nil
}

// InstallNativeVideoRecovery binds fresh request policy and the durable pinned
// descriptor before the manager can reconstruct a native child.
func (h *PlaybackHandler) InstallNativeVideoRecovery() {
	if h == nil || h.tm == nil {
		return
	}
	previousAuthorization := h.tm.AuthorizeSessionReconstruction
	h.tm.AuthorizeSessionReconstruction = func(ctx context.Context, card playback.RecipeCard) error {
		if previousAuthorization != nil {
			if err := previousAuthorization(ctx, card); err != nil {
				return err
			}
		}
		if !storagesource.IsNativeLocation(card.InputPath) {
			return nil
		}
		if h.NativeVideo == nil || h.NativeVideo.Files == nil {
			return nativeVideoUnavailable()
		}
		file, err := h.NativeVideo.Files.GetByID(ctx, card.MediaFileID)
		if err != nil {
			return err
		}
		if file == nil || file.FilePath != card.InputPath {
			return storagesource.ErrReferenceConflict
		}
		session := &playback.Session{ID: card.SessionID, UserID: card.UserID, ProfileID: card.ProfileID, MediaFileID: card.MediaFileID}
		opened, _, err := h.NativeVideo.OpenDelegatedBound(ctx, session, file)
		if err != nil {
			return err
		}
		defer opened.Close()
		return opened.Reauthorize(ctx)
	}
	previous := h.tm.RestoreInputSource
	h.tm.RestoreInputSource = func(ctx context.Context, card *playback.RecipeCard, opts *playback.TranscodeOpts) error {
		if !storagesource.IsNativeLocation(opts.InputPath) {
			if previous != nil {
				return previous(ctx, card, opts)
			}
			return nil
		}
		if h.NativeVideo == nil {
			return nativeVideoUnavailable()
		}
		file, err := h.ResolveNativeVideoFile(ctx, card.MediaFileID)
		if err != nil {
			return err
		}
		if file.FilePath != card.InputPath {
			return storagesource.ErrReferenceConflict
		}
		session := &playback.Session{ID: card.SessionID, UserID: card.UserID, ProfileID: card.ProfileID, MediaFileID: card.MediaFileID}
		opts.InputSource, opts.InputFormat, err = h.NativeVideo.InputSource(ctx, session, file)
		return err
	}
}
