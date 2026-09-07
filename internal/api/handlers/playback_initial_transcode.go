package handlers

import (
	"context"
	"fmt"
	"strings"

	"github.com/Silo-Server/silo-server/internal/models"
	"github.com/Silo-Server/silo-server/internal/noderouting"
	"github.com/Silo-Server/silo-server/internal/playback"
)

// prepareInitialTranscodeV3 freezes a single executor recipe. It never
// admits a playback process, allocates output directories or retries another recipe.
func (h *PlaybackHandler) prepareInitialTranscodeV3(ctx context.Context, session *playback.Session, file *models.MediaFile, result playback.PlannerResultV3) (playback.RecipeCard, playback.TranscodeOpts, error) {
	fail := func(err error) (playback.RecipeCard, playback.TranscodeOpts, error) {
		return playback.RecipeCard{}, playback.TranscodeOpts{}, err
	}
	if session == nil || session.Executor == nil || file == nil || result.Plan == nil || session.TranscodeTransportID == "" {
		return fail(fmt.Errorf("initial transcode requires an executor-bound session"))
	}
	if result.Plan.Delivery != playback.DeliveryTranscodeHLSV3 || result.PlayMethod != playback.PlayTranscode || strings.EqualFold(strings.TrimSpace(result.TargetVideoCodec), "copy") || strings.TrimSpace(result.TargetVideoCodec) == "" || file.IsAudioOnly() {
		return fail(fmt.Errorf("initial transport requires video transcode HLS"))
	}
	if result.Plan.SessionID != session.ID {
		return fail(fmt.Errorf("initial transcode plan session mismatch"))
	}
	cfg := h.playbackConfig()
	outputSubdir, err := session.Executor.OutputSubdir()
	if err != nil {
		return fail(err)
	}
	outputDir, err := session.Executor.OutputDir(cfg.TranscodeDir)
	if err != nil {
		return fail(err)
	}
	timeline, timelineErr := h.prepareTransportTimelineV3(ctx, session, file, result)
	if timelineErr != nil {
		return fail(timelineErr)
	}
	videoCodec := result.TargetVideoCodec
	sourceMetadata := sourceExecutionMetadataV3(file, result)
	sourceProfile, sourceBitDepth := sourceVideoTranscodeFactsV3(file, result)
	opts := playback.TranscodeOpts{InputPath: file.FilePath, OutputDir: outputDir, OutputSubdir: outputSubdir, SessionID: session.ID, SourceVideoCodec: sourceMetadata.VideoCodec, SourceVideoProfile: sourceProfile, SourceVideoBitDepth: sourceBitDepth, SourceAudioChannels: result.SourceAudioChannels, SoftwareVideoDecode: sourceMetadata.SoftwareVideoDecode, ToneMapPolicy: result.ToneMapPolicy, ToneMapMode: result.ToneMapMode, ToneMapSourceKind: result.ToneMapSourceKind, ToneMapRecipeVersion: result.ToneMapRecipeVersion, ToneMapPreflightRequired: result.ToneMapPreflightRequired, ToneMapSourceRevision: result.ToneMapSourceRevision, VideoBitstreamFilter: videoBitstreamFilterForPlanV3(result.Plan), VideoSampleEntry: videoSampleEntryForPlanV3(result.Plan), SeekSeconds: timeline.seekSeconds, StreamOriginSeconds: timeline.streamOriginSeconds, CopySeekAnchorResolved: timeline.copySeekAnchorResolved, StartSegmentNumber: timeline.startSegmentNumber, TargetResolution: result.TargetResolution, TargetCodecVideo: videoCodec, TargetCodecAudio: result.TargetAudioCodec, TargetAudioChannels: result.TargetAudioChannels, TargetAudioBitrateKbps: result.TargetAudioBitrateKbps, TargetBitrateKbps: result.TargetBitrateKbps, SegmentDuration: playback.DefaultSegmentDuration, SegmentRetentionSeconds: cfg.SegmentRetentionSeconds, FFmpegPath: cfg.FFmpegPath, HWAccel: cfg.HWAccel, HWDevice: cfg.HWDevice, AudioTrackIndex: plannedAudioTrackIndexV3(result, session.AudioTrackIndex), SubtitleTrackIndex: result.SubtitleTransportTrackIndex, SubtitleBurnIn: result.SubtitleBurnIn, SubtitleCodec: result.SubtitleCodec, TotalDuration: sourceMetadata.DurationSeconds, FastStart: true, NodeType: playbackNodeIntegratedV3, ExecutionMode: playbackNodeIntegratedV3, FFmpegLogSink: h.FFmpegLogSink}
	opts.Executor = new(*session.Executor)
	opts.TranscodeTransportID = session.TranscodeTransportID
	if opts.ToneMapMode != "" {
		opts.ToneMapDVConfigPresent = sourceMetadata.ToneMapDVConfigPresent
		opts.ToneMapDVBLCompatIDPresent = sourceMetadata.ToneMapDVBLCompatIDPresent
		opts.ToneMapDVBLPresent = sourceMetadata.ToneMapDVBLPresent
		opts.ToneMapDVRPUPresent = sourceMetadata.ToneMapDVRPUPresent
	}
	remote := session.RoutingExecution == string(noderouting.ExecutionTranscode)
	if !remote {
		opts, err = playback.PrepareFrozenTranscodeOpts(ctx, opts)
		if err != nil {
			return fail(err)
		}
	}
	card := playback.NewRecipeCard(session.UserID, session.ProfileID, file.ID, session.TranscodeNodeURL, opts)
	card.OriginalStartedAt = session.StartedAt
	card.RoutingWorkload = string(noderouting.WorkloadVideoTranscode)
	card.RoutingExecution = string(noderouting.ExecutionAPI)
	card.RoutingEgress = string(noderouting.EgressAPI)
	if remote {
		card.RoutingExecution = session.RoutingExecution
		card.RoutingExecutionNodeID = session.RoutingExecutionNodeID
		card.RoutingEgress = session.RoutingEgress
		card.RoutingEgressNodeID = session.RoutingEgressNodeID
		// Only the selected worker resolves its hardware/device policy. No API
		// hardware preflight runs before the remote preparation exchange.
		card, err = h.prepareRemoteExecutorRecipeV3(ctx, card)
		if err != nil {
			return fail(err)
		}
		opts = card.TranscodeOpts(outputDir, cfg.FFmpegPath, h.FFmpegLogSink)
	}
	if err := playback.ValidateCopyFMP4RecipeCard(card); err != nil {
		return fail(err)
	}
	return card, opts, nil
}
