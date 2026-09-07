package handlers

import (
	"context"
	"net/http"

	"github.com/Silo-Server/silo-server/internal/playback"
	"github.com/Silo-Server/silo-server/internal/userstore"
)

// ClientTimelineAcceptedProgress returns the stored receipt's sequence and both
// clocks. The runtime must use Position, never ItemPosition, for its session.
func ClientTimelineAcceptedProgress(binding playback.InitialActivationBindingV3, receipt *userstore.PlaybackProgressReceipt) (*PlaybackAcceptedProgress, error) {
	if receipt == nil {
		return nil, nil
	}
	if binding.Validate() != nil || receipt.Fence != binding.Fence {
		return nil, playback.ErrClientPlaybackTimelineV3
	}
	sample := receipt.Sample
	if sample.DurationSeconds != binding.Progress.DurationSeconds || sample.PersistenceDisabled || sample.Hints != binding.Progress.Hints || sample.Thresholds != binding.Progress.Thresholds {
		return nil, playback.ErrClientPlaybackTimelineV3
	}
	local, err := binding.ClientTimeline.LocalPosition(sample.PositionSeconds)
	if err != nil {
		return nil, err
	}
	return &PlaybackAcceptedProgress{Sequence: sample.Sequence, Position: local, IsPaused: sample.Paused, TimelineID: binding.ClientTimeline.TimelineID, ItemPosition: new(sample.PositionSeconds)}, nil
}

// ClientPlaybackTimelineService reads the trusted complete manifest under the
// current caller's access/installation scope. Runtime integration owns it.
type ClientPlaybackTimelineService interface {
	GetClientPlaybackTimeline(context.Context, PlaybackCaller, int) (playback.ClientPlaybackManifestV3, error)
}

func (h *PlaybackHandler) SupportsBoundClientTimeline() bool {
	return h.initialFlow != nil && h.initialFlow.TimelineResolver != nil
}

func (h *PlaybackHandler) GetClientPlaybackTimeline(ctx context.Context, caller PlaybackCaller, fileID int) (playback.ClientPlaybackManifestV3, error) {
	if err := h.validatePlaybackCaller(ctx, caller); err != nil {
		return playback.ClientPlaybackManifestV3{}, err
	}
	if !h.SupportsBoundClientTimeline() {
		return playback.ClientPlaybackManifestV3{}, playbackOperationError(http.StatusConflict, "capability_not_configured", "Bound client timelines are not configured")
	}
	if _, err := h.initialFlow.Control.GetAdmittedPlaybackSource(ctx, caller.UserID); err != nil {
		return playback.ClientPlaybackManifestV3{}, playbackAuthorityOperationError()
	}
	manifest, err := h.initialFlow.TimelineResolver.ResolveClientPlaybackManifest(ctx, caller.UserID, caller.ProfileID, fileID)
	if err != nil || manifest.Validate() != nil {
		return playback.ClientPlaybackManifestV3{}, playbackOperationError(http.StatusConflict, "timeline_unavailable", "Playback timeline is unavailable")
	}
	return manifest, nil
}

func initialTimelineMutationResponse(binding playback.InitialActivationBindingV3, result userstore.PlaybackProgressResult, draining bool) (PlaybackMutationView, error) {
	response := initialMutationResponse(result, draining)
	if binding.ClientTimeline == (playback.ClientPlaybackTimelineV3{}) {
		return response, nil
	}
	accepted, err := ClientTimelineAcceptedProgress(binding, result.State.Last)
	if err != nil {
		return PlaybackMutationView{}, playbackAuthorityOperationError()
	}
	response.Accepted = accepted
	return response, nil
}

func initialTimelineSample(binding playback.InitialActivationBindingV3, timelineID string, sequence int64, position float64, paused bool) (userstore.PlaybackProgressSample, error) {
	if binding.ClientTimeline != (playback.ClientPlaybackTimelineV3{}) {
		sample, err := binding.ClientTimelineSample(timelineID, sequence, position, paused)
		if err != nil {
			return userstore.PlaybackProgressSample{}, playbackOperationError(http.StatusConflict, "timeline_changed", "Playback timeline or part position does not match the captured session")
		}
		return sample, nil
	}
	if timelineID != "" {
		return userstore.PlaybackProgressSample{}, playbackOperationError(http.StatusConflict, "timeline_changed", "This session has no bound client timeline")
	}
	sample := binding.Progress
	sample.Sequence, sample.PositionSeconds, sample.Paused = sequence, position, paused
	return sample, nil
}
