package handlers

import (
	"context"

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
