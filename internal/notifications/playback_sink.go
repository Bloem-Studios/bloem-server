package notifications

import (
	"context"

	"github.com/Silo-Server/silo-server/internal/userstore"
)

var _ userstore.PlaybackProgressSink = (*interestTrackingStore)(nil)

func (s *interestTrackingStore) ReadPlaybackProgress(ctx context.Context, scope userstore.PlaybackProgressScope) (userstore.PlaybackProgressState, error) {
	sink, ok := s.UserStore.(userstore.PlaybackProgressSink)
	if !ok {
		return userstore.PlaybackProgressState{}, userstore.ErrPlaybackSinkUnsupported
	}
	return sink.ReadPlaybackProgress(ctx, scope)
}
func (s *interestTrackingStore) InstallPlaybackAuthority(ctx context.Context, request userstore.InstallPlaybackAuthorityRequest) (userstore.PlaybackProgressResult, error) {
	sink, ok := s.UserStore.(userstore.PlaybackProgressSink)
	if !ok {
		return userstore.PlaybackProgressResult{}, userstore.ErrPlaybackSinkUnsupported
	}
	return sink.InstallPlaybackAuthority(ctx, request)
}
func (s *interestTrackingStore) ApplyPlaybackProgress(ctx context.Context, request userstore.ApplyPlaybackProgressRequest) (userstore.PlaybackProgressResult, error) {
	sink, ok := s.UserStore.(userstore.PlaybackProgressSink)
	if !ok {
		return userstore.PlaybackProgressResult{}, userstore.ErrPlaybackSinkUnsupported
	}
	result, err := sink.ApplyPlaybackProgress(ctx, request)
	if err == nil {
		s.queuePlaybackCommit(request.Scope, result)
	}
	return result, err
}
func (s *interestTrackingStore) StopPlaybackProgress(ctx context.Context, request userstore.StopPlaybackProgressRequest) (userstore.PlaybackProgressResult, error) {
	sink, ok := s.UserStore.(userstore.PlaybackProgressSink)
	if !ok {
		return userstore.PlaybackProgressResult{}, userstore.ErrPlaybackSinkUnsupported
	}
	result, err := sink.StopPlaybackProgress(ctx, request)
	if err == nil {
		s.queuePlaybackCommit(request.Scope, result)
	}
	return result, err
}

// Results describe committed state. Do not pre-read progress or call legacy
// setters: both would escape the concrete sink's transaction and replay fence.
func (s *interestTrackingStore) queuePlaybackCommit(scope userstore.PlaybackProgressScope, result userstore.PlaybackProgressResult) {
	if result.Outcome != userstore.PlaybackProgressApplied && result.Outcome != userstore.PlaybackProgressStopped {
		return
	}
	state := func(p userstore.PlaybackProjectionState) progressState {
		return progressState{exists: p.Exists, inProgress: p.Exists && !p.Completed && p.PositionSeconds > 0, completed: p.Completed}
	}
	if result.HistoryCreated || (result.ProgressChanged && state(result.Before) != state(result.After)) {
		s.updater.QueueItemMutation(s.userID, scope.ProfileID, scope.MediaItemID)
	}
}
